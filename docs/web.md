# Web IDE

`asmgraph web` serves a browser IDE for the puzzle game. It has an
interactive graph editor, a Monaco assembly editor, and a debugger. All of
these drive the real Go engine.

The browser never simulates anything. Instruction execution, bytecode
compilation, port communication, blocking, program counters, registers,
test validation, timing, and deadlock and cycle detection all come from the
backend. The frontend handles presentation, interaction, and local editing
state only.

## Quick start

Production build:

```bash
cd web && npm ci && npm run build && cd ..
```

```bash
go run ./cmd/asmgraph web
```

Then open <http://127.0.0.1:7357>.

Development, with hot reload. Run each command in its own terminal:

```bash
go run ./cmd/asmgraph web --static ''
```

```bash
cd web && npm run dev
```

Then open <http://127.0.0.1:5173>. Vite proxies `/api` (including the
WebSocket) to the Go server. Set `ASMGRAPH_BACKEND` to point the proxy at
a different backend.

`asmgraph web` flags:

| Flag | Default | Meaning |
|---|---|---|
| `--addr` | `127.0.0.1:7357` | Listen address. Keep it on loopback unless you know why. |
| `--puzzles` | `examples/puzzles` | Directory of puzzle YAML files |
| `--solutions` | `solutions` | Where solutions are saved: `<dir>/<puzzle-id>/<name>.yaml` |
| `--static` | `web/dist` | Built frontend. Pass `''` to serve only the API. |
| `--allow-host` | | Extra Host/Origin name to accept, for example a LAN hostname. Repeatable. |
| `--plugin` | | Native cell plugin to load. Repeatable. |

## Architecture

```
┌────────────── browser ───────────────┐          ┌──────────── asmgraph web ─────────────┐
│ React components (graph, editor, …)   │          │ transport/web                         │
│        ▲ selectors      │ actions     │  HTTP    │  /api/puzzles…  puzzle + solution     │
│ Zustand stores ◄──── controller ──────┼─────────►│                 library (files)       │
│  workspace (editing)    │ JSON-RPC    │  WS      │  /api/ws ─► transport/jsonrpc ─►      │
│  sim (execution)        └─────────────┼─────────►│        protocol.Service ─► engine     │
│  prefs, ui              ◄── events ───┼──────────│        (one session per connection)   │
└───────────────────────────────────────┘          └───────────────────────────────────────┘
```

The WebSocket carries **exactly the same JSON-RPC 2.0 protocol** as
`asmgraph serve --stdio`. Both transports share `transport/jsonrpc`. There
is no second, browser-only protocol; see [protocol.md](protocol.md).

### Backend additions for the browser

These were added inside the existing architecture, and all are covered by
Go tests:

| Addition | Where | Why |
|---|---|---|
| `transport/web` | new package | HTTP library API, WebSocket JSON-RPC sessions, static files, host and origin checks, backpressure |
| `transport/jsonrpc` | extracted from stdio | Shared JSON-RPC framing for every transport |
| `assemble` method | protocol | Compiles one program without touching the session, for the exact live byte count and errors while typing |
| `runTest` method | protocol, `Session.RunTest` | Runs a single test on a fresh machine without disturbing the debugger |
| `setBreakpoints` method | protocol, `Session.SetBreakpoints`, `Machine.BreakpointHit` | Real breakpoints. They decide only *when* a run pauses, never what it computes. |
| `listCellTypes` extras | protocol | `operandKinds`, `ports`, `syntax`, `state`, and `hardware`, so the editor needs no hard-coded rules |
| Hardware catalog and report | `loadPuzzle`, `loadSolution` | Effective limits for the palette; per-node costs, budget, and violations for the design being edited |
| Scores | `POST /api/puzzles/{id}/scores`, `scoring` package | The server re-runs a passing solution and keeps per-dimension personal bests |

## HTTP API

Every response is JSON. Errors are `{"error": string, "diagnostics"?: [...]}`
with an appropriate status code.

