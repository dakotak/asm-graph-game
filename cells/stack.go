package cells

import (
	"encoding/binary"
	"fmt"
	"strings"

	"asmgraph/cell"
	"asmgraph/vm"
)

// StackType is the type identifier of the passive stack peripheral.
const StackType = "stack"

// Stack port functions.
const (
	FnPush = "PUSH"
	FnPop  = "POP"
	FnPeek = "PEEK"
	FnSize = "SIZE"
)

// Passive stack capacity limits.
const (
	DefaultStackCapacity = 8
	MaxStackCapacity     = 64
)

var passiveStackSchema = []cell.ConfigField{{
	Name: "capacity", Type: cell.FieldInt, Default: DefaultStackCapacity,
	Min: cell.Int64(1), Max: cell.Int64(MaxStackCapacity),
	Description: "Maximum number of values the stack holds.",
}}

var stackFunctions = &cell.PortFunctions{
	Functions: []cell.PortFunction{
		{Name: FnPush, Direction: cell.DirectionIn, Min: 1, Summary: "Writing a value pushes it. Blocks while the stack is full."},
		{Name: FnPop, Direction: cell.DirectionOut, Min: 1, Summary: "Reading returns and removes the top value. Blocks while the stack is empty."},
		{Name: FnPeek, Direction: cell.DirectionOut, Summary: "Reading returns the top value without removing it. Blocks while the stack is empty."},
		{Name: FnSize, Direction: cell.DirectionOut, Summary: "Reading returns the current depth. Never blocks."},
	},
	Configurable: true,
	Default:      []string{FnPush, FnPop, FnPeek, FnSize},
	Rule:         "At least one PUSH port and one POP port.",
}

// Stack is a passive LIFO peripheral. It runs no program: each port has a
// function (PUSH, POP, PEEK, or SIZE) and neighbours use the stack simply
// by reading from and writing to those ports.
//
// Every port with a ready function offers its operation each tick (PUSH
// while not full, POP and PEEK while not empty, SIZE always). Like every
// cell, the stack completes at most one transfer per tick; when several
// neighbours are ready at once, the engine's fixed arbitration picks the
// lowest-numbered stack port. An operation that cannot be performed is
// simply not offered, so a blocked access never changes the stack.
type Stack struct{}

func (Stack) Type() string { return StackType }
func (Stack) Description() string {
	return "Passive LIFO stack. Ports are assigned PUSH, POP, PEEK or SIZE; no program."
}
func (Stack) ISA() *vm.ISA { return nil }
func (Stack) Hardware() cell.HardwareSpec {
	return cell.HardwareSpec{
		Category:      cell.CategoryPeripheral,
		Ports:         cell.PortSpec{Min: 2, Max: 8, Default: 4},
		PortFunctions: stackFunctions,
		Cost: cell.CostModel{Base: 5, PerPort: 1, Properties: []cell.PropertyCost{
			{Property: "capacity", Cost: 1, Per: 4},
		}},
		Overrides: cell.Overrides{Cost: true, Defaults: true},
	}
}
func (Stack) StateFields() []cell.StateField {
	return []cell.StateField{{Name: "stack", Kind: "list", Summary: "Stored values; the last element is the top."}}
}
func (Stack) ConfigSchema() []cell.ConfigField { return passiveStackSchema }
func (Stack) ValidateConfig(cfg map[string]any) []cell.ConfigError {
	return configErrors(passiveStackSchema, cfg)
}

func (Stack) New(spec cell.Spec, _ cell.Env) (cell.Instance, error) {
	n := cell.ConfigInt(spec.Config, "capacity", DefaultStackCapacity)
	if n < 1 || n > MaxStackCapacity {
		return nil, fmt.Errorf("capacity %d out of range", n)
	}
	fns := spec.PortFunctions
	if fns == nil {
		fns = stackFunctions.DefaultAssignment(spec.PortCount)
	}
	c := &stackCell{capacity: int(n), fn: make([]string, len(fns))}
	for i, f := range fns {
		c.fn[i] = strings.ToUpper(f)
	}
	return c, nil
}

type stackCell struct {
	capacity int
	fn       []string // function per port
	values   []vm.Word
}

func (c *stackCell) Offers(dst []cell.Offer) []cell.Offer {
	n := len(c.values)
	for i, f := range c.fn {
		p := vm.PortN(i)
		switch {
		case f == FnPush && n < c.capacity:
			dst = append(dst, cell.Offer{Port: p, Op: cell.RequestRead})
		case (f == FnPop || f == FnPeek) && n > 0:
			dst = append(dst, cell.Offer{Port: p, Op: cell.RequestWrite, Value: c.values[n-1]})
		case f == FnSize:
			dst = append(dst, cell.Offer{Port: p, Op: cell.RequestWrite, Value: vm.Word(n)})
		}
	}
	return dst
}

func (c *stackCell) Commit(p vm.Port, op cell.RequestOp, v vm.Word) {
	switch c.fn[p.Index()] {
	case FnPush:
		c.values = append(c.values, v)
	case FnPop:
		c.values = c.values[:len(c.values)-1]
	}
}

func (c *stackCell) PortFunction(p vm.Port) string {
	if p.Physical() && p.Index() < len(c.fn) {
		return c.fn[p.Index()]
	}
	return ""
}

func (c *stackCell) Inspect() map[string]any {
	return map[string]any{
		"stack":    append([]vm.Word{}, c.values...),
		"capacity": c.capacity,
	}
}

// Capacity and port functions are fixed for the instance, so the stored
// values are the complete mutable state.
func (c *stackCell) AppendState(b []byte) []byte {
	b = binary.AppendUvarint(b, uint64(len(c.values)))
	for _, v := range c.values {
		b = appendWord(b, v)
	}
	return b
}
