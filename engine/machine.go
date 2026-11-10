package engine

import (
	"encoding/binary"
	"fmt"
	"sort"
	"strings"

	"asmgraph/cell"
	"asmgraph/graph"
	"asmgraph/puzzle"
	"asmgraph/validate"
	"asmgraph/vm"
)

const maxOperands = 3

// tickReq is a cell's communication intent for the current tick. A cell
// makes at most one request per tick, so it completes at most one port
// transfer per tick.
type tickReq struct {
	op       cell.RequestOp
	sel      vm.Port // as written: P0..P3, ANY, or LAST
	eligible [vm.NumPorts]bool
	value    vm.Word
	noPort   string // why no port is eligible (e.g. LAST unset)
}

// cellRT is the runtime record of one cell inside a machine.
type cellRT struct {
	bp    *CellBlueprint
	inst  cell.Instance
	proc  cell.Processor
	dev   cell.Device
	src   cell.Source
	sink  cell.Sink
	check *validate.Exact

	// Execution state of the current instruction (processors).
	pc         int
	portVals   [maxOperands]vm.Word
	portHave   [maxOperands]bool
	executed   bool
	pendingOut bool
	outVal     vm.Word
	outcome    cell.Outcome
	halted     bool
	err        error

	// Port-selection history.
	last    vm.Port
	lastSet bool

	// Diagnostics and metrics.
	blocked     bool
	blockReason string
	used        bool
	completedN  int64

	// Per-tick scratch.
	req        tickReq
	reqOperand int
	matched    bool
	completed  bool
	progressed bool
	srcBuf     []vm.Word
	before     []byte
}

type transfer struct {
	w, r         int
	wp, rp       vm.Port
	wRank, rRank int
}

// Machine is one independent execution of a solution against one test
// case. It is not safe for concurrent use; Session adds locking.
type Machine struct {
	bp        *Blueprint
	test      *puzzle.TestCase
	testIndex int
	opts      Options
	maxTicks  int64

	cells  []*cellRT
	tick   int64
	status Status
	msg    string

	instructions int64
	transfers    int64

	rtErr   *RuntimeErrorInfo
	cycle   *CycleInfo
	blocked []BlockedCell
	notes   []string

	seen           map[string]int64
	seenBytes      int64
	cycleExhausted bool

	seq     uint64
	cands   []transfer
	used    []bool
	keyBuf  []byte
	pending []Event
}

// NewMachine creates a fresh machine for test case testIndex. Every cell
// instance is newly constructed, so no state can leak between test cases.
func NewMachine(bp *Blueprint, testIndex int, opts Options) (*Machine, error) {
	if testIndex < 0 || testIndex >= len(bp.Puzzle.Tests) {
		return nil, fmt.Errorf("test index %d out of range", testIndex)
	}
	test := &bp.Puzzle.Tests[testIndex]
	m := &Machine{
		bp: bp, test: test, testIndex: testIndex, opts: opts,
		maxTicks: opts.MaxTicks, status: StatusReady,
		used: make([]bool, len(bp.Cells)),
	}
	if m.maxTicks <= 0 {
		m.maxTicks = int64(bp.Puzzle.MaxTicks())
	}
	if m.opts.MaxCycleStates <= 0 {
		m.opts.MaxCycleStates = DefaultMaxCycleStates
	}
	if m.opts.MaxCycleBytes <= 0 {
		m.opts.MaxCycleBytes = DefaultMaxCycleBytes
	}
	env := cell.Env{Inputs: test.InputWords()}
	expected := test.OutputWords()
	for _, cb := range bp.Cells {
		inst, err := cb.Arch.New(cb.Spec, env)
		if err != nil {
			return nil, fmt.Errorf("cell %s: %w", cb.ID, err)
		}
		c := &cellRT{bp: cb, inst: inst}
		if cb.Programmable {
			p, ok := inst.(cell.Processor)
			if !ok {
				return nil, fmt.Errorf("cell %s: programmable type %s does not implement cell.Processor", cb.ID, cb.Type)
			}
			c.proc = p
		} else {
			d, ok := inst.(cell.Device)
			if !ok {
				return nil, fmt.Errorf("cell %s: non-programmable type %s does not implement cell.Device", cb.ID, cb.Type)
			}
			c.dev = d
		}
		c.src, _ = inst.(cell.Source)
		if s, ok := inst.(cell.Sink); ok {
			c.sink = s
			c.check = validate.NewExact(expected[s.Stream()])
		}
		m.cells = append(m.cells, c)
	}
	if opts.CycleDetection {
		m.seen = map[string]int64{}
	}
	// A test whose expected outputs are all empty is complete before any
	// tick executes.
	if m.allOutputsPassed() {
		m.finish(StatusPassed, "all expected output streams are empty")
	} else if m.seen != nil {
		m.recordState()
	}
	return m, nil
}

