package engine

import (
	"asmgraph/graph"
	"asmgraph/vm"
)

// EventType names an event.
type EventType string

const (
	EventSimulationStarted    EventType = "simulation.started"
	EventSimulationPaused     EventType = "simulation.paused"
	EventSimulationReset      EventType = "simulation.reset"
	EventTickCompleted        EventType = "tick.completed"
	EventInstructionCompleted EventType = "instruction.completed"
	EventCellBlocked          EventType = "cell.blocked"
	EventCellUnblocked        EventType = "cell.unblocked"
	EventPortTransfer         EventType = "port.transfer"
	EventCellStateChanged     EventType = "cell.state"
	EventOutputValidation     EventType = "output.validation"
	EventTestCompleted        EventType = "test.completed"
	EventDeadlockDetected     EventType = "deadlock.detected"
	EventCycleDetected        EventType = "cycle.detected"
	EventSimulationCompleted  EventType = "simulation.completed"
	EventRuntimeError         EventType = "runtime.error"
)

// Event describes a committed state change. Seq increases by one for every
// event emitted through a session (or machine), so frontends can detect
// gaps; Tick is the tick whose commit produced the event.
type Event struct {
	Seq  uint64    `json:"seq"`
	Tick int64     `json:"tick"`
	Type EventType `json:"type"`
	Test string    `json:"test,omitempty"`
	Cell string    `json:"cell,omitempty"`
	Data any       `json:"data,omitempty"`
}

// TransferEvent is the payload of port.transfer.
type TransferEvent struct {
	From  graph.Endpoint `json:"from"` // writer
	To    graph.Endpoint `json:"to"`   // reader
	Value vm.Word        `json:"value"`
}

// InstructionEvent is the payload of instruction.completed.
type InstructionEvent struct {
	PC          int    `json:"pc"`
	Address     int    `json:"address"`
	Instruction string `json:"instruction"`
	SourceLine  int    `json:"sourceLine,omitempty"`
	NextPC      int    `json:"nextPc"`
	Halted      bool   `json:"halted,omitempty"`
}

// BlockEvent is the payload of cell.blocked.
type BlockEvent struct {
	Reason      string `json:"reason"`
	Instruction string `json:"instruction,omitempty"`
	SourceLine  int    `json:"sourceLine,omitempty"`
}

// TickEvent is the payload of tick.completed.
type TickEvent struct {
	Status       Status `json:"status"`
	Instructions int    `json:"instructions"`
	Transfers    int    `json:"transfers"`
	Progress     bool   `json:"progress"`
}
