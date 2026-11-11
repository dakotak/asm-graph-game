// Package protocol is the transport-independent command/event API. A
// Service maps named commands with JSON parameters to engine.Session
// operations and forwards committed simulation events. Transports (stdio
// JSON-RPC today, HTTP/WebSocket later) only frame messages.
package protocol

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"asmgraph/asm"
	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/engine"
	"asmgraph/plugins"
	"asmgraph/puzzle"
	"asmgraph/solution"
	"asmgraph/vm"
)

// Version is the protocol version reported by initialize.
const Version = "1"

// ServerName and ServerVersion identify this implementation.
const (
	ServerName    = "asmgraph"
	ServerVersion = "0.1.0"
)

// Error codes. The -327xx/-326xx codes are JSON-RPC 2.0 standard codes;
// -320xx are application codes.
const (
	CodeParseError       = -32700
	CodeInvalidRequest   = -32600
	CodeMethodNotFound   = -32601
	CodeInvalidParams    = -32602
	CodeInternalError    = -32603
	CodeValidationFailed = -32001 // data: {"diagnostics": [...]}
	CodeCompileFailed    = -32002 // data: {"diagnostics": [...]}
	CodeInvalidState     = -32003
	CodeNotFound         = -32004
)

// Error is a structured command error.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

func errf(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// EventLevel filters which events are forwarded.
type EventLevel string

const (
	// LevelNone forwards nothing.
	LevelNone EventLevel = "none"
	// LevelLifecycle forwards started/paused/reset/test/terminal events.
	LevelLifecycle EventLevel = "lifecycle"
	// LevelTicks adds tick.completed, port.transfer, output.validation and
	// cell.blocked/unblocked.
	LevelTicks EventLevel = "ticks"
	// LevelAll adds instruction.completed and cell.state.
	LevelAll EventLevel = "all"
)

func (l EventLevel) rank() int {
	switch l {
	case LevelNone:
		return 0
	case LevelLifecycle:
		return 1
	case LevelTicks:
		return 2
	}
	return 3
}

func eventRank(t engine.EventType) int {
	switch t {
	case engine.EventTickCompleted, engine.EventPortTransfer, engine.EventOutputValidation,
		engine.EventCellBlocked, engine.EventCellUnblocked:
		return 2
	case engine.EventInstructionCompleted, engine.EventCellStateChanged:
		return 3
	}
	return 1
}

// Service executes commands against one session.
type Service struct {
	reg    *cell.Registry
	sess   *engine.Session
	notify func(engine.Event)

	mu       sync.Mutex
	level    EventLevel
	seq      uint64
	shutdown bool
	// AllowFiles permits path-based loading. Transports that expose the
	// service to untrusted clients should disable it.
	AllowFiles bool
}

// NewService creates a service. notify receives forwarded events; it may
// be called from a background goroutine and must be safe for concurrent
// use with the transport's response writer.
func NewService(reg *cell.Registry, notify func(engine.Event)) *Service {
	s := &Service{reg: reg, notify: notify, level: LevelAll, AllowFiles: true}
	s.sess = engine.NewSession(reg, s.onEvent)
	return s
}

// Session exposes the underlying session (for embedding).
func (s *Service) Session() *engine.Session { return s.sess }

func (s *Service) onEvent(e engine.Event) {
	if s.notify == nil {
		return
	}
	s.mu.Lock()
	lvl := s.level
	forward := lvl != LevelNone && eventRank(e.Type) <= lvl.rank()
	if forward {
		// Renumber after filtering so forwarded events are gap-free: a gap
		// in seq always means a lost message.
		s.seq++
		e.Seq = s.seq
	}
	s.mu.Unlock()
	if forward {
		s.notify(e)
	}
}

// ShutdownRequested reports whether a shutdown command was received.
func (s *Service) ShutdownRequested() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.shutdown
}

// Close stops background work.
func (s *Service) Close() { s.sess.Close() }

type handler func(*Service, json.RawMessage) (any, *Error)

var methods map[string]handler