// Tick returns the number of ticks executed.
func (m *Machine) Tick() int64 { return m.tick }

// Status returns the current status.
func (m *Machine) Status() Status { return m.status }

// Blueprint returns the machine's blueprint.
func (m *Machine) Blueprint() *Blueprint { return m.bp }

// TestIndex returns the index of the test case being executed.
func (m *Machine) TestIndex() int { return m.testIndex }

// Run executes ticks until a terminal status or until maxSteps ticks have
// run (maxSteps <= 0 means no step budget; the tick limit still applies).
func (m *Machine) Run(maxSteps int64) Status {
	for n := int64(0); !m.status.Terminal() && (maxSteps <= 0 || n < maxSteps); n++ {
		m.Step()
	}
	return m.status
}

// Step executes exactly one tick (no-op once terminal):
//
//  1. Evaluate: each cell, using only its own state, either executes a
//     local instruction or forms one communication request.
//  2. Match: requests are paired across connections by a fixed priority
//     (independent of cell iteration order); each cell joins at most one
//     transfer.
//  3. Commit: matched transfers are applied atomically to both sides;
//     instructions whose last source value just arrived execute.
//  4. Retire: completed instructions advance their program counters.
//  5. Events: committed changes are emitted.
//  6. Completion: failure > runtime error > pass > deadlock > cycle > tick limit.
func (m *Machine) Step() Status {
	if m.status.Terminal() {
		return m.status
	}
	m.status = StatusRunning
	m.tick++
	emitting := m.opts.Events != nil
	for _, c := range m.cells {
		c.req = tickReq{}
		c.matched, c.completed, c.progressed = false, false, false
		if emitting {
			c.before = m.appendCellState(c.before[:0], c)
		}
	}

	// 1. Evaluate.
	for _, c := range m.cells {
		if c.dev != nil {
			if r := c.dev.Request(); r.Op != cell.RequestNone {
				m.setRequest(c, r.Op, r.Port, r.Value)
			}
		} else {
			m.evalProcessor(c)
		}
	}

	// 2. Match.
	accepted := m.match()

	// 3. Commit transfers.
	var outChanged []*cellRT
	for _, t := range accepted {
		w, r := m.cells[t.w], m.cells[t.r]
		v := w.req.value
		if w.req.sel == vm.PortAny {
			w.last, w.lastSet = t.wp, true
		}
		if r.req.sel == vm.PortAny {
			r.last, r.lastSet = t.rp, true
		}
		w.matched, r.matched = true, true
		w.progressed, r.progressed = true, true
		w.used, r.used = true, true
		m.transfers++
		if w.dev != nil {
			w.dev.CommitWrite(t.wp)
		} else {
			w.pendingOut = false
			w.completed = true
		}
		if r.dev != nil {
			r.dev.CommitRead(t.rp, v)
			if r.check != nil && r.check.Observe(v) {
				outChanged = append(outChanged, r)
			}
		} else {
			r.portVals[r.reqOperand] = v
			r.portHave[r.reqOperand] = true
			if m.nextPortSource(r) < 0 {
				m.execute(r)
			}
		}
		if emitting {
			m.emit(EventPortTransfer, w.bp.ID, TransferEvent{
				From:  graph.Endpoint{Cell: w.bp.ID, Port: t.wp.String()},
				To:    graph.Endpoint{Cell: r.bp.ID, Port: t.rp.String()},
				Value: v,
			})
		}
	}

	// 4. Retire completed instructions.
	retired := 0
	progress := len(accepted) > 0
	for _, c := range m.cells {
		if c.progressed {
			progress = true
		}
		if c.completed {
			m.retire(c)
			retired++
		}
	}

	// Blocked bookkeeping (cells whose request was not matched).
	for _, c := range m.cells {
		was := c.blocked
		if c.req.op != cell.RequestNone && !c.matched && c.err == nil {
			c.blocked = true
			c.blockReason = m.blockReason(c)
		} else {
			c.blocked, c.blockReason = false, ""
		}
		if emitting && c.blocked != was {
			if c.blocked {
				ins, line := m.currentInstruction(c)
				m.emit(EventCellBlocked, c.bp.ID, BlockEvent{Reason: c.blockReason, Instruction: ins, SourceLine: line})
			} else {
				m.emit(EventCellUnblocked, c.bp.ID, nil)
			}
		}
	}

	// 5. Events for committed state.
	if emitting {
		for _, c := range m.cells {
			after := m.appendCellState(m.keyBuf[:0], c)
			m.keyBuf = after
			if string(after) != string(c.before) {
				snap := m.cellSnapshot(c)
				m.emit(EventCellStateChanged, c.bp.ID, snap)
			}
		}
		for _, c := range outChanged {
			m.emit(EventOutputValidation, c.bp.ID, m.outputSnapshot(c))
		}
	}

	// 6. Completion.
	m.checkCompletion(progress)
	if emitting {
		m.emit(EventTickCompleted, "", TickEvent{Status: m.status, Instructions: retired, Transfers: len(accepted), Progress: progress})
		m.emitTerminal()
	}
	return m.status
}

