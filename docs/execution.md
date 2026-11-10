# Execution model

The simulation is a global, tick-based, single-threaded state machine.
**The same initial state always produces the same next state.** No Go map
iteration order, goroutine scheduling, or wall-clock time affects results.

## State

Each cell's state is in three separate parts:

| Part | Contents | Owner |
|---|---|---|
| Program memory | Encoded bytecode, at most `program_memory` bytes | Blueprint (immutable) |
| Data storage | Registers, stacks, stream cursors, received outputs | `cell.Instance` (plugin) |
| Execution state | Program counter, source values already received, pending output value, halt flag, `LAST` | Engine |

## Tick phases

`Machine.Step` runs one tick:

1. **Evaluate.** Every cell, using only its own state, does one of these:
   - A processor that still needs a source value from a port makes a
     **read request** for that port.
   - A processor that has all its source values **executes** the
     instruction locally, by calling the plugin's `Execute` exactly once.
     If the result goes to a port, it makes a **write request**. Otherwise
     the instruction is complete.
   - A processor holding a result that has not been delivered makes a
     **write request** again.
   - A device (input or output cell) states its request through
     `Device.Request`.
2. **Match.** Read and write requests are paired across connections; see
   [arbitration](#arbitration). Each cell joins at most one transfer.
3. **Commit.** Each matched transfer is applied to both sides at once:
   the writer's pending value is cleared and the reader gets the value. A
   processor that just received its last missing source value executes
   now, in the same tick.
4. **Retire.** Completed instructions advance their program counter: to
   the jump target, or pc+1, wrapping to 0 after the last instruction.
   `HLT` stops the cell.
5. **Events.** Events are emitted for committed changes only.
6. **Completion check.** See [Completion](#completion).

**Rule: one transfer per cell per tick.** A cell makes at most one
request per tick, so it can complete at most one port transfer per tick.
`MOV P0, P1` therefore takes at least two ticks: one to receive and one to
send.

## Instruction lifecycle

Each instruction goes through these stages:

```
gather port sources (one read per tick) → execute once → deliver result to port (one write) → retire
```

- Register and literal sources are read at execute time.
- **Blocked** instructions keep their state. A value already received from
  `P0` is never read again. A computed result waits in the
  pending-output slot until it is delivered.
- Local side effects happen at execute time. For example, `POP P1` pops
  immediately, and the value is held as pending output until the reader
  takes it. Snapshots show this as `stage: "write"` with
  `pending.output`.
- A blocked instruction does not advance the program counter.

Outcomes: **completed** (retired), **blocked** (request not matched),
**halted** (`HLT`), and **runtime error** (for example stack overflow or
underflow). A runtime error ends the test case.

## Communication

Communication is a synchronous rendezvous. A write and a read complete
together, in the same tick, and only when both sides request them in that
tick. There are no buffers in the connections. Either both sides commit or
neither does.

**Unavailable ports block; they are not errors.** Reading or writing a
port that is disabled, not connected, or connected to a cell that never
communicates blocks forever. The engine records why, for example:

- `cannot read from P3: port is disabled on this cell`
- `cannot write to P2: port is not connected`
- `waiting to write to P1: b.P0 is not ready to read`
- `cannot read from LAST: LAST is not set: no ANY transfer has completed on this cell yet`

A permanently blocked cell does not fail the test if other cells can still
produce the required output.

### ANY and LAST

- `ANY` can use any of the cell's four physical ports. Only connected
  ports can ever match.
- After a transfer through `ANY` (read or write), `LAST` is set to the
  physical port that was used.
- `LAST` means that port. Until an `ANY` transfer has happened, `LAST`
  has no port and the request blocks.
- Transfers on specific ports, or through `LAST`, do not change `LAST`.

### Arbitration

All candidate transfers are collected: connections where one end has a
write request and the other end has a read request that both include the
connected ports. Each candidate gets a rank on each side:

- **0** if that side named one port (`P0`–`P3`, or `LAST`)
- **the port index** if that side used `ANY` (P0 = 0 … P3 = 3)

Candidates are accepted greedily in order of **(reader rank, writer
rank)**. A candidate is skipped if its writer or reader is already
matched.

This means:

- A reader on `ANY` takes from the lowest-numbered port that has a ready
  writer.
- A writer on `ANY` delivers to the lowest-numbered port that has a ready
  reader.
- When a writer on `ANY` and a reader on `ANY` compete, the reader's
  preference is applied first.

When two candidates share a cell, that cell's rank differs between them.
So the result depends only on the requests, never on the order of cells in
the puzzle or of connections in the solution. The engine tests check this
by reversing the connection order.

TIS-100 was the conceptual reference here. These rules are this engine's
own documented behaviour; they do not claim to match TIS-100 exactly.

## Streams

**Input cells** offer the next value of their stream through `ANY` every
tick until the stream is exhausted. Each value is delivered exactly once.
**When the stream is exhausted, the cell stops offering values for
good.** It never produces an extra value, and any reader waits forever.

**Output cells** read through `ANY` every tick and record what they
receive. They do not judge the values themselves.

## Completion

Exact-output validation (`validate.Exact`) runs outside the cells, one
received value at a time. Each output stream is **pending**, **passed**,
or **failed**:

- A correct value advances progress.
- A wrong value fails the stream at once.
- Receiving the last expected value passes the stream.
- Any value received after the stream has passed fails it ("unexpected
  extra value"). This can happen while other outputs are still pending.
- An empty expected stream is passed from the start.

After the commit phase of each tick, the engine checks these in order:

1. Any output **failed** → `failed`. Failure wins over success in the
   same tick.
2. Any cell raised a **runtime error** → `runtime_error`.
3. **All outputs passed** → `passed`. The test ends right away. Programs
   do not need to halt, no more ticks run, and the engine does not wait
   for extra output.
4. **No progress** this tick → `deadlocked`.
5. **Repeated state**, if cycle detection is enabled → `cycle_detected`.
6. **Tick limit** reached → `tick_limit_exceeded`.

If every output stream of a test is empty, the test passes at tick 0.

## Deadlock detection

A tick makes **progress** if any transfer committed, any instruction
executed, or any instruction retired. A tick with no progress leaves the
machine state exactly as it was. Because the simulation is deterministic
and has no outside inputs, every later tick would be the same. So the
first tick without progress proves a permanent deadlock: every active
cell is blocked, halted, or idle. The result lists each blocked processor
with its current instruction, source line, and reason.

Deadlock is different from temporary blocking. A cell can stay blocked
for many ticks while others make progress, for example while it waits for
a slow neighbour. That is not a deadlock.

## Cycle detection (optional)

The machine has finite state when all its storage is bounded: fixed-width
integers, bounded stacks, finite streams. So a run that never finishes
must eventually repeat a complete state. With
`Options.CycleDetection` (`--cycles` on the CLI), the engine stores a
canonical encoding of the whole machine state after every tick. The
encoding covers program counters, pending instruction state, `LAST`, halt
flags, and every instance's `AppendState`: registers, stacks, input
cursors, and received outputs. The tick counter is not included.

States are map keys made of the **full byte encoding**, so a match means
the states are exactly equal. A hash collision can never produce a false
match. A repeated state proves the run would never finish, and it is
reported as `cycle_detected` with the first tick, the repeat tick, and
the period.

Cycle detection is off by default because it keeps one state per tick.
`MaxCycleStates` (default 1,000,000) and `MaxCycleBytes` (default 256 MiB)
limit the memory it uses. When a limit is reached, detection stops, the
result records a note, and the run continues under the tick limit.

## Tick limits

The default limit is `limits.max_ticks` in the puzzle, or 100,000 if that
is not set. `Options.MaxTicks` (`--max-ticks`) overrides it. When the
limit is reached, the status is `tick_limit_exceeded` and blocked cells
are listed for diagnosis.

## Test case isolation

Each test case runs on a new `Machine`. Every `cell.Instance` is built
again with `Architecture.New`. The program counter, pending state, `LAST`,
stream cursors, outputs, and validators all start from zero. Nothing is
reset by hand, so no state can be missed. The engine tests check that
running a test alone or as part of a suite gives identical results, and
that repeated runs give identical event streams.

## Metrics

Each `TestResult` reports these metrics. They are informational only;
there is no scoring in V1.

- `ticks`
- `instructions` (retired)
- `transfers`
- `programBytes` per cell, and `totalProgramBytes`
- `cellsSupplied`
- `cellsUsed`: cells that executed an instruction or took part in a transfer
- `connections`
