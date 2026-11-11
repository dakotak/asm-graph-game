# Plugins and validators

## Cell plugin contract

A cell type implements `cell.Architecture`:

```go
type Architecture interface {
    Type() string                         // stable identifier used in puzzles
    Description() string
    ISA() *vm.ISA                         // nil for non-programmable cells
    ConfigSchema() []cell.ConfigField
    ValidateConfig(cfg map[string]any) []cell.ConfigError
    New(spec cell.Spec, env cell.Env) (cell.Instance, error)
}
```

`New` is called **once per test case** and returns fresh mutable state.
Do not share mutable state between instances.

A programmable type's instances implement `cell.Processor`:

```go
ReadRegister(code uint8) (vm.Word, error)
WriteRegister(code uint8, v vm.Word) error
Execute(ins *vm.Instruction, src []vm.Word) cell.Outcome
Inspect() map[string]any          // JSON-friendly debug view
AppendState(b []byte) []byte      // canonical encoding of ALL mutable state
```

A **passive peripheral**'s instances implement `cell.Peripheral`. It
serves several ports at once without any instruction processor:

```go
Offers(dst []cell.Offer) []cell.Offer   // this tick: at most one {Port, Op, Value} per port
Commit(port vm.Port, op cell.RequestOp, v vm.Word)  // the one offer the engine matched
```

`Offers` must depend only on the instance's own state. The engine matches
offers against neighbours' requests under the normal arbitration (lowest
port first) and calls `Commit` for at most one of them per tick. An
operation that cannot happen now (push to a full stack, pop from an empty
one) is simply not offered, so blocked operations never partially change
state. See `cells/stack.go`.

A simpler non-programmable type can implement `cell.Device`, which makes
one request per tick like a processor does (the I/O cells do this):

```go
Request() cell.Request            // this tick's intent: none, read, or write (port incl. ANY/LAST)
CommitRead(port vm.Port, v vm.Word)
CommitWrite(port vm.Port)
```

Instances may also implement `cell.PortDescriber`
(`PortFunction(p) string`), so snapshots, the graph, and block reasons
can name a port's function (`P1 (POP)`).

`cell.Spec` gives `New` the instance's configuration: `PortCount`, the
enabled-port mask `Ports`, `PortFunctions` (peripherals), `ProgramMemory`
(processors), and `Config`.

The optional interfaces `cell.StreamDeclarer` (on the architecture) and
`cell.Source` / `cell.Sink` (on instances) connect a type to test-case
streams and output validation.

### Hardware description

An architecture describes itself as hardware by implementing
`cell.Hardware`:

```go
func (Stack) Hardware() cell.HardwareSpec {
    return cell.HardwareSpec{
        Category:      cell.CategoryPeripheral,          // processor | peripheral | io
        Ports:         cell.PortSpec{Min: 2, Max: 8, Default: 4},
        PortFunctions: &cell.PortFunctions{
            Functions: []cell.PortFunction{
                {Name: "PUSH", Direction: cell.DirectionIn, Min: 1, Summary: "..."},
                {Name: "POP", Direction: cell.DirectionOut, Min: 1, Summary: "..."},
                // ...
            },
            Configurable: true,                          // players assign functions per port
            Default:      []string{"PUSH", "POP", "PEEK", "SIZE"},
            Rule:         "At least one PUSH port and one POP port.",
        },
        Cost: cell.CostModel{Base: 5, PerPort: 1, Properties: []cell.PropertyCost{
            {Property: "capacity", Cost: 1, Per: 4},     // 1 per 4 slots, rounded up
        }},
        Overrides: cell.Overrides{Cost: true, Defaults: true}, // what puzzles may change
    }
}
```

Processors also declare `ProgramMemory: &cell.MemorySpec{Min, Max, Default}`.
I/O types set `PuzzleOnly: true`. The registry rejects impossible
descriptions (empty or inverted ranges, more than `vm.MaxPorts` ports,
negative costs, undeclared default functions).

Architectures without `Hardware` get a default description: four fixed
ports, cost 10 + 2/port (processors, with 1–65535 bytes of memory) or
5 + 1/port (other devices), and puzzle-only if they bind test streams.
The multiplier example plugin relies on this.

`cell.ValidateHardware` checks a configuration against these limits; the
engine then applies the puzzle's narrower rules. For rules the spec cannot
express, implement `cell.HardwareValidator`
(`ValidateHardware(cell.HardwareConfig) []cell.ConfigError`). Plugins stay
responsible for their own configuration; the engine enforces the common
constraints. See [hardware.md](hardware.md).

### State description

`cell.StateDescriber` (on the architecture) declares the fields `Inspect`
reports, in display order, with a kind (`word` or `list`) and a summary.
Frontends use it to show a cell's registers before a run starts. Without
it, a type is described by its ISA's addressable registers, so a hidden
register like the standard cell's `BAK` would only appear once running.

## Boundaries

What the engine guarantees, and what plugins must do:

- **Plugins never touch ports.** `Execute` gets its source values already
  resolved and returns `Outcome.Output` for the destination slot. The
  engine performs every read and write, handles blocking, `ANY`, `LAST`,
  and arbitration, and decides which port is used.
- **Plugins only see their own state.** An instance gets no references to
  other cells, the tick counter, or the clock.