func (m *Machine) setRequest(c *cellRT, op cell.RequestOp, sel vm.Port, v vm.Word) {
	c.req = tickReq{op: op, sel: sel, value: v}
	switch {
	case sel.Physical():
		c.req.eligible[sel] = true
	case sel == vm.PortAny:
		for i := range c.req.eligible {
			c.req.eligible[i] = true
		}
	case sel == vm.PortLast:
		if c.lastSet {
			c.req.eligible[c.last] = true
		} else {
			c.req.noPort = "LAST is not set: no ANY transfer has completed on this cell yet"
		}
	}
}

func (m *Machine) nextPortSource(c *cellRT) int {
	ins := &c.bp.Code[c.pc]
	for i, slot := range ins.Def.Slots {
		if slot == vm.SlotSource && ins.Operands[i].Kind == vm.OperandPort && !c.portHave[i] {
			return i
		}
	}
	return -1
}

func (m *Machine) evalProcessor(c *cellRT) {
	if c.halted || len(c.bp.Code) == 0 {
		return
	}
	ins := &c.bp.Code[c.pc]
	if !c.executed {
		if i := m.nextPortSource(c); i >= 0 {
			c.reqOperand = i
			m.setRequest(c, cell.RequestRead, ins.Operands[i].Port, 0)
			return
		}
		m.execute(c)
	}
	if c.err == nil && c.pendingOut {
		dst := ins.Operands[ins.Def.DestIndex()]
		m.setRequest(c, cell.RequestWrite, dst.Port, c.outVal)
	}
}

// execute runs the plugin's local semantics for the current instruction
// exactly once. Port-sourced values were gathered in earlier ticks (or in
// this tick's commit); register and immediate sources are read now.
func (m *Machine) execute(c *cellRT) {
	ins := &c.bp.Code[c.pc]
	src := c.srcBuf[:0]
	for i, slot := range ins.Def.Slots {
		if slot != vm.SlotSource {
			continue
		}
		op := ins.Operands[i]
		var v vm.Word
		switch op.Kind {
		case vm.OperandImmediate:
			v = op.Value
		case vm.OperandPort:
			v = c.portVals[i]
		case vm.OperandRegister:
			if op.Register != vm.RegNIL {
				var err error
				if v, err = c.proc.ReadRegister(op.Register); err != nil {
					c.err = err
					return
				}
			}
		}
		src = append(src, v)
	}
	c.srcBuf = src
	out := c.proc.Execute(ins, src)
	c.executed, c.progressed, c.used = true, true, true
	if out.Err != nil {
		c.err = out.Err
		return
	}
	if out.Jump && (out.Target < 0 || out.Target >= len(c.bp.Code)) {
		c.err = fmt.Errorf("jump target %d out of range", out.Target)
		return
	}
	c.outcome = out
	if d := ins.Def.DestIndex(); d >= 0 {
		if !out.HasOutput {
			c.err = fmt.Errorf("%s produced no value for its destination", ins.Def.Mnemonic)
			return
		}
		dst := ins.Operands[d]
		if dst.Kind == vm.OperandPort {
			c.pendingOut, c.outVal = true, out.Output
			return
		}
		if dst.Register != vm.RegNIL {
			if err := c.proc.WriteRegister(dst.Register, out.Output); err != nil {
				c.err = err
				return
			}
		}
	}
	c.completed = true
}

