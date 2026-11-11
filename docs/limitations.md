# Known limitations (V1)

- **The web IDE keeps your work in the browser.** Saved solutions,
  imported puzzles and personal bests live in `localStorage` for that
  browser profile; clearing site data deletes them, and there is no sync.
  Export solutions to keep or move them.
- **Personal bests are not tamper-proof.** The engine computes every score,
  but it runs on the player's machine.
- **Native plugins do not work in the IDE.** WebAssembly cannot load Go
  plugins; cell types must be registered statically in
  `cmd/asmgraph-wasm`.
- **The engine download is about 1.6 MB gzipped** (5.7 MB uncompressed).
- **`runTests` blocks the engine worker** until it finishes; the page stays
  responsive, but Pause only applies to the interactive debugger run.
- **The optional Go server is single-user and local.** It binds to
  loopback and has no authentication. Exposing it beyond your machine
  (`--addr`, `--allow-host`) is only for trusted networks.
- **A peripheral completes at most one transfer per tick**, like every
  cell. A stack shared by several processors serves them one per tick.
  This keeps arbitration simple and deterministic; it is also a design
  constraint players work with.
- **Port functions are not validated against wiring by the backend.**
  Wiring two ports that both only send (or only receive) is allowed by
  the engine and simply never transfers. The web IDE refuses such wires,
  and the deadlock report explains them.
- **No accounts or leaderboards yet.** `scoring.Dominates`/`Frontier` are
  the starting point.
- **Scoring runs every test synchronously** when a solution passes. A
  solution that runs to a very high tick limit takes a while to score.
- **Node IDs are generated** (`cpu1`, `stack1`) when hardware is added in
  the web IDE; renaming nodes requires editing the YAML.
- **Puzzle editing in the browser** is not implemented. Puzzles are
  edited as YAML files. The backend already provides `validatePuzzle` and
  `exportPuzzle` for a future editor.
- **Completion status** ("solved") comes from the stored personal bests,
  or from the browser's record of passing runs.
- **Native plugins are fragile.** They need Linux, macOS, or FreeBSD with
  cgo, and the plugin must be built with the exact toolchain and
  dependency versions of the host. Prefer static registration. There are
  no scripting or out-of-process plugins.
- **Operand types are fixed.** Plugins can add instructions, and the
  assembler's resolver table is keyed by slot kind so new operand types
  can be added later. But V1 plugins cannot define their own operand
  syntax, and instructions are limited to three operands with at most one
  destination.
- **No `JRO` (relative jump).** Instructions have variable length, so a
  relative jump would need a defined meaning for instruction-count offsets.
  It was left out.
- **Puzzles cannot select validators in schema v1.** Only exact-sequence
  output validation can be used from puzzle files. Custom
  `engine.Validator`s can be passed through the Go API, and a later schema
  version can let puzzles name them.
- **Empty expected outputs pass at tick 0** when every output of a test
  is empty. This is the documented behaviour, but a puzzle that relies on
  it does not exercise the solution.
- **Extra output fails a stream** that has already passed, even while
  other outputs are still pending. This is strict on purpose.
- **Cycle detection costs memory.** It keeps one full state per tick until
  it reaches its configurable limits. Detection only proves a cycle if
  every plugin's `AppendState` is complete.
- **`delayMs` pacing uses wall-clock time.** It only decides when ticks
  run, never what they compute.
- **Ticks are not parallelised.** Each machine runs on one goroutine.
  Different test cases could run in parallel, since machines share only
  the immutable blueprint, but `RunAll` runs them one after another.
- **No scoring or leaderboards.** Metrics are informational only.
