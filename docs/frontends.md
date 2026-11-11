# Frontends and debugging

The repository includes a browser IDE that runs the engine as WebAssembly
(see [web.md](web.md)). The engine was designed so that other frontends (TUIs,
desktop apps, IDE extensions, automation) can be built on it without
copying game rules.

## Integration options

| Approach | Use when |
|---|---|
| Spawn `asmgraph serve --stdio` and speak JSON-RPC | Any language. Editors, TUIs, desktop apps. |
| Import `asmgraph/engine` and use `engine.Session` | Go frontends and tools |
| Run `asmgraph test --json` | CI, graders, batch evaluation |
| Load `asmgraph.wasm` (`cmd/asmgraph-wasm`) and call `asmgraphEngine.handle(json)` | Browser or Node frontends with no server. This is what the bundled web IDE uses (in a Web Worker). |
| Connect to `asmgraph web` (`/api/ws` plus the HTTP library API) | Clients that want a shared server with file storage |

## Building a player UI

| Feature | Source |
|---|---|
| Show the fixed cells | `loadPuzzle` → `puzzle.cells` (id, type, port_count, ports, program_memory) |
| Offer hardware to build | `loadPuzzle` → `hardware` (effective catalog), `listCellTypes` → `hardware` (plugin specs) |
| Show costs and violations | `loadSolution` → `hardware` report, and diagnostics with paths like `hardware[1].port_count` (also on errors) |
| Show scores | `runTests` → `SuiteResult.score`; the web server's `/api/puzzles/{id}/scores` keeps bests |
| Explain cell types and instructions | `listCellTypes` |
| Connect ports | Edit `connections` in your model, then `loadSolution` with inline `yaml`. Diagnostics give paths such as `connections[2].to.port`. |
| Edit and compile assembly | `compile` → per-cell `bytes` / `limit` (memory bars), `disassembly`. Errors carry `cell`, `sourceLine`, and `sourceColumn`. |
| Run, step, pause, reset | `start`, `step`, `run` (async with `delayMs` for animation), `pause`, `reset` |
| Highlight the current or blocked line | `CellSnapshot.sourceLine`, `blocked`, `blockReason`; `cell.blocked` events |
| Inspect registers and stacks | `CellSnapshot.state` |
| Animate transfers | `port.transfer` events (`from`, `to`, `value`) |
| Show input and output progress | `Snapshot.inputs`, `Snapshot.outputs`; `output.validation` events |
| Show test results | `runTests` → `SuiteResult` |
| Save solutions | Keep your own YAML, or use `exportSolution` after loading. Put positions in `metadata`. |

Visual positions belong only in the solution's (or puzzle's) `metadata`.
The engine never reads them.

Recommended sync strategy:

1. Call `getState` once after `start` or `reset`.
2. Apply `cell.state`, `port.transfer`, and `output.validation` events
   using `seq`.
3. If `seq` ever skips a number, call `getState` again.

For fast runs, use `setEventLevel` with `lifecycle` or `ticks`.

## Puzzle authoring tools

A web editor can create a complete puzzle file, including every test
case:

- add or remove cells
- choose types (`listCellTypes` gives config schemas)
- set program memory and ports
- edit input and expected streams
- add, edit, or remove tests

The editor should:

1. Send the document to `validatePuzzle` (inline `yaml`) after each change
   and show the path-annotated diagnostics.
2. To test a reference solution, call `loadPuzzle`, `loadSolution`, then
   `runTests`.
3. To export, write the YAML (`exportPuzzle` normalises it). To import,
   read the YAML file.

Validation is always done by the backend, so frontends never re-implement
puzzle rules.

## Debugging workflow (CLI)

1. `asmgraph validate puzzle.yaml solution.yaml` checks topology and
   assembly errors, with file lines.
2. `asmgraph compile … --disasm` shows memory use per cell and per
   instruction.
3. `asmgraph test …` shows which test fails, and how:
   - **failed**: the first mismatching index, the expected value, and the
     value received
   - **deadlocked**: every blocked processor with its instruction, line,
     and reason (for example `cannot write to P2: port is not connected`)
   - **tick_limit_exceeded**: rerun with `--cycles` to prove an infinite
     loop
   - **runtime_error**: cell, instruction, and line
4. `asmgraph run … --test NAME --trace` gives a tick-by-tick log of
   transfers, retired instructions, and blocking.
5. `asmgraph run … --snapshot --json` dumps the full final state.

Typical bugs and what you see:

| Symptom | Likely cause |
|---|---|
| Two cells blocked on writes to each other | Both send first. Make one of them receive first. See `examples/solutions/combine-streams.yaml`. |
| `LAST is not set` | `LAST` was used before any `ANY` transfer |
| `port is disabled on this cell` | The puzzle does not enable that port; pick another |
| `unexpected extra value` | A finished output stream received more data |