func (m *Machine) retire(c *cellRT) {
	pc := c.pc
	ins := &c.bp.Code[pc]
	next := pc + 1
	if c.outcome.Jump {
		next = c.outcome.Target
	}
	if next >= len(c.bp.Code) {
		next = 0 // execution wraps from the end of the program to its start
	}
	if c.outcome.Halt {
		c.halted = true
		next = pc
	}
	c.pc = next
	c.portHave = [maxOperands]bool{}
	c.portVals = [maxOperands]vm.Word{}
	c.executed, c.pendingOut, c.outVal = false, false, 0
	c.outcome = cell.Outcome{}
	c.completedN++
	m.instructions++
	if m.opts.Events != nil {
		m.emit(EventInstructionCompleted, c.bp.ID, InstructionEvent{
			PC: pc, Address: int(ins.Offset), Instruction: ins.Format(c.bp.Arch.ISA()),
			SourceLine: c.bp.SourceLines[pc], NextPC: next, Halted: c.halted,
		})
	}
}

// match pairs write and read requests across connections.
//
// Candidate transfers are all connections whose two ends have a write and
// a read request that include those ports. Each candidate gets a rank on
// each side: 0 for a request naming a single port (P0..P3 or LAST), or the
// port index for an ANY request. Candidates are accepted greedily in order
// of (reader rank, writer rank), skipping any whose writer or reader has
// already been matched. Two candidates that share a cell always differ in
// that cell's rank, so the result depends only on the requests, never on
// the order in which cells or connections are visited.
func (m *Machine) match() []transfer {
	m.cands = m.cands[:0]
	topo := m.bp.Topology
	for k := 0; k < topo.NumConnections(); k++ {
		a, b := topo.Link(k)
		m.addCandidate(a, b)
		m.addCandidate(b, a)
	}
	if len(m.cands) == 0 {
		return nil
	}
	sort.SliceStable(m.cands, func(i, j int) bool {
		if m.cands[i].rRank != m.cands[j].rRank {
			return m.cands[i].rRank < m.cands[j].rRank
		}
		return m.cands[i].wRank < m.cands[j].wRank
	})
	for i := range m.used {
		m.used[i] = false
	}
	var out []transfer
	for _, t := range m.cands {
		if m.used[t.w] || m.used[t.r] {
			continue
		}
		m.used[t.w], m.used[t.r] = true, true
		out = append(out, t)
	}
	return out
}

func (m *Machine) addCandidate(w, r graph.Link) {
	cw, cr := m.cells[w.Cell], m.cells[r.Cell]
	if cw.req.op != cell.RequestWrite || !cw.req.eligible[w.Port] {
		return
	}
	if cr.req.op != cell.RequestRead || !cr.req.eligible[r.Port] {
		return
	}
	m.cands = append(m.cands, transfer{
		w: w.Cell, r: r.Cell, wp: w.Port, rp: r.Port,
		wRank: rank(cw, w.Port), rRank: rank(cr, r.Port),
	})
}

func rank(c *cellRT, p vm.Port) int {
	if c.req.sel == vm.PortAny {
		return int(p)
	}
	return 0
}

func (m *Machine) blockReason(c *cellRT) string {
	r := c.req
	verb, want := "read from", "write"
	if r.op == cell.RequestWrite {
		verb, want = "write to", "read"
	}
	if r.noPort != "" {
		return fmt.Sprintf("cannot %s LAST: %s", verb, r.noPort)
	}
	topo := m.bp.Topology
	idx := c.bp.Index
	if r.sel == vm.PortAny {
		var peers []string
		for p := vm.Port(0); p < vm.NumPorts; p++ {
			if l, ok := topo.Peer(idx, p); ok {
				peers = append(peers, fmt.Sprintf("%s via %s", topo.CellID(l.Cell), p))
			}
		}
		if len(peers) == 0 {
			return fmt.Sprintf("waiting to %s ANY: no ports are connected", verb)
		}
		return fmt.Sprintf("waiting to %s ANY: no connected cell is ready to %s (%s)", verb, want, strings.Join(peers, ", "))
	}
	var p vm.Port
	for i, ok := range r.eligible {
		if ok {
			p = vm.Port(i)
		}
	}
	label := p.String()
	if r.sel == vm.PortLast {
		label = "LAST (" + p.String() + ")"
	}
	if !topo.Enabled(idx, p) {
		return fmt.Sprintf("cannot %s %s: port is disabled on this cell", verb, label)
	}
	l, ok := topo.Peer(idx, p)
	if !ok {
		return fmt.Sprintf("cannot %s %s: port is not connected", verb, label)
	}
	return fmt.Sprintf("waiting to %s %s: %s.%s is not ready to %s", verb, label, topo.CellID(l.Cell), l.Port, want)
}

