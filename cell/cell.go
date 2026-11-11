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
const APIVersion = 2

// Spec is the configuration of one cell instance, whether the puzzle
// supplies it or the player instantiated it.
type Spec struct {
	ID            string
	Type          string
	ProgramMemory int // program-memory budget in bytes (programmable cells)
	// PortCount is the number of physical ports, P0..P(PortCount-1).
	PortCount int
	// Ports marks which of those ports are enabled (len PortCount). Fixed
	// schema-v1 cells may disable some; player hardware enables all.
	Ports []bool
	// PortFunctions is the function of each port for peripherals that
	// declare port functions (len PortCount), otherwise nil.
	PortFunctions []string
	Config        map[string]any // validated plugin configuration
}

// Enabled reports whether physical port p exists and is enabled.
func (s *Spec) Enabled(p vm.Port) bool {
	return p.Physical() && p.Index() < len(s.Ports) && s.Ports[p.Index()]
}

// Hardware returns the hardware configuration described by the spec.
func (s *Spec) Hardware() HardwareConfig {
	return HardwareConfig{Ports: s.PortCount, PortFunctions: s.PortFunctions, ProgramMemory: s.ProgramMemory, Config: s.Config}
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

// StateField describes one entry of the map an Instance's Inspect returns,
// so frontends can lay out a cell's registers before a run starts.
type StateField struct {
	Name    string `json:"name"`    // key in Inspect
	Kind    string `json:"kind"`    // "word" (a single value) or "list"
	Summary string `json:"summary"` // one-line description
}

// StateDescriber is optionally implemented by architectures to declare the
// fields Inspect reports, in display order. Architectures that do not
// implement it are described by their ISA's registers.
type StateDescriber interface {
	StateFields() []StateField
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

func (o RequestOp) String() string {
	switch o {
	case RequestRead:
		return "read"
	case RequestWrite:
		return "write"
	}
	return "none"
}

// Request is a device's communication intent for the current tick.
type Request struct {
	Op    RequestOp
	Port  vm.Port // physical port, ANY, or LAST
	Value vm.Word // for writes
}

// Device is a non-programmable instance that makes a single request per
// tick, like a processor does (input and output cells). Each tick the
// engine calls Request; if the request is matched, exactly one of
// CommitRead or CommitWrite is called with the physical port used.
// Peripherals that serve several ports at once implement Peripheral
// instead.
type Device interface {
	Instance
	Request() Request
	CommitRead(port vm.Port, v vm.Word)
	CommitWrite(port vm.Port)
}

// Offer is one port operation a peripheral is ready to perform this tick.
// For RequestWrite, Value is what a neighbour reading that port receives.
type Offer struct {
	Port  vm.Port // a physical port
	Op    RequestOp
	Value vm.Word
}

// Peripheral is passive hardware that serves its ports concurrently (for
// example a stack with PUSH and POP ports). Each tick the engine calls
// Offers, which must depend only on the instance's own state; an offer per
// port at most. The engine matches offers against neighbours' requests
// under the same rules as every other cell, completing at most one
// transfer per cell per tick (lowest-numbered port first), and then calls
// Commit exactly once for the matched offer. An unmatched offer has no
// effect, so a blocked operation never partially changes state.
type Peripheral interface {
	Instance
	Offers(dst []Offer) []Offer
	// Commit applies the matched offer. For reads, v is the received value.
	Commit(port vm.Port, op RequestOp, v vm.Word)
}

// PortDescriber is optionally implemented by instances to label their
// ports (for example "PUSH") in snapshots and block reasons.
type PortDescriber interface {
	PortFunction(p vm.Port) string
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
