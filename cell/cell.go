// Package cell defines the contracts between the simulation engine and
// cell architectures (built-in or plugin-provided), plus the registry
// that maps cell type identifiers to architectures.
//
// Architectures are immutable descriptions; Instances are the mutable
// per-test-case execution state created from them. The engine owns all
// scheduling and port communication: an Instance never sees another cell,
// never touches a port, and never observes the tick counter.
package cell

import "asmgraph/vm"

// APIVersion is the version of this plugin contract. Native plugins export
// a CellAPIVersion symbol that must equal it.
const APIVersion = 1

// Spec is the puzzle-defined configuration of one cell instance.
type Spec struct {
	ID            string
	Type          string
	ProgramMemory int               // program-memory budget in bytes (programmable cells)
	Ports         [vm.NumPorts]bool // enabled physical ports
	Config        map[string]any    // validated plugin configuration
}

// Env is the per-test-case environment visible to instances. Only stream
// cells normally need it.
type Env struct {
	Inputs map[string][]vm.Word
}

// Architecture is an immutable cell type definition.
type Architecture interface {
	// Type is the stable identifier used in puzzle files, e.g. "standard".
	Type() string
	// Description is a one-line human description.
	Description() string
	// ISA returns the instruction set, or nil for non-programmable cells.
	ISA() *vm.ISA
	// ConfigSchema documents the accepted plugin configuration.
	ConfigSchema() []ConfigField
	// ValidateConfig checks a puzzle-supplied configuration.
	ValidateConfig(cfg map[string]any) []ConfigError
	// New creates fresh execution state for one test case.
	New(spec Spec, env Env) (Instance, error)
}

// StreamDeclarer is implemented by architectures that consume named
// test-case input streams or produce output streams that are validated.
// Puzzle validation uses it to check test cases without knowing about
// specific cell types.
type StreamDeclarer interface {
	InputStreams(cfg map[string]any) []string
	OutputStreams(cfg map[string]any) []string
}

// Instance is the mutable execution state of one cell.
type Instance interface {
	// Inspect returns the observable data storage (registers, stacks,
	// stream progress, ...) as JSON-serializable values.
	Inspect() map[string]any
	// AppendState appends a canonical, deterministic byte encoding of all
	// mutable state. Two instances of the same architecture and spec with
	// equal encodings must behave identically from then on. The engine uses
	// it for exact repeated-state (cycle) detection.
	AppendState(b []byte) []byte
}

// Processor is a programmable instance. The engine fetches and decodes
// instructions, gathers source operands (performing any port reads),
// calls Execute exactly once per instruction, and routes the produced
// output to the destination register or port.
type Processor interface {
	Instance
	// ReadRegister returns a register value. NIL is handled by the engine.
	ReadRegister(code uint8) (vm.Word, error)
	// WriteRegister stores a register value. NIL is handled by the engine.
	WriteRegister(code uint8, v vm.Word) error
	// Execute performs the instruction's local effect. src holds the values
	// of the instruction's source operands in operand order. Execute must be
	// deterministic and may only mutate the receiver.
	Execute(ins *vm.Instruction, src []vm.Word) Outcome
}

// Outcome is the result of executing one instruction.
type Outcome struct {
	// Output is the value for the instruction's destination operand. It is
	// required when the instruction has a destination slot.
	Output    vm.Word
	HasOutput bool
	// Jump transfers control to instruction index Target.
	Jump   bool
	Target int
	// Halt stops this cell permanently after the instruction completes.
	Halt bool
	// Err is a runtime error; it terminates the test case.
	Err error
}

// RequestOp is a device communication intent.
type RequestOp uint8

const (
	RequestNone RequestOp = iota
	RequestRead
	RequestWrite
)

// Request is a device's communication intent for the current tick.
type Request struct {
	Op    RequestOp
	Port  vm.Port // physical port, ANY, or LAST
	Value vm.Word // for writes
}

// Device is a non-programmable instance that communicates through ports
// (input and output cells, or plugin peripherals). Each tick the engine
// calls Request; if the request is matched, exactly one of CommitRead or
// CommitWrite is called with the physical port used.
type Device interface {
	Instance
	Request() Request
	CommitRead(port vm.Port, v vm.Word)
	CommitWrite(port vm.Port)
}

// Source is implemented by instances that emit a test-case input stream.
type Source interface {
	Stream() string
	Position() int
	Len() int
}

// Sink is implemented by instances whose received values are validated
// against an expected output stream. Validation itself lives outside the
// cell (see package validate).
type Sink interface {
	Stream() string
	Values() []vm.Word
}