| Method and path | Result |
|---|---|
| `GET /api/health` | `{ok, name, version, protocolVersion}` |
| `GET /api/puzzles` | `{puzzles: [PuzzleSummary]}`: id, name, description, file, cellTypes, cells, programmableCells, tests, difficulty (`metadata.difficulty`), valid, diagnostics, solutions, hardware (players build hardware), bests |
| `GET /api/puzzles/{id}` | `{summary, yaml, puzzle}`. The single YAML file, with every test case. |
| `GET /api/puzzles/{id}/solutions` | `{solutions: [{name, modified, size}]}` |
| `GET /api/puzzles/{id}/solutions/{name}` | `{name, yaml}` |
| `PUT /api/puzzles/{id}/solutions/{name}` | Body `{yaml}`, at most 1 MiB. The YAML must parse as a solution for that puzzle. Work-in-progress solutions that don't compile are allowed. Writes are atomic (temp file and rename). |
| `GET /api/puzzles/{id}/scores` | Personal bests: `{puzzle, bests: {cost?, ticks?, bytes?: {score, file, achieved}}}` |
| `POST /api/puzzles/{id}/scores` | Body `{yaml}`. The server validates the solution, runs **every** test itself, and, if all pass, records the score: `{passed, summary, score?, improved: [dimension], record}`. Invalid solutions get 422 with diagnostics. The client never sends a score. |
| `GET /api/puzzles/{id}/scores/{cost\|ticks\|bytes}/solution` | `{name, yaml}` of the solution behind that personal best |
| `POST /api/rpc` | Stateless JSON-RPC: `initialize`, `listCellTypes`, `validatePuzzle`, `assemble` |
| `GET /api/ws` | WebSocket. Full JSON-RPC session with event notifications. |

**Backpressure.** Events go through a bounded queue for each connection.
If a client falls behind, events are **dropped rather than slowing the
simulation**. The service numbers forwarded events without gaps, so the
client sees the missing `seq` and calls `getState` to resynchronize.
Responses are never dropped.

**Security:**

- The server binds to loopback by default.
- Requests whose `Host` is not localhost, 127.0.0.1, ::1, or an
  `--allow-host` name are rejected, which defends against DNS rebinding.
- WebSocket origins are checked the same way.
- Paths are never taken from clients. Puzzle IDs and solution names must
  match `^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$` without `..`, and symlinks are
  refused.
- The WebSocket service sets `AllowFiles = false`, so `loadPuzzle {path}`
  is refused and clients send YAML inline.
- Responses carry a strict Content-Security-Policy (`script-src 'self'`,
  `frame-ancestors 'none'`), `nosniff`, and `no-referrer`.
- No subprocesses are started.

## Frontend

### Technology

| Choice | Reason |
|---|---|
| React 19, TypeScript, Vite | As specified. Fast dev loop, small production build. |
| `@xyflow/react` (React Flow 12) | Graph editing: custom nodes and edges, handles, zoom, pan, minimap |
| `monaco-editor`, used directly | A curated entry point, `src/editor/monacoEntry.ts`: the core API and editor contributions without the 80+ bundled languages, the TS, CSS, HTML, and JSON workers, the LSP client, or a CDN loader. Lazy loaded. |
| Zustand | Small stores with selector subscriptions, so each component re-renders only for the slice it reads |
| Tailwind CSS 4 | Styling with semantic CSS-variable tokens for dark and light themes |
| `react-resizable-panels` 4 | Resizable, collapsible panels with layouts persisted to localStorage |
| `@dagrejs/dagre` | Automatic layout. Handles cycles. |
| `yaml` | Solution import and export |
| shadcn/ui | Not installed. Its approach (small, app-owned primitives styled with Tailwind) is followed in `src/components/ui.tsx`, so no extra dependency or CLI is needed. |

### Source layout

