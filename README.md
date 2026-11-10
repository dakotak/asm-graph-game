# asmgraph

A headless, deterministic engine for an assembly-programming puzzle game.
It is inspired by TIS-100, but the machine is a **graph**, not a grid:

- A puzzle supplies a fixed set of **cells** (standard processors, stack
  processors, input and output streams, or plugin architectures).
- The player **connects ports** between cells and **writes assembly** for
  the programmable ones.
- Programs compile to **bytecode**, and every cell has a puzzle-defined
  **program-memory budget in bytes**. Tight budgets push the player to
  spread the work across cells.
- A tick-based simulator runs the solution against every test case on a
  fresh machine and reports pass/fail, deadlocks, cycles, and metrics.

Each puzzle and **all of its test cases live in one YAML file**. The
player's solution (connections and programs) is a **separate YAML file**.

There is no graphical frontend yet. The engine is a Go library with a CLI
and a JSON-RPC 2.0 interface over stdio, so a TUI, a web editor, or an IDE
extension can drive it.

## Quick start

Requires Go 1.22 or later. The only dependency is `gopkg.in/yaml.v3`.

```bash
go build -o bin/asmgraph ./cmd/asmgraph
```

```bash
./bin/asmgraph test examples/puzzles/double-input.yaml examples/solutions/double-input.yaml
```

```
puzzle double-input — Double the Input
  PASS  positive             passed               ticks=15     instructions=15     transfers=8
  PASS  negative             passed               ticks=15     instructions=15     transfers=8
  PASS  boundaries           passed               ticks=11     instructions=11     transfers=6

PASSED: 3/3 test cases passed
ticks: total 41, max 15   instructions: 41   transfers: 22
program bytes: 10 (processor 10/32)
cells used: 3/3   connections: 2
```

More commands:

```bash
./bin/asmgraph compile examples/puzzles/reverse-sequences.yaml examples/solutions/reverse-sequences.yaml --disasm
```

```bash
./bin/asmgraph run examples/puzzles/double-input.yaml examples/solutions/double-input.yaml --test boundaries --trace
```

```bash
./bin/asmgraph validate examples/puzzles/double-input.yaml examples/solutions/invalid/double-input-broken.yaml
```

```bash
go test ./...
```

## Example puzzles

| Puzzle | Shows |
|---|---|
| [`double-input`](examples/puzzles/double-input.yaml) | Basic I/O and 32-bit wraparound |
| [`filter-positive`](examples/puzzles/filter-positive.yaml) | Labels and conditional branching |
| [`combine-streams`](examples/puzzles/combine-streams.yaml) | Two input streams and cell-to-cell exchange without deadlock |
| [`reverse-sequences`](examples/puzzles/reverse-sequences.yaml) | A task that needs the stack processor |
| [`distributed-polynomial`](examples/puzzles/distributed-polynomial.yaml) | 8-byte memory limits force the program across three cells |
| [`plugins/multiplier`](examples/plugins/multiplier) | A third-party cell type, linked statically or loaded as a native Go plugin |

Working solutions are in [`examples/solutions`](examples/solutions). The
files in [`examples/solutions/invalid`](examples/solutions/invalid) show
the diagnostics for bad topology and assembly, a program that is too large,
and a deadlock.

## Repository layout

```
vm/                word semantics, ports, ISA descriptions, bytecode encode/decode
asm/               shared assembly parser and ISA-driven compiler
cell/              plugin contracts (Architecture, Processor, Device) and registry
cells/             built-in cells: standard, stack, input, output (+ ExecCore)
plugins/           static and native Go plugin loaders
graph/             topology: ports, connections, validation
puzzle/            puzzle YAML schema, loading, validation
solution/          solution YAML schema, loading, validation
validate/          exact-output stream validation
engine/            blueprint, deterministic machine, runner, session API
protocol/          transport-independent command/event service
transport/stdio/   JSON-RPC 2.0 over newline-delimited stdio
cmd/asmgraph/      CLI
diag/              structured diagnostics
examples/          puzzles, solutions, plugin example
docs/              documentation
```

## Documentation

| Document | Contents |
|---|---|
| [Architecture](docs/architecture.md) | Packages, data flow, design decisions |
| [CLI reference](docs/cli.md) | Commands, flags, exit codes, JSON output |
| [Puzzle format](docs/puzzle-format.md) | Puzzle YAML schema, validation, authoring |
| [Solution format](docs/solution-format.md) | Solution YAML schema and validation |
| [Assembly language](docs/assembly.md) | Syntax, operands, instruction reference, integer semantics |
| [Bytecode](docs/bytecode.md) | Encoding, program-memory accounting, versioning |
| [Execution model](docs/execution.md) | Ticks, communication, ANY/LAST, completion, deadlock and cycle detection |
| [Plugins and validators](docs/plugins.md) | Writing and registering cell types; custom validators |
| [JSON-RPC protocol](docs/protocol.md) | Commands, events, snapshots |
| [Frontends and debugging](docs/frontends.md) | Integration guide, debugging workflow, puzzle editors |
| [Known limitations](docs/limitations.md) | What V1 deliberately does not do |
