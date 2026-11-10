# Known limitations (V1)

- **No graphical frontend.** There is only the CLI and JSON-RPC.
- **No HTTP/WebSocket transport yet.** `protocol.Service` is ready for
  one.
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
