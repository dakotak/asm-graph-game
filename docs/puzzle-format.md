# Puzzle file format

**A puzzle and all of its test cases are defined in one YAML file.** There
are no separate test files, manifests, or references to outside data. The
player's solution is a different file; see
[solution-format.md](solution-format.md).

## Schema (version 2)

```yaml
schema_version: 2            # required; 1 or 2 (see "Version 1" below)
id: double-input             # required; letters, digits, '.', '_', '-'
name: Double the Input       # optional display name
description: |               # optional
  Multiply every input value by two.

cells:                       # required, non-empty; the fixed hardware
  - id: input                # required, unique
    type: input              # required; a registered cell type
    config:                  # type-specific configuration
      stream: numbers        # (input/output default to one port)

  - id: processor
    type: standard
    program_memory: 32       # required for programmable types; bytes
    port_count: 4            # optional; defaults to the type's default
    ports: [P0, P1, P2, P3]  # optional; enabled ports (default: all of them)
    metadata: {}             # optional, free-form, ignored by the engine

  - id: output
    type: output
    config:
      stream: result

hardware:                    # optional; lets players build their own hardware
  max_cost: 50
  catalog:
    standard: {max: 2}

limits:                      # optional
  max_ticks: 100000          # default 100000; 1..100000000

metadata: {}                 # optional, free-form (difficulty, layout hints, ...)

tests:                       # required, non-empty
  - name: positive           # required, unique
    inputs:                  # one entry per input stream
      numbers: [1, 2, 3, 4]
    outputs:                 # one entry per output stream
      result: [2, 4, 6, 8]
```

Unknown fields cause errors. This catches typos such as `program_mem`.

The `hardware` section, port counts, port functions, costs, and scoring
are described in [hardware.md](hardware.md).

### Version 1

Version-1 files are still read and behave as they always did. In v1:

- every cell has exactly four ports, `P0`–`P3`, and `ports` lists the
  enabled ones;
- `port_count`, `port_functions`, and `hardware` are not allowed;
- `type: stack` means the programmable stack processor and is read as
  `stack_processor` (the name `stack` now belongs to the passive stack).

`asmgraph migrate` converts v1 files to v2 without changing behaviour.

### Cells

| Field | Notes |
|---|---|
| `id` | Unique within the puzzle. Solutions use it in connections and in the `programs` map. |
| `type` | A registered type. Built-in types are `standard`, `stack_processor`, `stack` (passive), `input`, and `output`. `asmgraph types` lists the types, their hardware limits, and config fields. |
| `port_count` | Number of ports (v2). Defaults to the type's default and must be within the type's range. |
| `ports` | The ports a player may connect. If omitted, all of them are enabled. `[]` disables all of them. |
| `port_functions` | For peripherals with port functions (v2), one per port, for example `[PUSH, POP]`. Defaults to the type's default assignment. |
| `program_memory` | Required and positive for programmable types; within the type's range in v2 (`standard`: 1–1024). Not allowed on non-programmable types. Two cells of the same type can have different budgets; their registers and instruction sets stay the same. |
| `config` | Checked against the type's schema. Unknown keys cause errors. |

Built-in configuration:

| Type | Field | Default | Meaning |
|---|---|---|---|
| `input` | `stream` (required) | | Name of the test-case input stream this cell emits |
| `output` | `stream` (required) | | Name of the expected output stream this cell receives |
| `stack` | `capacity` | 8 | Passive stack capacity, 1..64 |
| `stack_processor` | `stack_size` | 16 | Stack capacity, 1..256 |

### Test cases

Every test case runs the same hardware and the same solution, each on a
new machine. A test must provide:

- an `inputs` entry for every input stream bound by an input cell, and
- an `outputs` entry for every output stream bound by an output cell.

A test may not name a stream that no cell is bound to. Values must fit in a
signed 32-bit integer (−2147483648 … 2147483647). Out-of-range values are
errors; they are never wrapped.

Each stream name may be bound to only one cell. A puzzle needs at least
one output cell, because tests are judged on outputs.

**Empty expected outputs.** An output stream with `[]` is satisfied from
the start, and it fails as soon as it receives any value. If every output
stream of a test is empty, the test passes at tick 0 without executing.
Puzzle authors should avoid this unless that is what they want.

## Validation

`asmgraph validate puzzle.yaml`, the `validatePuzzle` RPC, and
`puzzle.Puzzle.Validate` all check:

- the schema version
- puzzle and cell ID syntax, and unique cell IDs
- that cell types are registered
- port names (within the cell's port count) with no duplicates
- program-memory rules (see the table above)
- plugin configuration, checked against the type's schema
- in v2, each fixed cell against its plugin's hardware limits
- the `hardware` rules (see [hardware.md](hardware.md#puzzle-rules))
- unique, non-empty test names
- that every bound stream is present in every test, and no unbound stream appears
- integer range of every input and expected value
- `limits.max_ticks`

Every diagnostic includes the YAML path (for example
`tests[1].inputs.xs[1]`) and, where possible, the file line and column.

## Authoring tips

- **Let players design the machine.** `stack-reverse` supplies only I/O
  and a budget; `polynomial-design` makes memory expensive so a cheap
  design and a fast design compete on different score dimensions.
- **Use program memory to shape the puzzle.** `distributed-polynomial`
  needs 12 bytes for the single-cell answer but gives each cell only 8.
  Run `asmgraph compile --disasm` on a reference solution to see what
  each instruction costs (see [bytecode.md](bytecode.md)).
- **Add decoy cells.** `reverse-sequences` includes a `standard` helper
  cell that is not needed.
- **Cover the boundaries.** Include values near ±2³¹ to test wraparound.
- **Limit ports to steer the topology.** Input and output cells usually
  get a single port.
- **Add a reference solution to CI**:

```bash
asmgraph test puzzle.yaml reference-solution.yaml --cycles
```

## Editing tools

Editors can build the whole file, including tests, as structured data and
export it as YAML. In Go, use `puzzle.Puzzle.Marshal`. Over RPC, use
`loadPuzzle` with inline `yaml` and then `exportPuzzle`. Editors should
call `validatePuzzle` and not re-implement these rules.
