# Player-designed hardware

In schema-v2 puzzles, players do not just program the cells a puzzle
supplies. They **design the machine**: they pick hardware from a catalog,
configure it (port counts, program memory, stack capacity, port
functions), wire it up, and program the processors. Three layers decide
what is possible:

| Layer | Decides | Where |
|---|---|---|
| **Plugins** | What hardware *can* do: category, port range, port functions, program memory range, cost model, which of these puzzles may override | `cell.HardwareSpec` (Go) |
| **Puzzles** | What this puzzle *allows*: types, instance counts, budget, narrower ranges, cost overrides | the `hardware` section of the puzzle YAML |
| **Players** | What they *build* within both | the `hardware` list of the solution YAML |

The Go backend enforces all three. The web IDE mirrors some rules so it
can react instantly, but every design is re-validated, costed, and scored
by the backend.

Assembly programming stays the main challenge. Hardware design adds
engineering tradeoffs around it: a bigger program memory costs money, a
pipeline of small processors is fast but expensive, and a stack saves
code but adds a node.

## Categories

| Category | Runs player code | Examples | Placed by |
|---|---|---|---|
| `processor` | yes (has an ISA and program memory) | `standard`, `stack_processor` | puzzle or player |
| `peripheral` | no: behaviour is exposed through port functions | `stack` | puzzle or player |
| `io` | no: binds a test stream | `input`, `output` | puzzle only |

Peripherals implement `cell.Peripheral`, not a fake instruction processor
(see [plugins.md](plugins.md)).

## Ports

Every cell has its own port count. Ports are numbered `P0`, `P1`, … up to
`P(n-1)`. The plugin declares the supported range (for example `standard`:
1–8, default 4), and the puzzle may narrow it.

- Programs may only name ports their cell has. `MOV ACC, P5` on a
  four-port processor is an assembly error.
- `ANY` and `LAST` work as before, over the cell's configured ports only.
- Connections to ports that do not exist are rejected. The web IDE
  removes them explicitly (and says so) when a player lowers a port count.
- The bytecode can name ports up to `P63` (see [bytecode.md](bytecode.md));
  each plugin sets its own, usually much lower, limit.

In the graph, `P0` is on the left, `P1` right, `P2` top, `P3` bottom, then
around again (`P4` left, …), spread along each side.

## The passive stack

`stack` is a peripheral: a LIFO memory with no program. Each of its ports
has a **function**:

| Function | Direction (seen from a neighbour) | Behaviour |
|---|---|---|
| `PUSH` | in: write to it | Pushes the value. Blocks while the stack is full. |
| `POP` | out: read from it | Returns and removes the top value. Blocks while empty. |
| `PEEK` | out: read from it | Returns the top value without removing it. Blocks while empty. |
| `SIZE` | out: read from it | Returns the current depth. Never blocks. |

Configuration:

| Setting | Range | Default |
|---|---|---|
| `port_count` | 2–8 | 4 |
| `port_functions` | one per port, at least one `PUSH` and one `POP` | `[PUSH, POP, PEEK, SIZE]`, repeated for more ports |
| `config.capacity` | 1–64 | 8 |

A four-port stack can be two `PUSH` and two `POP` ports, so two processors
can feed it, or one port of each function. Neighbours simply
`MOV ACC, P1` into a `PUSH` port or `MOV P2, ACC` from a `POP` port.

