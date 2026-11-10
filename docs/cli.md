# CLI reference

```
asmgraph <command> [flags] [args]
```

You can put flags before or after the positional arguments. Every command
accepts:

| Flag | Meaning |
|---|---|
| `--json` | Print machine-readable JSON on stdout |
| `--plugin PATH` | Load a native cell plugin (`.so`). Repeatable. |

The `ASMGRAPH_PLUGINS` environment variable can also list plugin paths,
separated by `:` (`;` on Windows).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. All tests passed. |
| 1 | Invalid puzzle or solution, compile errors, or a failing test (failed, deadlocked, cycle, tick limit, runtime error) |
| 2 | Usage error, unreadable file, or plugin load failure |

## `validate PUZZLE [SOLUTION]`

Validates the puzzle. If you also pass a solution, it validates the
solution and compiles it too.

```
$ asmgraph validate examples/puzzles/double-input.yaml examples/solutions/invalid/double-input-broken.yaml
examples/solutions/invalid/double-input-broken.yaml:8:31: connections[1].from.port: port P2 is disabled on cell "input" by the puzzle
examples/solutions/invalid/double-input-broken.yaml:10:11: connections[2].from: port processor.P0 is already used by connections[0]
examples/solutions/invalid/double-input-broken.yaml:12:35: connections[3].from.port: invalid port "P5" (expected P0, P1, P2, or P3)
examples/solutions/invalid/double-input-broken.yaml:18: programs.processor: cell processor, line 2:1: unknown instruction MUL for cell type standard (supported: NOP MOV ADD SUB NEG SAV SWP JMP JEZ JNZ JGZ JLZ HLT)
examples/solutions/invalid/double-input-broken.yaml:19: programs.processor: cell processor, line 3:10: MOV destination operand: "P9" is not a valid destination: expected a register (NIL, ACC) or a port (P0-P3, ANY, LAST)
examples/solutions/invalid/double-input-broken.yaml:20: programs.processor: cell processor, line 4:5: JMP target operand: undefined label "LOOP"
solution validation/compilation failed: 6 error(s)
```

Assembly errors in block-scalar programs (`|`) are mapped to the line in
the YAML file. The line and column inside the program are also shown.

JSON output: `{"valid": bool, "stage"?: string, "diagnostics": [...]}`.
The fields of a diagnostic are described in [protocol.md](protocol.md#diagnostics).

## `compile PUZZLE SOLUTION [--disasm]`

Compiles every programmable cell and reports bytes used against the
cell's budget, the instruction count, and the bytecode as hex.

```
$ asmgraph compile examples/puzzles/reverse-sequences.yaml examples/solutions/reverse-sequences.yaml --disasm
helper           standard      0 / 12   bytes    0 instructions
stack            stack        18 / 24   bytes    8 instructions  011001080940010700450f41110709010111
      0  @0    011001         MOV P0, ACC          ; line 1
      1  @3    0809           JEZ 4                ; line 2
      ...
total: 18 program bytes
```

JSON: `{"ok": true, "programs": [ProgramReport], "totalBytes": n}`. A
`ProgramReport` has `cell`, `type`, `bytes`, `limit`, `instructions`,
`hex`, and `disassembly[]` (`index`, `address`, `bytes`, `text`,
`sourceLine`).

## `run PUZZLE SOLUTION`

Runs one test case.

| Flag | Meaning |
|---|---|
| `--test NAME\|INDEX` | Test case to run. Defaults to the first. |
| `--trace` | Print committed events tick by tick: transfers, completed instructions, blocking, output validation |
| `--max-ticks N` | Override the puzzle's tick limit |
| `--cycles` | Enable exact repeated-state detection |
| `--snapshot` | Print the final machine snapshot |

```
$ asmgraph run examples/puzzles/double-input.yaml examples/solutions/invalid/double-input-deadlock.yaml
  FAIL  positive             deadlocked           ticks=4      instructions=2      transfers=1
        no cell can make progress: every active cell is blocked, halted, or idle
        blocked: processor at "MOV ACC, P2" (line 3): cannot write to P2: port is not connected
        output result     pending  received [] of expected [2 4 6 8]
```

JSON: `{"ok": bool, "result": TestResult, "events"?: [Event], "snapshot"?: Snapshot}`.

## `test PUZZLE SOLUTION`

Runs every test case in the puzzle file, each on a fresh machine, and
prints per-test results and summary metrics. It takes `--max-ticks` and
`--cycles`. JSON output is a `SuiteResult`; see
[protocol.md](protocol.md#results).

## `types`

Lists the registered cell types, including any loaded plugins, with their
registers, instructions (opcode, operands, summary), and config fields.

## `serve --stdio`

Serves JSON-RPC 2.0 on stdin and stdout. See [protocol.md](protocol.md).
`--no-files` turns off path-based loading, so clients must send YAML
inline.

## `version`

Prints the version and the protocol version.