func init() {
	methods = map[string]handler{
		"initialize":     (*Service).initialize,
		"listCellTypes":  (*Service).listCellTypes,
		"validatePuzzle": (*Service).validatePuzzle,
		"loadPuzzle":     (*Service).loadPuzzle,
		"loadSolution":   (*Service).loadSolution,
		"exportPuzzle":   (*Service).exportPuzzle,
		"exportSolution": (*Service).exportSolution,
		"compile":        (*Service).compile,
		"start":          (*Service).start,
		"step":           (*Service).step,
		"run":            (*Service).run,
		"pause":          (*Service).pause,
		"reset":          (*Service).reset,
		"getState":       (*Service).getState,
		"getCell":        (*Service).getCell,
		"getResult":      (*Service).getResult,
		"runTests":       (*Service).runTests,
		"runTest":        (*Service).runTest,
		"setBreakpoints": (*Service).setBreakpoints,
		"assemble":       (*Service).assemble,
		"setEventLevel":  (*Service).setEventLevel,
		"shutdown":       (*Service).doShutdown,
	}
}

// Methods lists the supported command names.
func Methods() []string {
	out := make([]string, 0, len(methods))
	for m := range methods {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
}

// Call executes one command. params may be nil.
func (s *Service) Call(method string, params json.RawMessage) (any, *Error) {
	h, ok := methods[method]
	if !ok {
		return nil, errf(CodeMethodNotFound, "method %q not found", method)
	}
	return h(s, params)
}

func decode(params json.RawMessage, v any) *Error {
	if len(bytes.TrimSpace(params)) == 0 || string(bytes.TrimSpace(params)) == "null" {
		return nil
	}
	d := json.NewDecoder(bytes.NewReader(params))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return errf(CodeInvalidParams, "invalid params: %v", err)
	}
	return nil
}

func diagErr(code int, msg string, l diag.List) *Error {
	return &Error{Code: code, Message: msg, Data: map[string]any{"diagnostics": l}}
}

func stateErr(err error) *Error {
	var l diag.List
	if errors.As(err, &l) {
		return diagErr(CodeCompileFailed, "solution does not compile", l)
	}
	if errors.Is(err, engine.ErrNotStarted) || errors.Is(err, engine.ErrNoPuzzle) || errors.Is(err, engine.ErrNoSolution) {
		return errf(CodeInvalidState, "%v", err)
	}
	return errf(CodeInvalidParams, "%v", err)
}

// SourceParams selects YAML content by file path or inline text.
type SourceParams struct {
	Path string `json:"path,omitempty"`
	YAML string `json:"yaml,omitempty"`
}

func (s *Service) source(params json.RawMessage) ([]byte, string, *Error) {
	var p SourceParams
	if e := decode(params, &p); e != nil {
		return nil, "", e
	}
	switch {
	case p.Path != "" && p.YAML != "":
		return nil, "", errf(CodeInvalidParams, "give either path or yaml, not both")
	case p.YAML != "":
		return []byte(p.YAML), "", nil
	case p.Path != "":
		if !s.AllowFiles {
			return nil, "", errf(CodeInvalidParams, "file access is disabled; send yaml instead")
		}
		data, err := os.ReadFile(p.Path)
		if err != nil {
			return nil, "", errf(CodeNotFound, "%v", err)
		}
		return data, p.Path, nil
	}
	return nil, "", errf(CodeInvalidParams, "path or yaml is required")
}

func (s *Service) initialize(json.RawMessage) (any, *Error) {
	return map[string]any{
		"name": ServerName, "version": ServerVersion,
		"protocolVersion":       Version,
		"bytecodeVersion":       vm.BytecodeVersion,
		"puzzleSchemaVersion":   puzzle.SchemaVersion,
		"solutionSchemaVersion": solution.SchemaVersion,
		"cellApiVersion":        cell.APIVersion,
		"nativePlugins":         plugins.NativeSupported(),
		"cellTypes":             s.reg.Types(),
		"methods":               Methods(),
	}, nil
}