func (m *Machine) allOutputsPassed() bool {
	n := 0
	for _, c := range m.cells {
		if c.check != nil {
			n++
			if c.check.State != validate.Passed {
				return false
			}
		}
	}
	return n > 0
}

func (m *Machine) checkCompletion(progress bool) {
	for _, c := range m.cells {
		if c.check != nil && c.check.State == validate.Failed {
			mm := c.check.Mismatch
			if mm.Expected == nil {
				m.finish(StatusFailed, fmt.Sprintf("output %q received unexpected extra value %d at position %d", c.sink.Stream(), mm.Got, mm.Index))
			} else {
				m.finish(StatusFailed, fmt.Sprintf("output %q expected %d at position %d but received %d", c.sink.Stream(), *mm.Expected, mm.Index, mm.Got))
			}
			return
		}
	}
	for _, c := range m.cells {
		if c.err != nil {
			ins, line := m.currentInstruction(c)
			m.rtErr = &RuntimeErrorInfo{Cell: c.bp.ID, PC: c.pc, Instruction: ins, SourceLine: line, Message: c.err.Error()}
			m.finish(StatusRuntimeError, fmt.Sprintf("runtime error in cell %s at %q: %v", c.bp.ID, ins, c.err))
			return
		}
	}
	if m.allOutputsPassed() {
		m.finish(StatusPassed, "all output streams match")
		return
	}
	if !progress {
		m.blocked = m.blockedCells()
		m.finish(StatusDeadlocked, "no cell can make progress: every active cell is blocked, halted, or idle")
		return
	}
	if m.seen != nil && !m.cycleExhausted {
		if first, ok := m.recordState(); ok {
			m.cycle = &CycleInfo{FirstTick: first, RepeatTick: m.tick, Period: m.tick - first}
			m.blocked = m.blockedCells()
			m.finish(StatusCycleDetected, fmt.Sprintf("machine state at tick %d repeats the state at tick %d; it will never complete", m.tick, first))
			return
		}
	}
	if m.tick >= m.maxTicks {
		m.blocked = m.blockedCells()
		m.finish(StatusTickLimit, fmt.Sprintf("tick limit of %d reached before all outputs were satisfied", m.maxTicks))
	}
}

func (m *Machine) finish(s Status, msg string) {
	m.status, m.msg = s, msg
	if s == StatusPassed && len(m.opts.Validators) > 0 {
		res := m.Result()
		ctx := &ValidationContext{Puzzle: m.bp.Puzzle, Test: m.test, Topology: m.bp.Topology, Final: m.Snapshot(), Result: *res}
		for _, v := range m.opts.Validators {
			if err := v.Validate(ctx); err != nil {
				m.status, m.msg = StatusFailed, fmt.Sprintf("validator %s: %v", v.ID(), err)
				return
			}
		}
	}
}

func (m *Machine) blockedCells() []BlockedCell {
	var out []BlockedCell
	for _, c := range m.cells {
		if c.blocked && c.proc != nil {
			ins, line := m.currentInstruction(c)
			out = append(out, BlockedCell{Cell: c.bp.ID, Instruction: ins, SourceLine: line, Reason: c.blockReason})
		}
	}
	return out
}

func (m *Machine) currentInstruction(c *cellRT) (string, int) {
	if c.proc == nil || len(c.bp.Code) == 0 {
		return "", 0
	}
	return c.bp.Code[c.pc].Format(c.bp.Arch.ISA()), c.bp.SourceLines[c.pc]
}

