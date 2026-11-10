# Architecture

## Data flow

```
puzzle.yaml ──► puzzle.Parse ──► Puzzle.Validate(registry)
                                         │
solution.yaml ─► solution.Parse ─► Solution.Validate(puzzle) ──► graph.Build
                                         │
                                         ▼
                       engine.Prepare: compile each program (asm + vm.ISA)
                                         │
                                         ▼
                         Blueprint (immutable, shareable)
                                         │
                 ┌───────────────────────┼────────────────────────┐
                 ▼                       ▼                        ▼
       NewMachine(test 0)      NewMachine(test 1)   …   (one fresh machine per test case)
                 │
         Step / Run ──► events ──► Session ──► protocol.Service ──► transport/stdio
```

## Packages

| Package | Responsibility | Depends on |
|---|---|---|
| `vm` | `Word` (int32) arithmetic, `Port`, `ISA`/`InstructionDef`, bytecode encode/decode, `Program` image | none |
| `asm` | One shared parser; compiler that resolves operands through a per-slot resolver table and encodes against any `vm.ISA` | `vm` |
| `cell` | Plugin contracts (`Architecture`, `Instance`, `Processor`, `Device`, `Source`, `Sink`), config schema helpers, `Registry` | `vm` |
| `cells` | Built-in architectures and `ExecCore`, the shared core-instruction semantics | `cell`, `vm` |
| `plugins` | `Loader` abstraction: `Static` and `Native` (Go `plugin` package, build-tagged) | `cell` |
| `graph` | Topology construction and validation | `vm` |
| `puzzle`, `solution` | YAML schemas, strict decoding, semantic validation with YAML paths and line numbers | `cell`, `graph`, `diag` |
| `validate` | Exact-output stream validation (outside the cells) | `vm` |
| `engine` | `Prepare` → `Blueprint`; `Machine` (one test case); `RunTest`/`RunAll`; `Session` (stateful API) | everything above |
| `protocol` | Transport-independent command dispatcher and event filtering | `engine` |
| `transport/stdio` | JSON-RPC 2.0 framing | `protocol` |
| `cmd/asmgraph` | CLI | everything |

There are no import cycles. The engine does not import any transport,
rendering, or terminal code.

## Main design decisions

**Architectures and instances are separate.** A `cell.Architecture` is an
immutable description: its ISA, config schema, and constructor. Calling
`New` gives a mutable `Instance` for exactly one test case. Every test
case builds every instance from scratch, so there is no reset logic that
could miss some state.

**The engine owns everything between cells.** Processors only implement
`Execute(instruction, sourceValues) → Outcome`. The engine handles
fetching, operand routing, port reads and writes, `ANY` and `LAST`,
blocking, program counters, and scheduling. A plugin never sees another
cell, a port, or the tick counter. See [plugins.md](plugins.md).

**Core semantics have one implementation.** `cells.ExecCore` implements
every core instruction. Built-in and third-party processors call it, so
`ADD` means the same thing everywhere. `vm.NewISA` rejects any ISA that
reuses a core opcode or mnemonic with different meaning.

**Program memory is measured in bytes.** Programs compile to a compact
bytecode where small immediates fit in one byte. The size of the encoded
code is checked against the cell's `program_memory`. The source text,
instruction count, and Go memory use do not count. See
[bytecode.md](bytecode.md).

**The simulation runs on one thread and does not depend on visit
order.** Each tick has fixed phases: evaluate, match, commit, retire,
events, completion. Port matching uses a fixed priority, so the result
does not depend on the order in which cells or connections are visited.
No Go map iteration, goroutine scheduling, or wall-clock time affects the
result. Goroutines are used only to pace `Session.RunAsync`, which steps
the machine under a lock. See [execution.md](execution.md).

**Validation lives outside the cells.** Output cells only record values.
`validate.Exact` compares them with the test case incrementally, so a test
ends on the tick its last output arrives. Custom post-run validators
(`engine.Validator`) get read-only copies of the final state.

**Diagnostics are structured.** Every validation or compile problem is a
`diag.Diagnostic` with a code, a YAML path, a file line and column, and,
for assembly errors, the cell and the line and column inside the program.
The CLI and the JSON-RPC API report the same diagnostics.

## Versioning

| Artifact | Version | Where |
|---|---|---|
| Puzzle files | `schema_version: 1` | `puzzle.SchemaVersion` |
| Solution files | `schema_version: 1` | `solution.SchemaVersion` |
| Bytecode | `1` | `vm.BytecodeVersion`, stored in `Program` images |
| Plugin contract | `1` | `cell.APIVersion`, checked against a native plugin's `CellAPIVersion` |
| JSON-RPC protocol | `"1"` | `protocol.Version`, reported by `initialize` |
