package engine

import (
	"asmgraph/graph"
	"asmgraph/puzzle"
	"asmgraph/validate"
	"asmgraph/vm"
)

// Status is the execution status of a machine.
type Status string

const (
	StatusReady         Status = "ready"   // created; no tick executed yet
	StatusRunning       Status = "running" // at least one tick executed, not terminal
	StatusPassed        Status = "passed"
	StatusFailed        Status = "failed"
	StatusDeadlocked    Status = "deadlocked"
	StatusCycleDetected Status = "cycle_detected"
	StatusTickLimit     Status = "tick_limit_exceeded"
	StatusRuntimeError  Status = "runtime_error"
)

// Terminal reports whether no further ticks will execute.
func (s Status) Terminal() bool {
	switch s {
	case StatusReady, StatusRunning:
		return false
	}
	return true
}

// Metrics are informational execution statistics for one test case.
type Metrics struct {
	Ticks             int64          `json:"ticks"`
	Instructions      int64          `json:"instructions"`
	Transfers         int64          `json:"transfers"`
	ProgramBytes      map[string]int `json:"programBytes"`
	TotalProgramBytes int            `json:"totalProgramBytes"`
	CellsSupplied     int            `json:"cellsSupplied"`
	CellsUsed         int            `json:"cellsUsed"`
	Connections       int            `json:"connections"`
}

// Snapshot is a complete, serializable view of a machine.
type Snapshot struct {
	Puzzle      string             `json:"puzzle"`
	Test        string             `json:"test"`
	TestIndex   int                `json:"testIndex"`
	Tick        int64              `json:"tick"`
	Status      Status             `json:"status"`
	Message     string             `json:"message,omitempty"`
	Cells       []CellSnapshot     `json:"cells"`
	Connections []graph.Connection `json:"connections"`
	Inputs      []InputSnapshot    `json:"inputs"`
	Outputs     []OutputSnapshot   `json:"outputs"`
	Metrics     Metrics            `json:"metrics"`
}

// CellSnapshot is the observable state of one cell.
type CellSnapshot struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	Programmable  bool   `json:"programmable"`
	ProgramBytes  int    `json:"programBytes,omitempty"`
	ProgramMemory int    `json:"programMemory,omitempty"`
	// PC is the current instruction index; Address its byte offset.
	PC          int    `json:"pc"`
	Address     int    `json:"address"`
	Instruction string `json:"instruction,omitempty"`
	SourceLine  int    `json:"sourceLine,omitempty"`
	// Stage is "idle", "ready", "write" (result held, waiting to send),
	// or "halted".
	Stage   string           `json:"stage"`
	Pending *PendingSnapshot `json:"pending,omitempty"`
	// Request is the communication the cell attempted in the last tick,
	// e.g. "read P0", "write ANY".
	Request     string         `json:"request,omitempty"`
	Blocked     bool           `json:"blocked"`
	BlockReason string         `json:"blockReason,omitempty"`
	Halted      bool           `json:"halted,omitempty"`
	Last        string         `json:"last,omitempty"`
	Ports       []PortSnapshot `json:"ports"`
	State       map[string]any `json:"state"`
	Completed   int64          `json:"instructionsCompleted"`
	Error       string         `json:"error,omitempty"`
}

// PendingSnapshot is intermediate state of a partially executed
// instruction: source values already received, and/or a result waiting to
// be sent.
type PendingSnapshot struct {
	Received    []PendingValue `json:"received,omitempty"`
	Output      *vm.Word       `json:"output,omitempty"`
	Destination string         `json:"destination,omitempty"`
}

// PendingValue is a source operand value received through a port.
type PendingValue struct {
	Operand int     `json:"operand"`
	Value   vm.Word `json:"value"`
}

// PortSnapshot describes one physical port slot.
type PortSnapshot struct {
	Port    string          `json:"port"`
	Enabled bool            `json:"enabled"`
	Peer    *graph.Endpoint `json:"peer,omitempty"`
}

// InputSnapshot is the progress of one input stream.
type InputSnapshot struct {
	Cell     string `json:"cell"`
	Stream   string `json:"stream"`
	Position int    `json:"position"`
	Length   int    `json:"length"`
}