// CellTypeInfo describes an architecture for editors.
type CellTypeInfo struct {
	Type         string             `json:"type"`
	Description  string             `json:"description"`
	Programmable bool               `json:"programmable"`
	Instructions []InstructionInfo  `json:"instructions,omitempty"`
	Registers    []vm.RegisterDef   `json:"registers,omitempty"`
	Config       []cell.ConfigField `json:"config,omitempty"`
}

// InstructionInfo describes one instruction for editors.
type InstructionInfo struct {
	Mnemonic string   `json:"mnemonic"`
	Opcode   string   `json:"opcode"`
	Operands []string `json:"operands"`
	Summary  string   `json:"summary"`
	Core     bool     `json:"core"`
}

// DescribeTypes lists every registered architecture.
func DescribeTypes(reg *cell.Registry) []CellTypeInfo {
	var out []CellTypeInfo
	for _, t := range reg.Types() {
		a, _ := reg.Lookup(t)
		info := CellTypeInfo{Type: t, Description: a.Description(), Config: a.ConfigSchema()}
		if isa := a.ISA(); isa != nil {
			info.Programmable = true
			info.Registers = isa.Registers()
			for _, d := range isa.Instructions() {
				ops := []string{}
				for _, sl := range d.Slots {
					ops = append(ops, sl.String())
				}
				info.Instructions = append(info.Instructions, InstructionInfo{
					Mnemonic: d.Mnemonic, Opcode: fmt.Sprintf("0x%02X", d.Opcode),
					Operands: ops, Summary: d.Summary, Core: d.Opcode < vm.CoreOpcodeLimit,
				})
			}
		}
		out = append(out, info)
	}
	return out
}

// OperandKinds documents what each operand slot kind accepts. Editors use
// it for completion and hover help instead of hard-coding the rules.
var OperandKinds = map[string]string{
	"source":      "a register, a port (P0-P3, ANY, LAST), or an integer literal in [-2147483648, 2147483647]",
	"destination": "a register or a port (P0-P3, ANY, LAST)",
	"target":      "a label, or a 0-based instruction index",
}

// PortInfo documents a port operand.
type PortInfo struct {
	Name        string `json:"name"`
	Physical    bool   `json:"physical"`
	Description string `json:"description"`
}

// Ports lists every port operand.
var Ports = []PortInfo{
	{"P0", true, "Physical port slot 0"},
	{"P1", true, "Physical port slot 1"},
	{"P2", true, "Physical port slot 2"},
	{"P3", true, "Physical port slot 3"},
	{"ANY", false, "Any connected port; lowest-numbered ready port wins. Sets LAST."},
	{"LAST", false, "The port used by the most recent ANY transfer; blocks until one has happened."},
}

// Syntax documents the shared assembly syntax.
var Syntax = map[string]any{
	"comments":         []string{"#", ";"},
	"labelSuffix":      ":",
	"caseSensitive":    false,
	"operandSeparator": ",",
	"integerFormats":   []string{"decimal", "0x hex", "0b binary", "0o octal"},
}

func (s *Service) listCellTypes(json.RawMessage) (any, *Error) {
	return map[string]any{
		"types":        DescribeTypes(s.reg),
		"operandKinds": OperandKinds,
		"ports":        Ports,
		"syntax":       Syntax,
	}, nil
}

// AssembleParams configures assemble.
type AssembleParams struct {
	Type        string `json:"type"`
	Source      string `json:"source"`
	MemoryLimit int    `json:"memoryLimit,omitempty"`
}

