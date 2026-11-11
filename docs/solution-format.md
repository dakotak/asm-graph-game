# Solution file format

A solution is a player's answer to one puzzle. It is stored apart from the
puzzle file. It contains only what the player controls: **the hardware
they built** (when the puzzle allows it), **connections**, and
**programs**. A solution cannot redefine the puzzle's cells, their types,
memory limits, ports, or tests. Unknown top-level keys such as `cells` or
`tests` cause errors.

## Schema (version 2)

```yaml
schema_version: 2              # 1 or 2; version 1 has no hardware list
puzzle: double-input           # must match the puzzle's id

hardware:                      # player-built nodes; only if the puzzle allows it
  - id: helper                 # unique; not a puzzle cell id
    type: standard             # a type the puzzle's catalog allows
    port_count: 2              # optional; effective default otherwise
    program_memory: 16         # processors; optional
  - id: stack
    type: stack
    port_functions: [PUSH, POP, PEEK, SIZE]   # peripherals; optional
    config: {capacity: 8}                     # optional

connections:                   # bidirectional; from/to is for readability only
  - from: {cell: input, port: P0}
    to:   {cell: processor, port: P0}
  - from: {cell: processor, port: P1}
    to:   {cell: output, port: P0}

programs:                      # cell id -> assembly source
  processor: |
    MOV P0, ACC
    ADD ACC
    MOV ACC, P1
    JMP 0

metadata:                      # optional, free-form; the engine ignores it
  layout:
    processor: {x: 200, y: 0}
```

## Connection rules

| Rule | Diagnostic |
|---|---|
| Both cells must exist (a puzzle cell or a player node) | `unknown cell "x"` |
| Ports must be physical ports the cell has (`P0`…`P(n-1)`). `ANY` and `LAST` are not connectable. | `cell "processor" has no port P5: it has 4 port(s), P0-P3` |
| The puzzle must enable the port on that cell | `port P2 is disabled on cell "input" by the puzzle` |
| Each port joins at most one connection | `port processor.P0 is already used by connections[0]` |
| A cell cannot connect to itself | `cell "a" cannot connect to itself` |
| Two cells may have several connections between them on different ports | (allowed) |

Cycles, disconnected components, and unconnected cells are allowed.
Connections are fixed for the whole run. Positions in `metadata` do not
affect execution.

## Programs

- Keys must be IDs of **programmable** cells: puzzle cells or player-built
  processors.
- A programmable cell with no program is idle: it never executes anything.
- Every program is compiled for its cell's architecture and checked
  against that cell's `program_memory`. See [assembly.md](assembly.md) and
  [bytecode.md](bytecode.md).

## Validation order

`engine.Prepare`, `asmgraph validate|compile|run|test`, and the `compile`
RPC run these checks:

1. Puzzle validation
2. Solution schema and puzzle ID
3. Player hardware: plugin limits and puzzle rules for every node, then
   instance counts, node count, and budget
   (see [hardware.md](hardware.md#player-solutions))
4. Program targets
5. Topology
6. Compilation of every program (each against its cell's program memory
   and port count)

All diagnostics are collected and reported together, so a player sees
every problem at once.
