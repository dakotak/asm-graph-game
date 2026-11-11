# JSON-RPC protocol (version "2")

`asmgraph serve --stdio` speaks **JSON-RPC 2.0** over **newline-delimited
JSON**. Each request, response, and notification is one line.

- **stdout** carries only protocol messages.
- **stderr** carries logs.

The commands are implemented by the transport-independent
`protocol.Service` through `transport/jsonrpc`. The same protocol is
available:

- over a WebSocket from `asmgraph web` (`/api/ws`, one message per text
  frame, one session per connection);
- in the browser or Node, from the WebAssembly build
  (`cmd/asmgraph-wasm`): `asmgraphEngine.handle(message)` returns the
  response string, and `asmgraphEngine.onEvent(fn)` receives event
  notifications. File paths are refused there; send YAML inline.

See [web.md](web.md).

## Framing

- Standard request IDs: string, number, or `null`. A request **without**
  an `id` is a notification: it is executed and never gets a response,
  even if it fails.
- Batches (JSON arrays) are supported. The response array contains only
  the requests that had an `id`.
- A success response has `result` and no `error`. An error response has
  `error` and no `result`.
- Requests are handled in order. `run` without `wait` returns at once, so
  that `pause` can be handled while a simulation runs.
- The server shuts down gracefully on the `shutdown` method or at EOF. It
  stops any background run first.

## Errors

| Code | Meaning |
|---|---|
| −32700 | Parse error (`id: null`) |
| −32600 | Invalid request: not an object, missing `"jsonrpc":"2.0"` or `method`, or a bad `id` |
| −32601 | Method not found |
| −32602 | Invalid params. Unknown fields are rejected. |
| −32603 | Internal error |
| −32001 | Validation failed. `data.diagnostics` has the details. |
| −32002 | Compilation failed. `data.diagnostics` has the details. |
| −32003 | Invalid state, for example `step` before `start` |
| −32004 | Not found (file or cell) |

### Diagnostics

```json
{"severity":"error","code":"asm.error","message":"program is 12 bytes but this cell has 8 bytes of program memory (this instruction crosses the limit)",
 "file":"solution.yaml","line":16,"path":"programs.alpha","cell":"alpha","sourceLine":4,"sourceColumn":1}
```

## Commands