// assemble compiles one program for a cell type without touching the
// session. Frontends call it while the player types to get exact bytecode
// sizes and diagnostics from the real assembler.
func (s *Service) assemble(params json.RawMessage) (any, *Error) {
	var p AssembleParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	arch, ok := s.reg.Lookup(p.Type)
	if !ok {
		return nil, errf(CodeInvalidParams, "unknown cell type %q", p.Type)
	}
	isa := arch.ISA()
	if isa == nil {
		return nil, errf(CodeInvalidParams, "cell type %q is not programmable", p.Type)
	}
	if p.MemoryLimit < 0 || len(p.Source) > 1<<20 {
		return nil, errf(CodeInvalidParams, "invalid memoryLimit or source too large")
	}
	out := map[string]any{"ok": true, "limit": p.MemoryLimit, "diagnostics": []diag.Diagnostic{}}
	// Compile without a limit first so the true size is known even when the
	// program does not fit.
	res, errs := asm.Compile(p.Source, isa, asm.Options{})
	if len(errs) > 0 {
		var l diag.List
		for _, e := range errs {
			l = append(l, diag.Diagnostic{Severity: diag.Error, Code: "asm.error", Message: e.Msg, SourceLine: e.Pos.Line, SourceColumn: e.Pos.Column})
		}
		out["ok"], out["diagnostics"] = false, l
		return out, nil
	}
	out["bytes"] = res.Program.Size()
	out["instructions"] = len(res.Instructions)
	out["hex"] = hex.EncodeToString(res.Program.Code)
	var dis []engine.DisasmEntry
	for i := range res.Instructions {
		in := &res.Instructions[i]
		dis = append(dis, engine.DisasmEntry{
			Index: i, Address: int(in.Offset),
			Bytes:      hex.EncodeToString(res.Program.Code[in.Offset : int(in.Offset)+in.Size]),
			Text:       in.Format(isa),
			SourceLine: res.SourceLines[i],
		})
	}
	if dis == nil {
		dis = []engine.DisasmEntry{}
	}
	out["disassembly"] = dis
	if p.MemoryLimit > 0 && res.Program.Size() > p.MemoryLimit {
		_, errs := asm.Compile(p.Source, isa, asm.Options{MemoryLimit: p.MemoryLimit})
		var l diag.List
		for _, e := range errs {
			l = append(l, diag.Diagnostic{Severity: diag.Error, Code: "asm.memory", Message: e.Msg, SourceLine: e.Pos.Line, SourceColumn: e.Pos.Column})
		}
		out["ok"], out["diagnostics"] = false, l
	}
	return out, nil
}

func (s *Service) parsePuzzle(params json.RawMessage) (*puzzle.Puzzle, diag.List, *Error) {
	data, file, e := s.source(params)
	if e != nil {
		return nil, nil, e
	}
	p, l := puzzle.Parse(data, file)
	if l.HasErrors() {
		return nil, l, nil
	}
	l = append(l, p.Validate(s.reg)...)
	return p, l, nil
}

func (s *Service) validatePuzzle(params json.RawMessage) (any, *Error) {
	_, l, e := s.parsePuzzle(params)
	if e != nil {
		return nil, e
	}
	return map[string]any{"valid": !l.HasErrors(), "diagnostics": nonNil(l)}, nil
}

func (s *Service) loadPuzzle(params json.RawMessage) (any, *Error) {
	p, l, e := s.parsePuzzle(params)
	if e != nil {
		return nil, e
	}
	if l.HasErrors() {
		return nil, diagErr(CodeValidationFailed, "puzzle is invalid", l)
	}
	if l2 := s.sess.LoadPuzzle(p); l2.HasErrors() {
		return nil, diagErr(CodeValidationFailed, "puzzle is invalid", l2)
	}
	return map[string]any{"puzzle": p, "diagnostics": nonNil(l)}, nil
}

func (s *Service) loadSolution(params json.RawMessage) (any, *Error) {
	data, file, e := s.source(params)
	if e != nil {
		return nil, e
	}
	if s.sess.Puzzle() == nil {
		return nil, errf(CodeInvalidState, "%v", engine.ErrNoPuzzle)
	}
	sol, l := solution.Parse(data, file)
	if l.HasErrors() {
		return nil, diagErr(CodeValidationFailed, "solution is invalid", l)
	}
	if l2 := s.sess.LoadSolution(sol); l2.HasErrors() {
		return nil, diagErr(CodeValidationFailed, "solution is invalid", l2)
	}
	return map[string]any{"solution": sol, "diagnostics": []diag.Diagnostic{}}, nil
}