**Determinism.** Like every cell, a stack completes **at most one
transfer per tick**. When several neighbours are ready in the same tick,
the engine's fixed arbitration decides (see
[execution.md](execution.md#arbitration)): the lowest-numbered stack
port wins, and the others wait for a later tick. The order in which the
hardware or connections are listed never matters. An operation that
cannot happen (push when full, pop when empty) is simply not offered, so
a blocked access never partially changes the stack. Writing to a port
that only sends (for example a `POP` port) never completes; the deadlock
report says
`cannot write to P0: stack.P1 is a POP port, which only sends values`,
and the web IDE refuses such wires up front.

The old programmable stack (with `PUSH`/`POP` instructions) is still
available as `stack_processor`. Schema-v1 puzzles that say `type: stack`
are migrated to it automatically.

## Cost

Each plugin defines a cost model:

```
cost = base + per_port × ports + Σ ⌈property ÷ per⌉ × property_cost
```

Built-in defaults:

| Type | Base | Per port | Properties |
|---|---|---|---|
| `standard` | 10 | 2 | 1 per 8 bytes of `program_memory` |
| `stack_processor` | 14 | 2 | 1 per 8 bytes of `program_memory`, 1 per 4 `stack_size` |
| `stack` | 5 | 1 | 1 per 4 `capacity` |
| `input`, `output` | 0 | 0 | puzzle-only, never costed |

Unset configuration counts at its default (a stack with no `capacity`
pays for 8). Costs are integers, never negative, and computed by the
backend. **Only player-built hardware costs anything**: the puzzle's fixed
cells, including its I/O, are free.

Puzzles may override the cost model of types whose plugin allows it
(`overrides.cost`), for example to make memory expensive:

```yaml
catalog:
  standard:
    cost:
      base: 10
      port: 2
      properties:
        program_memory: {cost: 2, per: 4}
```

With program memory charged at zero, a six-port processor costs
`10 + 6 × 2 = 22`.

## Puzzle rules

The optional `hardware` section of a schema-v2 puzzle enables player
hardware. Without it the puzzle has a fixed topology, exactly like v1.

```yaml
hardware:
  max_nodes: 3            # player-built nodes (0 or omitted: no limit)
  max_cost: 50            # total cost budget (0 or omitted: no limit)
  ports: {min: 2, max: 6} # port range for every player node
  required: [stack]       # at least one of each
  prohibited: [stack_processor]
  catalog:                # allowed types; omit to allow every player-placeable type
    standard:
      min: 0
      max: 2              # instances
      ports: {min: 2, max: 4, default: 2}
      program_memory: {min: 8, max: 32, default: 16}
    stack:
      max: 1
      port_functions:
        allowed: [PUSH, POP, SIZE]   # no PEEK in this puzzle
        max: {PUSH: 1}
      config:
        capacity: {min: 2, max: 12}  # or values: [4, 8]
      cost: {base: 6}               # only if the plugin allows cost overrides
```

| Rule | Meaning |
|---|---|
| `catalog` keys | Allowed types. When `catalog` is omitted, every registered type that is not puzzle-only is allowed (minus `prohibited`). |
| `min` / `max` / `required` | Instance counts per type. |
| `max_nodes`, `max_cost` | Totals across all player nodes. |
| `ports`, `program_memory` | Ranges; they can only **narrow** the plugin's range. A `default` needs the plugin's `overrides.defaults`. |
| `port_functions.allowed` / `.max` | Which functions players may assign, and how often. Functions the plugin requires (like `PUSH`) cannot be disallowed. |
| `config.<field>` | `min`/`max` for integer fields, or a list of `values`. |
| `cost` | Overrides `base`, `port`, and `properties` (needs `overrides.cost`). |

Puzzle validation rejects any rule that a plugin could never satisfy:
ranges outside the plugin's limits, unknown functions or fields,
overrides the plugin forbids, contradictory required/prohibited types,
or minimums that exceed `max_nodes`.

The **effective** limits (plugin limits narrowed by the puzzle) are what
the IDE's palette shows. They are available from the `loadPuzzle` RPC
(`hardware`) and from `puzzle.Puzzle.HardwareCatalog` in Go. When the
plugin's default port functions break a puzzle rule, the effective
default replaces each offending function with the first allowed function
that still has room.

## Player solutions

A schema-v2 solution lists the player's nodes before the connections:

```yaml
schema_version: 2
puzzle: stack-reverse
hardware:
  - id: cpu
    type: standard
    port_count: 4
    program_memory: 32
  - id: stack
    type: stack
    port_count: 2
    port_functions: [PUSH, POP]
    config: {capacity: 9}
connections:
  - from: {cell: in, port: P0}
    to: {cell: cpu, port: P0}
  # ...
programs:
  cpu: |
    MOV P0, ACC
    # ...
```

Omitted fields take the effective defaults. Node IDs follow the same rules
as cell IDs and may not reuse a puzzle cell's ID. See
[solution-format.md](solution-format.md).

Each node is checked against the plugin's physical limits **and** the
puzzle's rules; both kinds of problem are reported together, one per
field, with YAML paths such as `hardware[1].port_functions`. The
`loadSolution` RPC returns the hardware report (per-node costs, total,
budget) even when the solution is invalid, so editors can show budget
usage next to the violations.

## Scoring

A solution that passes **every** test case is scored on three independent
dimensions. Lower is better in each:

| Dimension | Measure |
|---|---|
| **Hardware cost** | Total cost of the player's hardware |
| **Ticks** | The worst-case tick count across all test cases |
| **Program size** | Total compiled bytecode bytes of every program (not source characters or instruction counts) |

The dimensions are never combined into one number. A cheap, slow design
and a fast, expensive one can both be personal bests. For example,
`polynomial-design` can be solved for cost 20 in 40 ticks with one
processor, or for cost 54 in 28 ticks with a three-stage pipeline.

`engine.SuiteResult.score` carries the score (only when all tests pass).
The CLI prints it after `asmgraph test`, and the web IDE shows it after
**Run all tests**.

**Personal bests** are kept per dimension, together with the solution
that achieved each one. The web IDE keeps them in browser storage; the
optional Go server keeps them as files (`scoring.Store`:
`solutions/<puzzle>/bests/`). Either way the score comes from the engine
(`evaluateSolution`, or the server's scores endpoint), which re-validates
the solution and runs every test itself; the UI never computes a score. A new result replaces a best only if it is strictly better in
that dimension; ties are broken by the other dimensions in the order
cost, ticks, bytes. `scoring.Dominates` and `scoring.Frontier` give
Pareto comparisons for future leaderboards.

## Migration from schema v1

Version-1 puzzles and solutions keep working unchanged:

- every v1 cell has exactly four ports (`P0`–`P3`), with `ports` choosing
  which are enabled;
- v1 puzzles never allow player hardware;
- `type: stack` in a v1 puzzle means the programmable stack and is read
  as `stack_processor`.

`asmgraph migrate FILE...` rewrites files as v2 with identical behaviour,
editing the YAML tree so comments survive (blank lines between blocks
may not). It prints the result (or rewrites in place with `--write`) and
lists every change. When the new type default has fewer than four ports,
it adds an explicit `port_count: 4`, unless the enabled ports already fit
the default (an input with `ports: [P0]` simply becomes a one-port input).