```
web/src/
  api/types.ts          mirrors of the Go JSON types (engine, protocol, web)
  api/rpc.ts            WebSocket JSON-RPC client (timeouts, reconnect, error mapping)
  api/http.ts           library API client
  services/controller.ts  the only code that talks to the backend
  state/workspace.ts    editing state: puzzle, solution, selection, assembler results, diagnostics, breakpoints
  state/sim.ts          execution state: connection, machine view, edge activity, event log, results
  state/prefs.ts        persisted preferences, drafts, solved puzzles
  state/ui.ts           dialogs, panel tabs, focus and reveal requests
  lib/                  pure logic: solution YAML, connection checks, hardware resolution (cells, defaults, cost mirror,
                        port placement), event reducer, log ring buffer, layout, statuses
  editor/               Monaco entry, themes, catalog-driven language support, pure editor helpers
  components/           toolbar, sidebar, hardware palette and node editor, graph, editor pane, inspector,
                        bottom panel (tests with scores), dialogs
  commands.ts           single registry for toolbar, shortcuts, command palette and help
```

### State management

- **`workspace`**: what the player is editing. Every change that affects
  execution (programs, connections) increments `version` and sets `dirty`.
  Layout changes set `dirty` but not `version`.
- **`sim`**: what the backend reports. `view` is the snapshot, kept in
  maps by cell. Committed events update it.
- Editing state and execution state are separate stores, so a running
  simulation never re-renders the editor. Graph nodes and edges subscribe
  to their own cell or connection through selectors.
- **Sync rule.** The backend session holds a solution version
  (`sim.loadedVersion`). Compile, run, step, and test calls first send
  `loadSolution` if the edited version differs. **Any execution-relevant
  edit while a machine exists resets the simulation.** Edited programs or
  topology never keep running against stale bytecode.
- **Command serialization.** Execution commands (start, step, run, pause,
  reset, tests) run one at a time, so rapid key presses never interleave.

### Execution visualization and performance

Events arrive as JSON-RPC notifications. The controller:

1. Queues events and applies them in **one batch per animation frame**,
   using a timer in background tabs (`lib/simReducer.ts`). The reducer
   only copies committed data. For example, `cell.state` carries the full
   `CellSnapshot`.
2. Detects `seq` gaps, and an overloaded queue (more than 5,000 events),
   and replaces the view with `getState`. Stale snapshot responses are
   discarded: older ticks, a non-terminal state arriving after completion,
   or a machine that has since been replaced.
3. Records the latest transfer for each connection. The edge flashes and
   shows the value with a direction arrow for about two ticks.
4. Appends a readable entry to a **ring buffer of 1,000 entries**. The
   Events panel filters it by cell and category and renders the last 400.

Speed settings only pace the backend run (`run {delayMs}`):

| Speed | Event level | Visual updates |
|---|---|---|
| Step | `all` | events, then a `getState` after each step |
| 1, 5, 20 ticks/s | `all` | every committed event |
| Maximum | `lifecycle` | `getState` sampled every 150 ms; no per-transfer animation |

The engine never waits for the browser. The deterministic result is the
same at every speed.

### Graph editor

- Nodes are the puzzle's fixed cells plus the player's hardware. Fixed
  cells cannot be deleted; player nodes are removed with Delete (or the
  Remove button), together with their wires and program. Input and output
  cells have their own look (accent bar and `INPUT ▶` / `◀ OUTPUT` tag);
  passive peripherals have a dashed outline and show their stored data;
  player nodes show their cost (`¤22`) and any backend violations.
- Ports are React Flow handles in loose connection mode. Only the ports
  a cell actually has (and that are enabled) are drawn: P0 left, P1
  right, P2 top, P3 bottom, then around again, spread along each side.
  Labels sit outside the node and include a peripheral port's function
  (`P1 POP`). Each port shows one of four states:

| Port state | Drawn as |
|---|---|
| Free | hollow |
| Connected | filled |
| The cell is waiting on it (from the backend's `request` and `blocked`) | amber |
| Value transferred this tick | accent |

- Invalid connections are prevented by `isValidConnection` (`lib/connections.ts`):
  - ports the cell does not have, or that are disabled
  - ports already in use
  - self-connections
  - virtual ports
  - two ports that can never exchange data (for example an input stream
    into a stack's `POP` port: both only send)

  The backend validates the topology again on every load.

### Hardware design

For puzzles with a `hardware` section (see [hardware.md](hardware.md)):

- The sidebar's **Hardware** palette lists the types the puzzle allows,
  with their default cost and how many are placed, plus budget meters for
  cost and node count. Required types are marked. **+ Add** places a node
  with its effective defaults below the current design.
- Selecting a node opens its **configuration**: port count, program memory
  (processors), a function per port (peripherals), and configuration
  fields, each limited to the effective range, with the cost breakdown.
  Processors show it in the inspector next to their code; passive
  peripherals have no code, so the editor pane shows the configuration
  instead.
- Lowering a port count removes the wires that no longer fit, and says
  which.
- About 300 ms after each hardware edit the design is sent to the backend
  (`loadSolution`). Its report supplies the costs, and its diagnostics
  supply violations, shown on the palette and on the offending node.
- After **Run all tests** passes, the solution is posted to
  `/api/puzzles/{id}/scores`. The Tests panel shows the three score
  dimensions, the personal best of each, "new best" markers, and a
  **load** link to open the solution behind a best.
- Select a wire and press Delete or Backspace to remove it, or use the
  Disconnect button. Other controls: Auto layout (dagre), Fit, Snap to
  grid, and Animate.
- Positions are saved as solution `metadata.layout`, which is
  presentation only. Solutions without positions are laid out
  automatically, with input cells on the left and output cells on the
  right, decided from the test-case streams.

### Monaco integration

- **One editor instance** with **one text model per cell**. Switching
  cells keeps each cell's undo history, cursor, and scroll position.
  External changes (load, import) update models without echoing them
  back.
- **One language per cell type** (`asm-<type>`), generated from
  `listCellTypes`. That provides tokenizing, completion of the type's
  mnemonics, register and port operands, label completion for targets,
  and hover docs. Plugin types work automatically and unsupported
  instructions are never suggested.
- **Live diagnostics.** About 250 ms after typing stops, the editor calls
  `assemble` and shows the real errors as markers. The **Program Memory:
  N / M bytes** meter shows the exact encoded size. It shows `?` while the
  program does not assemble, and is never an estimate.
- **Decorations:** a ▶ marker and highlight on the current instruction
  (amber ⏸ and a hover reason when blocked), and breakpoint dots in the
  glyph margin. Toggle a breakpoint by clicking the gutter or pressing F9.
- Monaco uses the textarea input path (`editContext: false`). It works
  more reliably with IMEs, assistive technology, and browser automation.

### Solution persistence

| Action | How |
|---|---|
| Save | Mod+S. Asks for a name the first time. Written to the server. |
| Save a copy | Mod+Shift+S |
| Open a saved solution | Click the solution name in the toolbar |
| New empty solution | Command palette |
| Export YAML | Downloads the file |
| Import YAML | Must be for the open puzzle; validated by shape, then by the backend |

- The dirty state is shown as ● in the toolbar.
- Discarding unsaved work asks for confirmation, and the browser warns on
  unload.
- Unsaved edits are also kept as a **local draft** in localStorage and
  restored when you reopen the puzzle. Drafts are a safety net; the files
  on the server and exports are the real storage.
- When a puzzle opens, the app restores a draft if one exists, otherwise
  loads the most recently saved solution, otherwise starts an empty one.

### Keyboard shortcuts

| Action | Keys |
|---|---|
| Compile | Mod+Shift+B |
| Run / continue | F5 |
| Pause | F6 |
| Step one tick | F10 |
| Reset | Shift+F5 |
| Run all tests | Mod+Shift+Enter |
| Save / Save a copy | Mod+S / Mod+Shift+S |
| Browse puzzles | Mod+O |
| Command palette | Mod+Shift+P |
| Focus graph / editor | Alt+1 / Alt+2 |
| Next / previous cell | Alt+] / Alt+[ |
| Toggle breakpoint | F9 (in the editor) |
| Help | ? |

Mod is ⌘ on macOS and Ctrl elsewhere. Shortcuts avoid keys that Monaco
uses for editing. The help dialog (?) lists them all, generated from the
command registry.

### Error handling

- **Backend unavailable or disconnected.** A banner appears with a
  Reconnect button, and the client retries with exponential backoff. A
  reconnect creates a new backend session: the app reloads the puzzle,
  marks the solution as not loaded, re-applies breakpoints, and resets the
  simulation. Edits are never lost.
- **Protocol mismatch.** The `protocolVersion` from `initialize` is
  checked, and a mismatch is reported.
- **Invalid puzzle, invalid solution, or compile failure.** Diagnostics
  appear in the Diagnostics tab and as editor markers. Clicking one jumps
  to the source. Edits are preserved.
- **Other failures** (I/O, a rejected save, import errors) are shown as
  notifications with the server's message.

## Testing

```bash
cd web
```

```bash
npm test
```

```bash
npm run e2e
```

```bash
npm run typecheck
```

- **Unit tests** (Vitest, React Testing Library, jsdom; 53 tests):
  - solution YAML round trips (including player hardware) and rejections
  - connection rules, including port counts and data directions
  - hardware resolution: defaults, the cost mirror, port placement
  - the event reducer, including gap detection
  - the ring buffer, layout, and editor context parsing
  - the RPC client: correlation, errors, disconnect and reconnect
  - the controller, run against an in-memory fake backend: puzzle
    loading, compile diagnostics that keep the source, step sequencing
    and serialization, batched events and resync, invalidation on edit,
    maximum-speed sampling, saving, recovery after disconnection,
    import/export, and designing hardware (add, configure, remove,
    backend violations, score submission)
  - component rendering: the test panel, diagnostics, toolbar control
    states, palette, event filtering, and port and cell presentation
- **End-to-end** (Playwright, against the **real Go server** serving the
  production build, with a throwaway solutions directory):
  - build a solution by typing in Monaco and dragging wires, then check
    the exact byte count, run all tests (3/3), and save to disk
  - compile errors as editor markers and diagnostics
  - step, run to completion, and reset (tick values match the CLI)
  - invalid connections are rejected and deleting a wire works
  - export and re-import
  - design hardware for `stack-reverse`: add a processor and a passive
    stack, configure the stack, a refused POP-to-input wire, wire it, type
    the program, pass 3/3, and get scored by the server
- **Go:** `go test ./transport/web` covers:
  - the library API and path-traversal rejection
  - the host check
  - stateless RPC
  - WebSocket sessions
  - breakpoints
  - backpressure: dropped events with a visible `seq` gap
  - scores and personal bests, and hardware over WebSocket

## Supporting new cell types

New architectures appear in the IDE **without frontend changes**. Register
the type in Go (see [plugins.md](plugins.md)), and the IDE picks up:

- **Editor:** mnemonics, opcodes, operand slots, summaries, and registers
  from `listCellTypes`. These drive syntax highlighting, completion, and
  hover. Port completion offers the cell's own ports (with what each is
  connected to).
- **Hardware:** the type's `HardwareSpec` drives the palette, the node
  configuration editor (port range, memory range, port functions, config
  fields), and the cost display.
- **Debug state:** the plugin's `Inspect()` map is shown generically.
  Number values render as registers. Arrays render as lists with the top
  element marked, which is how a stack shows up.
- **Stream cells:** any cell whose `config.stream` names a test-case
  stream is drawn as an input or output, with progress and validation
  taken from the engine's snapshot.
- **Other non-programmable plugin cells:** their state is shown as
  key/value pairs.

To give a type a custom look, add a branch in
`components/graph/CellNode.tsx` keyed on the type, using the same snapshot
data.