| Method | Params | Result |
|---|---|---|
| `initialize` | — | `name`, `version`, `protocolVersion`, `bytecodeVersion`, `puzzleSchemaVersion`, `solutionSchemaVersion`, `cellApiVersion`, `nativePlugins`, `cellTypes`, `maxPorts`, `methods` |
| `listCellTypes` | — | `{types: [{type, description, programmable, instructions[], registers[], state[], config[], hardware}], operandKinds, ports, syntax}`. `state` lists the fields `CellSnapshot.state` reports (`{name, kind: "word"|"list", summary}`). `hardware` is the type's `cell.HardwareSpec`: `{category, ports: {min, max, default}, portFunctions?: {functions: [{name, direction, summary, min?, max?}], configurable, default, rule?}, programMemory?: {min, max, default}, cost: {base, perPort, properties?}, puzzleOnly?, overrides: {cost, defaults}}`. `operandKinds` documents each operand slot kind, `ports` describes `Pn`, ANY and LAST, and `syntax` gives comment characters and literal formats. |
| `assemble` | `{type, source, memoryLimit?, ports?}` | `{ok, bytes?, limit, instructions?, hex?, disassembly?, diagnostics}`. Compiles one program without touching the session, for live editor feedback. `bytes` is reported even when over the limit. With `ports`, naming a port the cell does not have is an error. |
| `validatePuzzle` | `{path}` or `{yaml}` | `{valid, diagnostics}`. Nothing is loaded. |
| `summarizePuzzle` | `{yaml, file?}` | `{summary, puzzle}`. A puzzle browser's description of one file (`library.Summary`: id, name, description, file, cellTypes, cells, programmableCells, tests, difficulty, valid, diagnostics, hardware). `puzzle` is null if the file does not parse. Stateless. |
| `evaluateSolution` | `{puzzle, solution, maxTicks?, cycleDetection?}` (both YAML) | `SuiteResult`, with `score` when every test passes. Validates and runs every test on fresh machines without touching the session. Invalid input gives a validation error with diagnostics. |
| `loadPuzzle` | `{path}` or `{yaml}` | `{puzzle, diagnostics, hardware, migrated}`. Clears any solution and simulation. `hardware` is the effective catalog players may build from (`puzzle.Effective`: `{type, spec, ports, programMemory?, functions?, functionMax?, config?, cost, min, max}`), or null for fixed-topology puzzles. `migrated` lists schema-v1 conversions. |
| `loadSolution` | `{path}` or `{yaml}` | `{solution, diagnostics, hardware}`. Validates against the loaded puzzle. `hardware` is the `HardwareReport` `{allowed, nodes: [{id, type, category, ports, programMemory?, portFunctions?, cost}], totalCost, maxCost?, nodeCount, maxNodes?, counts}`. On a validation error the report is also in the error's `data.hardware`, next to `data.diagnostics`. |
| `exportPuzzle` / `exportSolution` | — | `{yaml}` of the loaded document |
| `compile` | — | `{programs: [ProgramReport], totalBytes, diagnostics}` |
| `start` | `{test?: name\|index, maxTicks?, cycleDetection?}` | `Snapshot` of a fresh machine. Compiles first if needed. |
| `step` | `{ticks?: n (default 1), snapshot?: bool}` | `{status, tick, snapshot?}` |
| `run` | `{wait?: bool, maxTicks?: n, delayMs?: 0..60000}` | With `wait`: `{status, tick, result}`. Without: `{running: true}`; progress arrives as events. |
| `pause` | — | `{status, tick}` |
| `reset` | — | `Snapshot` of a fresh machine for the same test |
| `getState` | — | `Snapshot` |
| `getCell` | `{cell}` | `CellSnapshot` |
| `getResult` | — | `TestResult` (partial while running) |
| `runTests` | `{maxTicks?, cycleDetection?}` | `SuiteResult`. Uses separate fresh machines and does not disturb the interactive one. |
| `runTest` | `{test: name\|index, maxTicks?, cycleDetection?}` | `TestResult` from a fresh machine. The interactive machine is untouched. |
| `setBreakpoints` | `{breakpoints: {cell: [sourceLine…]}}` | `{breakpoints}`. `run` and multi-tick `step` pause after any tick that moves a cell onto a breakpoint line, before it executes. A `simulation.paused` event with `{reason: "breakpoint", cell, line}` is sent. Breakpoints never change results. |
| `setEventLevel` | `{level: none\|lifecycle\|ticks\|all}` | `{level}` |
| `shutdown` | — | `{}` |

`path` loading can be turned off with `serve --stdio --no-files`.

`delayMs` only paces a background run for animation. It never changes
results.

## Events

Events are sent as notifications:

```json
{"jsonrpc":"2.0","method":"event","params":{"seq":12,"tick":3,"type":"port.transfer","test":"positive","cell":"processor",
  "data":{"from":{"cell":"processor","port":"P1"},"to":{"cell":"output","port":"P0"},"value":2}}}
```

- Events describe **committed** state only. Nothing speculative is ever
  sent.
- `seq` increases by exactly 1 for each event forwarded on the
  connection, after level filtering. A gap means a message was lost.
- `tick` is the tick whose commit produced the event. Events within a
  tick come in phase order: transfers, then instruction completions, then
  block changes, then cell state, then output validation, then
  `tick.completed`, then terminal events.

| Type | Level | Data |
|---|---|---|
| `simulation.started` | lifecycle | `Snapshot` |
| `simulation.paused` | lifecycle | `{status, reason, cell?, line?}`. `reason` is `pause requested`, `tick budget reached`, or `breakpoint`. |
| `simulation.reset` | lifecycle | — |
| `simulation.completed` | lifecycle | `TestResult` |
| `test.completed` | lifecycle | `TestResult` (from `runTests`) |
| `deadlock.detected` | lifecycle | `{blocked: [BlockedCell]}` |
| `cycle.detected` | lifecycle | `{firstTick, repeatTick, period}` |
| `runtime.error` | lifecycle | `{cell, pc, instruction, sourceLine, message}` |
| `tick.completed` | ticks | `{status, instructions, transfers, progress}` |
| `port.transfer` | ticks | `{from, to, value}` (writer → reader) |
| `output.validation` | ticks | `OutputSnapshot`, sent every time an output cell receives a value (and so whenever its state changes) |
| `cell.blocked` | ticks | `{reason, instruction, sourceLine}`, sent on the change to blocked |
| `cell.unblocked` | ticks | — |
| `instruction.completed` | all | `{pc, address, instruction, sourceLine, nextPc, halted?}` |
| `cell.state` | all | `CellSnapshot`, only for cells whose canonical state changed |