- **`Execute` runs exactly once per instruction** and must be
  deterministic. Return errors in `Outcome.Err` (this ends the test case
  as `runtime_error`). Do not panic.
- **`AppendState` must cover every piece of mutable state** that affects
  later behaviour, in a fixed byte order. Cycle detection relies on it.
- **Use `vm.Word`** for every value. Do not use Go `int` in machine
  semantics.
- **Core instructions are fixed.** Build your instruction list with
  `vm.Core("MOV", "ADD", …)` and run them through `cells.ExecCore`.
  `vm.NewISA` rejects a core opcode with another mnemonic or operand
  layout, and a core mnemonic on a non-core opcode.
- Plugin instructions use opcodes `0x40`–`0xFF`, with at most three
  operands and at most one destination.
- Registers: `NIL` (0x00) is always present and handled by the engine.
  `ACC` must use code 0x01. Other registers use codes 0x02–0x0F, and
  their names must not clash with port names (`P0`–`P63`, `ANY`, `LAST`).
- **Ports belong to the engine too.** A peripheral offers operations on
  its own ports; the engine decides which one happens. Offer only ports
  below `Spec.PortCount`, at most one operation per port; anything else is
  a runtime error.

## Example

[`examples/plugins/multiplier`](../examples/plugins/multiplier) adds a
`multiplier` type with a 2-byte `MUL src` instruction:

```go
func New() *Arch {
    instrs := vm.Core("NOP", "MOV", "ADD", "SUB", "NEG", "JMP", "JEZ", "JNZ", "JGZ", "JLZ", "HLT")
    instrs = append(instrs, vm.InstructionDef{
        Opcode: 0x40, Mnemonic: "MUL", Slots: []vm.SlotKind{vm.SlotSource},
        Summary: "ACC = ACC * <source> (wrapping, low 32 bits).",
    })
    isa, _ := vm.NewISA("multiplier", instrs, []vm.RegisterDef{{Code: vm.RegACC, Name: "ACC"}})
    return &Arch{isa: isa}
}

func (c *instance) Execute(ins *vm.Instruction, src []vm.Word) cell.Outcome {
    if out, ok := cells.ExecCore(ins, src, cells.CoreRegs{ACC: &c.acc}); ok {
        return out
    }
    if ins.Def.Opcode == 0x40 {
        c.acc *= src[0]
        return cell.Outcome{}
    }
    return cell.Outcome{Err: fmt.Errorf("unsupported instruction %s", ins.Def.Mnemonic)}
}
```

The assembler, disassembler, and decoder work with the new instruction
without any changes, because they are driven by the ISA.

## Registration

### Static (portable, recommended)

Compile the plugin into your binary and register it:

```go
reg := cells.NewRegistry()  // built-ins
err := plugins.LoadAll(reg, plugins.Static{ID: "multiplier", Register: multiplier.Register})
```

`cell.Registry.Register` rejects duplicate type identifiers, invalid
names, and invalid hardware descriptions.

The plugin contract version is `cell.APIVersion` = 2 (version 2 added
variable port counts in `cell.Spec`, `cell.Peripheral`, and the hardware
description). Native plugins must be rebuilt against it.

### Native Go plugins

Build a `main` package that exports `RegisterCells`. Exporting
`CellAPIVersion` is optional but recommended:

```go
package main

var CellAPIVersion = cell.APIVersion
func RegisterCells(r *cell.Registry) error { return multiplier.Register(r) }
func main() {}
```

```bash
go build -buildmode=plugin -o multiplier.so ./examples/plugins/multiplier/plugin
```

```bash
asmgraph test --plugin multiplier.so examples/plugins/multiplier/puzzle.yaml examples/plugins/multiplier/solution.yaml
```

Go plugin restrictions apply:

- Native plugins work only on Linux, macOS, and FreeBSD, with cgo.
- The plugin must be built with the **same Go toolchain and the same
  versions of every shared package** as the host binary.
- A plugin cannot be unloaded.

On other platforms, or with `CGO_ENABLED=0`, `plugins.Native` returns
`plugins.ErrNativeUnsupported`. Everything else still works, and static
registration is always available. `initialize` over JSON-RPC reports
`nativePlugins: true|false`.

V1 has no scripting runtime or out-of-process plugins.

## Custom validators

Built-in exact-output validation runs incrementally inside the engine's
completion check. It lives in `validate.Exact`, not in the output cell.
You can add post-run checks by implementing `engine.Validator`:

```go
type Validator interface {
    ID() string
    Validate(ctx *engine.ValidationContext) error   // non-nil → test fails
}

type ValidationContext struct {
    Puzzle   *puzzle.Puzzle
    Test     *puzzle.TestCase
    Topology *graph.Topology
    Final    *engine.Snapshot   // deep copy of the final machine state
    Result   engine.TestResult  // outputs, metrics
}
```

Pass validators in `engine.Options.Validators`. They run only after
exact-output validation has passed. They get copies, run outside the cell
engine, and cannot change the simulation. They must be deterministic. A
failing validator turns the result into `failed` with the message
`validator <id>: <error>`.

**Future puzzle hook.** A later puzzle schema version is expected to let
a puzzle name a registered validator ID, for example
`validation: {validator: my-check}`. Schema version 1 does not accept this
field, so that it stays strict.