func (s *Service) exportPuzzle(json.RawMessage) (any, *Error) {
	p := s.sess.Puzzle()
	if p == nil {
		return nil, errf(CodeInvalidState, "%v", engine.ErrNoPuzzle)
	}
	b, err := p.Marshal()
	if err != nil {
		return nil, errf(CodeInternalError, "%v", err)
	}
	return map[string]any{"yaml": string(b)}, nil
}

func (s *Service) exportSolution(json.RawMessage) (any, *Error) {
	sol := s.sess.Solution()
	if sol == nil {
		return nil, errf(CodeInvalidState, "%v", engine.ErrNoSolution)
	}
	b, err := sol.Marshal()
	if err != nil {
		return nil, errf(CodeInternalError, "%v", err)
	}
	return map[string]any{"yaml": string(b)}, nil
}

func (s *Service) compile(json.RawMessage) (any, *Error) {
	progs, l := s.sess.Compile()
	if l.HasErrors() {
		return nil, diagErr(CodeCompileFailed, "solution does not compile", l)
	}
	total := 0
	for _, p := range progs {
		total += p.Bytes
	}
	return map[string]any{"programs": progs, "totalBytes": total, "diagnostics": nonNil(l)}, nil
}

// TestSelector accepts a test name or a numeric index.
type TestSelector string

// UnmarshalJSON accepts a string or a number.
func (t *TestSelector) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*t = TestSelector(strconv.Itoa(n))
		return nil
	}
	var str string
	if err := json.Unmarshal(b, &str); err != nil {
		return fmt.Errorf("test must be a name or an index")
	}
	*t = TestSelector(str)
	return nil
}

// StartParams configures start.
type StartParams struct {
	Test           TestSelector `json:"test,omitempty"`
	MaxTicks       int64        `json:"maxTicks,omitempty"`
	CycleDetection bool         `json:"cycleDetection,omitempty"`
}

func (s *Service) start(params json.RawMessage) (any, *Error) {
	var p StartParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	snap, err := s.sess.Start(string(p.Test), engine.Options{MaxTicks: p.MaxTicks, CycleDetection: p.CycleDetection})
	if err != nil {
		return nil, stateErr(err)
	}
	return snap, nil
}

// StepParams configures step.
type StepParams struct {
	Ticks    int  `json:"ticks,omitempty"`
	Snapshot bool `json:"snapshot,omitempty"`
}

func (s *Service) step(params json.RawMessage) (any, *Error) {
	var p StepParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	st, tick, err := s.sess.Step(p.Ticks)
	if err != nil {
		return nil, stateErr(err)
	}
	out := map[string]any{"status": st, "tick": tick}
	if p.Snapshot {
		snap, _ := s.sess.State()
		out["snapshot"] = snap
	}
	return out, nil
}

// RunParams configures run.
type RunParams struct {
	MaxTicks int64 `json:"maxTicks,omitempty"`
	Wait     bool  `json:"wait,omitempty"`
	DelayMs  int   `json:"delayMs,omitempty"`
}

func (s *Service) run(params json.RawMessage) (any, *Error) {
	var p RunParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	if p.DelayMs < 0 || p.DelayMs > 60_000 {
		return nil, errf(CodeInvalidParams, "delayMs must be between 0 and 60000")
	}
	if p.Wait {
		if p.DelayMs != 0 {
			return nil, errf(CodeInvalidParams, "delayMs is only valid for background runs")
		}
		res, err := s.sess.Run(p.MaxTicks)
		if err != nil {
			return nil, stateErr(err)
		}
		return map[string]any{"status": res.Status, "tick": res.Metrics.Ticks, "result": res}, nil
	}
	if err := s.sess.RunAsync(p.MaxTicks, time.Duration(p.DelayMs)*time.Millisecond, nil); err != nil {
		return nil, stateErr(err)
	}
	return map[string]any{"running": true}, nil
}

func (s *Service) pause(json.RawMessage) (any, *Error) {
	st, tick, err := s.sess.Pause()
	if err != nil {
		return nil, stateErr(err)
	}
	return map[string]any{"status": st, "tick": tick}, nil
}