// OutputSnapshot is the validation progress of one output stream.
type OutputSnapshot struct {
	Cell     string             `json:"cell"`
	Stream   string             `json:"stream"`
	State    validate.State     `json:"state"`
	Expected []vm.Word          `json:"expected"`
	Received []vm.Word          `json:"received"`
	Mismatch *validate.Mismatch `json:"mismatch,omitempty"`
}

// BlockedCell explains why a cell could not progress.
type BlockedCell struct {
	Cell        string `json:"cell"`
	Instruction string `json:"instruction,omitempty"`
	SourceLine  int    `json:"sourceLine,omitempty"`
	Reason      string `json:"reason"`
}

// CycleInfo reports an exactly repeated machine state.
type CycleInfo struct {
	FirstTick  int64 `json:"firstTick"`
	RepeatTick int64 `json:"repeatTick"`
	Period     int64 `json:"period"`
}

// RuntimeErrorInfo locates a runtime error.
type RuntimeErrorInfo struct {
	Cell        string `json:"cell"`
	PC          int    `json:"pc"`
	Instruction string `json:"instruction"`
	SourceLine  int    `json:"sourceLine,omitempty"`
	Message     string `json:"message"`
}

// TestResult is the outcome of one test case.
type TestResult struct {
	Test    string            `json:"test"`
	Index   int               `json:"index"`
	Status  Status            `json:"status"`
	Passed  bool              `json:"passed"`
	Message string            `json:"message,omitempty"`
	Metrics Metrics           `json:"metrics"`
	Outputs []OutputSnapshot  `json:"outputs"`
	Blocked []BlockedCell     `json:"blocked,omitempty"`
	Cycle   *CycleInfo        `json:"cycle,omitempty"`
	Error   *RuntimeErrorInfo `json:"error,omitempty"`
	Notes   []string          `json:"notes,omitempty"`
}

// SuiteResult is the outcome of every test case of a puzzle.
type SuiteResult struct {
	Puzzle  string        `json:"puzzle"`
	Passed  bool          `json:"passed"`
	Tests   []*TestResult `json:"tests"`
	Summary Summary       `json:"summary"`
}

// Summary aggregates metrics across test cases.
type Summary struct {
	PassedTests       int            `json:"passedTests"`
	TotalTests        int            `json:"totalTests"`
	TotalTicks        int64          `json:"totalTicks"`
	MaxTicks          int64          `json:"maxTicks"`
	TotalInstructions int64          `json:"totalInstructions"`
	TotalTransfers    int64          `json:"totalTransfers"`
	ProgramBytes      map[string]int `json:"programBytes"`
	TotalProgramBytes int            `json:"totalProgramBytes"`
	CellsSupplied     int            `json:"cellsSupplied"`
	CellsUsed         int            `json:"cellsUsed"`
	Connections       int            `json:"connections"`
}

// Validator is a custom post-run check. Validators run outside the cell
// execution engine after the built-in exact-output validation passes; they
// receive copies of the final state and cannot affect the simulation. A
// non-nil error fails the test case.
type Validator interface {
	ID() string
	Validate(ctx *ValidationContext) error
}

// ValidationContext is the read-only input to a Validator.
type ValidationContext struct {
	Puzzle   *puzzle.Puzzle
	Test     *puzzle.TestCase
	Topology *graph.Topology
	Final    *Snapshot
	Result   TestResult
}

// Options configures a machine.
type Options struct {
	// MaxTicks overrides the puzzle's limits.max_ticks when > 0.
	MaxTicks int64
	// CycleDetection enables exact repeated-state detection. It retains one
	// canonical state encoding per tick and is therefore off by default.
	CycleDetection bool
	// MaxCycleStates and MaxCycleBytes bound cycle-detection memory. When
	// exceeded, detection stops (and the result notes it); the run goes on.
	MaxCycleStates int
	MaxCycleBytes  int64
	// Events receives committed state-change events. Nil disables events.
	Events func(Event)
	// Sequence, if set, is the shared event sequence counter.
	Sequence *uint64
	// Validators are custom post-run validators.
	Validators []Validator
}

// Defaults for cycle-detection memory.
const (
	DefaultMaxCycleStates = 1_000_000
	DefaultMaxCycleBytes  = 256 << 20
)