// recordState stores the canonical machine state and reports the tick at
// which an identical state was first seen. Map keys are the complete state
// encodings, so a match is an exact equality, never a hash collision.
func (m *Machine) recordState() (int64, bool) {
	key := m.stateKey()
	if first, ok := m.seen[string(key)]; ok {
		return first, true
	}
	if len(m.seen) >= m.opts.MaxCycleStates || m.seenBytes+int64(len(key)) > m.opts.MaxCycleBytes {
		m.cycleExhausted = true
		m.notes = append(m.notes, fmt.Sprintf("cycle detection stopped at tick %d after reaching its memory limit", m.tick))
		m.seen = nil
		return 0, false
	}
	m.seen[string(key)] = m.tick
	m.seenBytes += int64(len(key))
	return 0, false
}

// stateKey encodes every piece of state that influences future execution:
// program counters, pending instruction state, port-selection history,
// halt flags, and each instance's canonical data (registers, stacks, input
// cursors, received outputs). The tick counter is deliberately excluded.
func (m *Machine) stateKey() []byte {
	b := m.keyBuf[:0]
	for _, c := range m.cells {
		b = m.appendCellState(b, c)
	}
	m.keyBuf = b
	return b
}

func (m *Machine) appendCellState(b []byte, c *cellRT) []byte {
	b = binary.AppendUvarint(b, uint64(c.pc))
	var flags byte
	if c.executed {
		flags |= 1
	}
	if c.pendingOut {
		flags |= 2
	}
	if c.halted {
		flags |= 4
	}
	if c.lastSet {
		flags |= 8
	}
	for i, h := range c.portHave {
		if h {
			flags |= 16 << i
		}
	}
	b = append(b, flags, byte(c.last))
	for i, h := range c.portHave {
		if h {
			b = binary.BigEndian.AppendUint32(b, uint32(c.portVals[i]))
		}
	}
	if c.pendingOut {
		b = binary.BigEndian.AppendUint32(b, uint32(c.outVal))
	}
	// Length-prefix the instance state so concatenation is unambiguous.
	at := len(b)
	b = append(b, 0, 0, 0, 0)
	b = c.inst.AppendState(b)
	binary.BigEndian.PutUint32(b[at:], uint32(len(b)-at-4))
	return b
}

func (m *Machine) emit(t EventType, cellID string, data any) {
	if m.opts.Events == nil {
		return
	}
	var seq uint64
	if m.opts.Sequence != nil {
		*m.opts.Sequence++
		seq = *m.opts.Sequence
	} else {
		m.seq++
		seq = m.seq
	}
	m.opts.Events(Event{Seq: seq, Tick: m.tick, Type: t, Test: m.test.Name, Cell: cellID, Data: data})
}

func (m *Machine) emitTerminal() {
	if !m.status.Terminal() {
		return
	}
	switch m.status {
	case StatusDeadlocked:
		m.emit(EventDeadlockDetected, "", map[string]any{"blocked": m.blocked})
	case StatusCycleDetected:
		m.emit(EventCycleDetected, "", m.cycle)
	case StatusRuntimeError:
		m.emit(EventRuntimeError, m.rtErr.Cell, m.rtErr)
	}
	m.emit(EventSimulationCompleted, "", m.Result())
}

// Result returns the test result. It is final once Status is terminal.
func (m *Machine) Result() *TestResult {
	r := &TestResult{
		Test: m.test.Name, Index: m.testIndex, Status: m.status,
		Passed: m.status == StatusPassed, Message: m.msg,
		Metrics: m.metrics(), Outputs: m.outputs(),
		Blocked: m.blocked, Cycle: m.cycle, Error: m.rtErr,
		Notes: m.notes,
	}
	return r
}

func (m *Machine) metrics() Metrics {
	mt := Metrics{
		Ticks: m.tick, Instructions: m.instructions, Transfers: m.transfers,
		ProgramBytes:  map[string]int{},
		CellsSupplied: len(m.cells), Connections: m.bp.Topology.NumConnections(),
	}
	for _, c := range m.cells {
		if c.bp.Program != nil {
			mt.ProgramBytes[c.bp.ID] = c.bp.Program.Size()
			mt.TotalProgramBytes += c.bp.Program.Size()
		}
		if c.used {
			mt.CellsUsed++
		}
	}
	return mt
}