func (s *Service) reset(json.RawMessage) (any, *Error) {
	snap, err := s.sess.Reset()
	if err != nil {
		return nil, stateErr(err)
	}
	return snap, nil
}

func (s *Service) getState(json.RawMessage) (any, *Error) {
	snap, err := s.sess.State()
	if err != nil {
		return nil, stateErr(err)
	}
	return snap, nil
}

// CellParams selects a cell.
type CellParams struct {
	Cell string `json:"cell"`
}

func (s *Service) getCell(params json.RawMessage) (any, *Error) {
	var p CellParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	if p.Cell == "" {
		return nil, errf(CodeInvalidParams, "cell is required")
	}
	c, err := s.sess.Cell(p.Cell)
	if err != nil {
		if errors.Is(err, engine.ErrNotStarted) {
			return nil, stateErr(err)
		}
		return nil, errf(CodeNotFound, "%v", err)
	}
	return c, nil
}

func (s *Service) getResult(json.RawMessage) (any, *Error) {
	r, err := s.sess.Result()
	if err != nil {
		return nil, stateErr(err)
	}
	return r, nil
}

// RunTestsParams configures runTests.
type RunTestsParams struct {
	MaxTicks       int64 `json:"maxTicks,omitempty"`
	CycleDetection bool  `json:"cycleDetection,omitempty"`
}

func (s *Service) runTests(params json.RawMessage) (any, *Error) {
	var p RunTestsParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	res, err := s.sess.RunTests(engine.Options{MaxTicks: p.MaxTicks, CycleDetection: p.CycleDetection})
	if err != nil {
		return nil, stateErr(err)
	}
	return res, nil
}

// RunTestParams configures runTest.
type RunTestParams struct {
	Test           TestSelector `json:"test"`
	MaxTicks       int64        `json:"maxTicks,omitempty"`
	CycleDetection bool         `json:"cycleDetection,omitempty"`
}

func (s *Service) runTest(params json.RawMessage) (any, *Error) {
	var p RunTestParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	res, err := s.sess.RunTest(string(p.Test), engine.Options{MaxTicks: p.MaxTicks, CycleDetection: p.CycleDetection})
	if err != nil {
		return nil, stateErr(err)
	}
	return res, nil
}

// BreakpointParams configures setBreakpoints: cell id -> source lines.
type BreakpointParams struct {
	Breakpoints map[string][]int `json:"breakpoints"`
}

func (s *Service) setBreakpoints(params json.RawMessage) (any, *Error) {
	var p BreakpointParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	n := 0
	for cell, lines := range p.Breakpoints {
		for _, l := range lines {
			if l < 1 {
				return nil, errf(CodeInvalidParams, "breakpoint line %d for cell %q must be >= 1", l, cell)
			}
			n++
		}
	}
	if n > 10_000 {
		return nil, errf(CodeInvalidParams, "too many breakpoints")
	}
	s.sess.SetBreakpoints(p.Breakpoints)
	if p.Breakpoints == nil {
		p.Breakpoints = map[string][]int{}
	}
	return map[string]any{"breakpoints": p.Breakpoints}, nil
}

// EventLevelParams configures setEventLevel.
type EventLevelParams struct {
	Level EventLevel `json:"level"`
}

func (s *Service) setEventLevel(params json.RawMessage) (any, *Error) {
	var p EventLevelParams
	if e := decode(params, &p); e != nil {
		return nil, e
	}
	switch p.Level {
	case LevelNone, LevelLifecycle, LevelTicks, LevelAll:
	default:
		return nil, errf(CodeInvalidParams, "level must be none, lifecycle, ticks, or all")
	}
	s.mu.Lock()
	s.level = p.Level
	s.mu.Unlock()
	return map[string]any{"level": p.Level}, nil
}

func (s *Service) doShutdown(json.RawMessage) (any, *Error) {
	s.sess.Close()
	s.mu.Lock()
	s.shutdown = true
	s.mu.Unlock()
	return map[string]any{}, nil
}

func nonNil(l diag.List) diag.List {
	if l == nil {
		return diag.List{}
	}
	return l
}