Ticks never send the full machine state. Frontends apply `cell.state`
deltas and fetch a full `getState` snapshot when they need one.

## Snapshots

`Snapshot`:

```
puzzle, test, testIndex, tick, status, message,
cells: [CellSnapshot], connections: [{from, to}],
inputs: [{cell, stream, position, length}],
outputs: [OutputSnapshot], metrics
```

`CellSnapshot`:

| Field | Meaning |
|---|---|
| `id`, `type`, `category`, `programmable`, `player`, `programBytes`, `programMemory` | Identity (`player` is true for player-built hardware) and memory use |
| `pc`, `address`, `instruction`, `sourceLine` | Current instruction: index, byte offset, disassembly, and source line to highlight |
| `stage` | `ready`, `write` (result held for delivery), `idle`, or `halted` |
| `pending` | `{received: [{operand, value}], output?, destination?}` for partly executed instructions |
| `request` | The communication tried in the last tick, for example `"read P0"` |
| `blocked`, `blockReason` | Whether the cell is blocked and why |
| `halted`, `last`, `instructionsCompleted`, `error` | |
| `ports` | `[{port, enabled, peer?: {cell, port}, function?}]`, one per configured port; `function` is a peripheral port's function (`PUSH`, …) |
| `state` | The plugin's `Inspect()` view. Standard: `{ACC, BAK}`. Stack processor: `{ACC, stack, capacity}`. Passive stack: `{stack, capacity}`. Input: `{stream, position, length, exhausted, next?}`. Output: `{stream, received}`. |

`OutputSnapshot`: `{cell, stream, state: pending|passed|failed, expected, received, mismatch?: {index, expected?, got}}`.

## Results

`TestResult`:

```
test, index, status, passed, message, metrics, outputs,
blocked?: [{cell, instruction, sourceLine, reason}],
cycle?, error?, notes?
```

`status` is one of:

- `passed`
- `failed`
- `deadlocked`
- `cycle_detected`
- `tick_limit_exceeded`
- `runtime_error`

While a test is still running, the status is `ready` or `running`.

`SuiteResult`: `{puzzle, passed, tests: [TestResult], summary, score?}`.
The `summary` has `passedTests`, `totalTests`, `totalTicks`, `maxTicks`,
`totalInstructions`, `totalTransfers`, `programBytes`,
`totalProgramBytes`, `cellsSupplied`, `cellsUsed`, `connections`, and
`hardwareCost`. `score` is `{cost, ticks, bytes}` and is present only when
every test passed: hardware cost, worst-case ticks, and total bytecode
bytes, three independent dimensions (see
[hardware.md](hardware.md#scoring)). `metrics` also carry `hardwareCost`.

Version "2" of the protocol added the hardware fields above, per-cell
port counts in snapshots, and `score`. Clients should check
`protocolVersion` from `initialize`.

## Example session

```
→ {"jsonrpc":"2.0","id":1,"method":"loadPuzzle","params":{"path":"examples/puzzles/double-input.yaml"}}
← {"jsonrpc":"2.0","id":1,"result":{"puzzle":{…},"diagnostics":[]}}
→ {"jsonrpc":"2.0","id":2,"method":"loadSolution","params":{"path":"examples/solutions/double-input.yaml"}}
→ {"jsonrpc":"2.0","id":3,"method":"start","params":{"test":"boundaries"}}
← {"jsonrpc":"2.0","method":"event","params":{"seq":1,"tick":0,"type":"simulation.started",…}}
← {"jsonrpc":"2.0","id":3,"result":{"tick":0,"status":"ready","cells":[…]}}
→ {"jsonrpc":"2.0","id":4,"method":"step"}
← {"jsonrpc":"2.0","method":"event","params":{"seq":2,"tick":1,"type":"port.transfer",…}}
← …
← {"jsonrpc":"2.0","id":4,"result":{"status":"running","tick":1}}
→ {"jsonrpc":"2.0","id":5,"method":"shutdown"}
← {"jsonrpc":"2.0","id":5,"result":{}}
```

## Go API

The same operations are available in-process through `engine.Session`:

- `LoadPuzzle`, `LoadSolution`, `Compile`
- `Start`, `Step`, `Run`, `RunAsync`, `Pause`, `Reset`
- `State`, `Cell`, `Result`, `RunTests`

There are also lower-level building blocks:

- `engine.Prepare`
- `engine.NewMachine` together with `Machine.Step`, `Run`, `Snapshot`, and `Result`
- `engine.RunTest` and `engine.RunAll`
