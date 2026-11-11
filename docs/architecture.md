# Architecture

## Data flow

```
puzzle.yaml ──► puzzle.Parse ──► Puzzle.Validate(registry)
                                         │
solution.yaml ─► solution.Parse ─► Solution.Validate(puzzle) ──► graph.Build
                 (player hardware: plugin limits + puzzle rules + budget)
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
                 │                                         │
         Step / Run ──► events ──► Session ──►        RunAll ──► Score{cost, ticks, bytes}
                 protocol.Service ──► transport/stdio | transport/web | cmd/asmgraph-wasm (browser)
```

## Packages

| Package | Responsibility | Depends on |
|---|---|---|
| `vm` | `Word` (int32) arithmetic, `Port`, `ISA`/`InstructionDef`, bytecode encode/decode, `Program` image | none |
| `asm` | One shared parser; compiler that resolves operands through a per-slot resolver table and encodes against any `vm.ISA` | `vm` |
| `cell` | Plugin contracts (`Architecture`, `Instance`, `Processor`, `Peripheral`, `Device`, `Source`, `Sink`), hardware descriptions (`HardwareSpec`, cost model, `ValidateHardware`), config schema helpers, `Registry` | `vm` |
| `cells` | Built-in architectures and `ExecCore`, the shared core-instruction semantics | `cell`, `vm` |
| `plugins` | `Loader` abstraction: `Static` and `Native` (Go `plugin` package, build-tagged) | `cell` |
| `graph` | Topology construction and validation | `vm` |
| `puzzle`, `solution` | YAML schemas (v1 and v2), strict decoding, semantic validation with YAML paths and line numbers; puzzle hardware rules and effective limits; player nodes, constraint checks, cost reports; v1 migration | `cell`, `graph`, `diag` |
| `validate` | Exact-output stream validation (outside the cells) | `vm` |
| `engine` | `Prepare` → `Blueprint`; `Machine` (one test case); `RunTest`/`RunAll`; `Session` (stateful API) | everything above |
| `scoring` | Per-dimension comparison, Pareto dominance, file-based personal bests | `engine` |
| `protocol` | Transport-independent command dispatcher and event filtering | `engine` |
| `transport/jsonrpc` | JSON-RPC 2.0 message handling shared by all transports | `protocol` |
| `transport/stdio` | JSON-RPC over newline-delimited stdin/stdout | `transport/jsonrpc` |
| `transport/web` | Optional server: HTTP library API, WebSocket JSON-RPC sessions, static frontend ([web.md](web.md)) | `transport/jsonrpc` |
| `library` | Puzzle summaries for puzzle browsers, shared by the server and the browser engine | `puzzle` |
| `cmd/asmgraph-wasm` | The engine as WebAssembly: the JSON-RPC dispatcher behind a two-function JS API | `transport/jsonrpc` |
| `cmd/asmgraph` | CLI | everything |

There are no import cycles. The engine does not import any transport,
rendering, or terminal code, which is what lets the same engine run
natively (CLI, servers) and in the browser as WebAssembly with no changes
beyond `engine.RunYield`.

## Main design decisions

**Architectures and instances are separate.** A `cell.Architecture` is an
immutable description: its ISA, config schema, and constructor. Calling
`New` gives a mutable `Instance` for exactly one test case. Every test
case builds every instance from scratch, so there is no reset logic that
could miss some state.

**Plugins define capabilities, puzzles define restrictions, players
design within both.** A `cell.HardwareSpec` declares what a type can
physically do (ports, port functions, memory, cost, what puzzles may
override). A puzzle's `hardware` rules can only narrow those limits. The
solution's nodes are checked against both, and costs are computed from
the effective model. See [hardware.md](hardware.md).

**Passive hardware is not a fake processor.** Peripherals implement
`cell.Peripheral`: each tick they offer an operation per port, and the
engine matches offers with the same arbitration it uses for every
request, still completing at most one transfer per cell per tick.

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
| Puzzle files | `schema_version: 2` (1 still read) | `puzzle.SchemaVersion`, `puzzle.MinSchemaVersion` |
| Solution files | `schema_version: 2` (1 still read) | `solution.SchemaVersion` |
| Bytecode | `2` (1 still read) | `vm.BytecodeVersion`, stored in `Program` images |
| Plugin contract | `2` | `cell.APIVersion`, checked against a native plugin's `CellAPIVersion` |
| JSON-RPC protocol | `"2"` | `protocol.Version`, reported by `initialize` |
