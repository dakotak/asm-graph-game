# Solution file format

A solution is a player's answer to one puzzle. It is stored apart from the
puzzle file. It contains only what the player controls: **connections**
and **programs**. A solution cannot redefine cells, types, memory limits,
registers, available ports, or tests. Unknown top-level keys such as
`cells` or `tests` cause errors.

## Schema (version 1)

```yaml
schema_version: 1
puzzle: double-input           # must match the puzzle's id

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
| Both cells must exist in the puzzle | `unknown cell "x"` |
| Ports must be physical slots `P0`–`P3`. `ANY` and `LAST` are not connectable. | `invalid port "P5"` |
| The puzzle must enable the port on that cell | `port P2 is disabled on cell "input" by the puzzle` |
| Each port joins at most one connection | `port processor.P0 is already used by connections[0]` |
| A cell cannot connect to itself | `cell "a" cannot connect to itself` |
| Two cells may have several connections between them on different ports | (allowed) |

Cycles, disconnected components, and unconnected cells are allowed.
Connections are fixed for the whole run. Positions in `metadata` do not
affect execution.

## Programs

- Keys must be IDs of **programmable** cells in the puzzle.
- A programmable cell with no program is idle: it never executes anything.
- Every program is compiled for its cell's architecture and checked
  against that cell's `program_memory`. See [assembly.md](assembly.md) and
  [bytecode.md](bytecode.md).

## Validation order

`engine.Prepare`, `asmgraph validate|compile|run|test`, and the `compile`
RPC run these checks:

1. Puzzle validation
2. Solution schema and puzzle ID
3. Program targets
4. Topology
5. Compilation of every program

All diagnostics are collected and reported together, so a player sees
every problem at once.