func (m *Machine) outputs() []OutputSnapshot {
	var out []OutputSnapshot
	for _, c := range m.cells {
		if c.check != nil {
			out = append(out, m.outputSnapshot(c))
		}
	}
	return out
}

func (m *Machine) outputSnapshot(c *cellRT) OutputSnapshot {
	return OutputSnapshot{
		Cell: c.bp.ID, Stream: c.sink.Stream(), State: c.check.State,
		Expected: append([]vm.Word{}, c.check.Expected...),
		Received: append([]vm.Word{}, c.sink.Values()...),
		Mismatch: c.check.Mismatch,
	}
}

// Snapshot returns a complete, deep-copied view of the machine.
func (m *Machine) Snapshot() *Snapshot {
	s := &Snapshot{
		Puzzle: m.bp.Puzzle.ID, Test: m.test.Name, TestIndex: m.testIndex,
		Tick: m.tick, Status: m.status, Message: m.msg,
		Connections: m.bp.Topology.Connections(),
		Inputs:      []InputSnapshot{}, Outputs: m.outputs(),
		Metrics: m.metrics(),
	}
	for _, c := range m.cells {
		s.Cells = append(s.Cells, m.cellSnapshot(c))
		if c.src != nil {
			s.Inputs = append(s.Inputs, InputSnapshot{Cell: c.bp.ID, Stream: c.src.Stream(), Position: c.src.Position(), Length: c.src.Len()})
		}
	}
	if s.Outputs == nil {
		s.Outputs = []OutputSnapshot{}
	}
	return s
}

// CellSnapshot returns the snapshot of one cell by id.
func (m *Machine) CellSnapshot(id string) (*CellSnapshot, bool) {
	for _, c := range m.cells {
		if c.bp.ID == id {
			s := m.cellSnapshot(c)
			return &s, true
		}
	}
	return nil, false
}

func (m *Machine) cellSnapshot(c *cellRT) CellSnapshot {
	cs := CellSnapshot{
		ID: c.bp.ID, Type: c.bp.Type, Programmable: c.bp.Programmable,
		ProgramMemory: c.bp.Spec.ProgramMemory,
		Blocked:       c.blocked, BlockReason: c.blockReason, Halted: c.halted,
		State: c.inst.Inspect(), Completed: c.completedN,
	}
	if c.bp.Program != nil {
		cs.ProgramBytes = c.bp.Program.Size()
	}
	if c.lastSet {
		cs.Last = c.last.String()
	}
	if c.err != nil {
		cs.Error = c.err.Error()
	}
	if c.req.op != cell.RequestNone {
		op := "read"
		if c.req.op == cell.RequestWrite {
			op = "write"
		}
		cs.Request = op + " " + c.req.sel.String()
	}
	topo := m.bp.Topology
	for p := vm.Port(0); p < vm.NumPorts; p++ {
		ps := PortSnapshot{Port: p.String(), Enabled: topo.Enabled(c.bp.Index, p)}
		if l, ok := topo.Peer(c.bp.Index, p); ok {
			ps.Peer = &graph.Endpoint{Cell: topo.CellID(l.Cell), Port: l.Port.String()}
		}
		cs.Ports = append(cs.Ports, ps)
	}
	switch {
	case c.halted:
		cs.Stage = "halted"
	case c.proc != nil && len(c.bp.Code) == 0:
		cs.Stage = "idle"
	case c.pendingOut:
		cs.Stage = "write"
	case c.dev != nil && c.req.op == cell.RequestNone:
		cs.Stage = "idle"
	default:
		cs.Stage = "ready"
	}
	if c.proc != nil && len(c.bp.Code) > 0 {
		ins := &c.bp.Code[c.pc]
		cs.PC, cs.Address = c.pc, int(ins.Offset)
		cs.Instruction = ins.Format(c.bp.Arch.ISA())
		cs.SourceLine = c.bp.SourceLines[c.pc]
		var p PendingSnapshot
		has := false
		for i, h := range c.portHave {
			if h {
				p.Received = append(p.Received, PendingValue{Operand: i, Value: c.portVals[i]})
				has = true
			}
		}
		if c.pendingOut {
			v := c.outVal
			p.Output = &v
			p.Destination = ins.Operands[ins.Def.DestIndex()].Port.String()
			has = true
		}
		if has {
			cs.Pending = &p
		}
	}
	return cs
}
