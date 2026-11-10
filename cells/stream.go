package cells

import (
	"encoding/binary"
	"fmt"

	"asmgraph/cell"
	"asmgraph/vm"
)

// Type identifiers of the stream cells.
const (
	InputType  = "input"
	OutputType = "output"
)

var streamSchema = []cell.ConfigField{{
	Name: "stream", Type: cell.FieldString, Required: true,
	Description: "Name of the test-case stream this cell is bound to.",
}}

// Input is a non-programmable cell that writes the values of a named
// test-case input stream, in order, to ANY connected port. Once the stream
// is exhausted it stops offering values and never produces another.
type Input struct{}

func (Input) Type() string { return InputType }
func (Input) Description() string {
	return "Emits the values of a test-case input stream through ANY port."
}
func (Input) ISA() *vm.ISA                     { return nil }
func (Input) ConfigSchema() []cell.ConfigField { return streamSchema }
func (Input) ValidateConfig(cfg map[string]any) []cell.ConfigError {
	return configErrors(streamSchema, cfg)
}
func (Input) InputStreams(cfg map[string]any) []string {
	return []string{cell.ConfigString(cfg, "stream", "")}
}
func (Input) OutputStreams(map[string]any) []string { return nil }

func (Input) New(spec cell.Spec, env cell.Env) (cell.Instance, error) {
	name := cell.ConfigString(spec.Config, "stream", "")
	vals, ok := env.Inputs[name]
	if !ok {
		return nil, fmt.Errorf("test case provides no input stream %q", name)
	}
	return &inputCell{stream: name, values: vals}, nil
}

type inputCell struct {
	stream string
	values []vm.Word
	pos    int
}

func (c *inputCell) Stream() string { return c.stream }
func (c *inputCell) Position() int  { return c.pos }
func (c *inputCell) Len() int       { return len(c.values) }

func (c *inputCell) Inspect() map[string]any {
	m := map[string]any{
		"stream":    c.stream,
		"position":  c.pos,
		"length":    len(c.values),
		"exhausted": c.pos >= len(c.values),
	}
	if c.pos < len(c.values) {
		m["next"] = c.values[c.pos]
	}
	return m
}

// The stream contents are fixed for a test case, so the cursor is the
// complete mutable state.
func (c *inputCell) AppendState(b []byte) []byte {
	return binary.AppendUvarint(b, uint64(c.pos))
}

func (c *inputCell) Request() cell.Request {
	if c.pos >= len(c.values) {
		return cell.Request{}
	}
	return cell.Request{Op: cell.RequestWrite, Port: vm.PortAny, Value: c.values[c.pos]}
}

func (c *inputCell) CommitRead(vm.Port, vm.Word) {}
func (c *inputCell) CommitWrite(vm.Port)         { c.pos++ }

// Output is a non-programmable cell that reads from ANY connected port
// every tick and records what it receives. Whether the values are correct
// is decided by the engine's validator, not by the cell.
type Output struct{}

func (Output) Type() string { return OutputType }
func (Output) Description() string {
	return "Receives values through ANY port; validated against an expected stream."
}
func (Output) ISA() *vm.ISA                     { return nil }
func (Output) ConfigSchema() []cell.ConfigField { return streamSchema }
func (Output) ValidateConfig(cfg map[string]any) []cell.ConfigError {
	return configErrors(streamSchema, cfg)
}
func (Output) InputStreams(map[string]any) []string { return nil }
func (Output) OutputStreams(cfg map[string]any) []string {
	return []string{cell.ConfigString(cfg, "stream", "")}
}

func (Output) New(spec cell.Spec, _ cell.Env) (cell.Instance, error) {
	return &outputCell{stream: cell.ConfigString(spec.Config, "stream", "")}, nil
}

type outputCell struct {
	stream   string
	received []vm.Word
}

func (c *outputCell) Stream() string    { return c.stream }
func (c *outputCell) Values() []vm.Word { return c.received }
func (c *outputCell) Request() cell.Request {
	return cell.Request{Op: cell.RequestRead, Port: vm.PortAny}
}
func (c *outputCell) CommitRead(_ vm.Port, v vm.Word) { c.received = append(c.received, v) }
func (c *outputCell) CommitWrite(vm.Port)             {}

func (c *outputCell) Inspect() map[string]any {
	return map[string]any{"stream": c.stream, "received": append([]vm.Word{}, c.received...)}
}

func (c *outputCell) AppendState(b []byte) []byte {
	b = binary.AppendUvarint(b, uint64(len(c.received)))
	for _, v := range c.received {
		b = appendWord(b, v)
	}
	return b
}
