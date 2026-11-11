# Assembly language

Every architecture shares one parser. An architecture's `vm.ISA` decides
which mnemonics and registers exist. The operand types and their meanings
are the same everywhere.

## Syntax

```
line     = { label ":" } [ mnemonic [ operand { [","] operand } ] ] [ comment ]
comment  = "#" … | ";" …
label    = identifier
operand  = identifier | integer
integer  = ["+"|"-"] ( decimal | 0x hex | 0b binary | 0o octal )
```

- Mnemonics, registers, ports, and labels are **case-insensitive**.
- Commas between operands are optional: `MOV P0 ACC` is the same as
  `MOV P0, ACC`. Leading, trailing, or doubled commas are errors.
- A line can hold several labels: `A: B: NOP`. A label can also stand on
  its own line before the instruction.
- Labels at the end of the program, with no instruction after them, refer
  to instruction 0. Execution wraps around, so this is where they would
  lead anyway.
- A label cannot have the name of a port or one of the cell's registers.
- `010` is decimal ten. There is no implicit octal.

Every error reports the line and column inside the program. The CLI also
maps it to the line in the solution file.

## Operands

| Slot | Accepts |
|---|---|
| source | a register, a port, or an integer literal |
| destination | a register or a port |
| target | a label, or an **instruction index** (0-based) |

**Registers.** `NIL` exists on every architecture: it reads as 0 and
discards writes. `ACC` is available on all built-in processors. Other
registers are defined by the architecture. `BAK` cannot be used as an
operand; only `SAV` and `SWP` reach it.

**Ports.** `P0`, `P1`, … are the cell's physical ports: a cell with *n*
ports has `P0` to `P(n-1)` (four on schema-v1 cells; configurable on
player-built hardware). Naming a port the cell does not have is an
assembly error. `ANY` and `LAST` are selectors that the engine resolves
over the cell's ports. Any instruction may read from or write
to a port, and the engine handles the blocking. See
[execution.md](execution.md#communication).

**Numeric jump targets** are instruction indices, not byte addresses.
`JMP 0` means the first instruction. An index equal to the number of
instructions wraps to 0. Disassembly prints targets as indices, so the
output can be assembled again unchanged.

## Integer semantics

All values are signed 32-bit integers (two's complement):

| Operation | Behaviour |
|---|---|
| `ADD`, `SUB`, `PADD` | Wrap around: `2147483647 + 1 = -2147483648` |
| `NEG` | Wraps: `-(-2147483648) = -2147483648` |
| Literals | Must be in [−2147483648, 2147483647]. Out-of-range literals are compile errors and never wrap. `0xFFFFFFFF` is rejected. |
| Comparisons (`JGZ`, `JLZ`, …) | Signed |
| Stream values | Range-checked when the puzzle is validated |
| Plugin arithmetic | Must use `vm.Word` (int32). Go defines int32 overflow as wraparound. |

## Core instructions

Core opcodes are fixed across all architectures. An architecture may
support only some of them, but it may never change their meaning.

| Opcode | Instruction | Effect |
|---|---|---|
| 0x00 | `NOP` | Nothing |
| 0x01 | `MOV src, dst` | `dst = src` |
| 0x02 | `ADD src` | `ACC = ACC + src` |
| 0x03 | `SUB src` | `ACC = ACC - src` |
| 0x04 | `NEG` | `ACC = -ACC` |
| 0x05 | `SAV` | `BAK = ACC` |
| 0x06 | `SWP` | swap `ACC` and `BAK` |
| 0x07 | `JMP tgt` | jump |
| 0x08 | `JEZ tgt` | jump if `ACC == 0` |
| 0x09 | `JNZ tgt` | jump if `ACC != 0` |
| 0x0A | `JGZ tgt` | jump if `ACC > 0` |
| 0x0B | `JLZ tgt` | jump if `ACC < 0` |
| 0x0F | `HLT` | halt this cell permanently |

**End of program.** After the last instruction, execution continues at
instruction 0. An empty program means the cell is idle. `HLT` stops the
cell; it keeps its registers and never runs again in that test case.
Programs never have to halt: a test ends when its outputs are complete.

## Built-in architectures

### `standard`

Registers `ACC` and `BAK` (`BAK` is not addressable). Supports every core
instruction. No configuration. 1–8 ports (default 4), 1–1024 bytes of
program memory.

### `stack_processor`

Register `ACC` and a bounded LIFO stack (`stack_size`, default 16). There
is no `BAK`, so `SAV` and `SWP` are not available. Schema-v1 puzzles call
this type `stack`; it is renamed automatically.

| Opcode | Instruction | Bytes | Effect |
|---|---|---|---|
| 0x40 | `PUSH src` | 2+ | push `src`; overflow is a runtime error |
| 0x41 | `POP dst` | 2 | pop into `dst`; underflow is a runtime error |
| 0x42 | `DUP` | 1 | push a copy of the top value |
| 0x43 | `DROP` | 1 | discard the top value |
| 0x44 | `SWAP` | 1 | swap the top two values |
| 0x45 | `JSE tgt` | 2+ | jump if the stack is empty |
| 0x46 | `PADD` | 1 | `ACC += pop()` |
| 0x47 | `DEPTH` | 1 | `ACC = stack depth` |

Several of these are one byte. For example, `PADD` adds a stored value to
`ACC` in 1 byte, which a standard processor cannot do at all. These
compact encodings are deliberate: specialised cells trade generality for
program density.

`POP P1` pops when it executes. The value then waits in the instruction's
pending-output slot until it is delivered (see
[execution.md](execution.md#instruction-lifecycle)).

### `stack` (passive)

Not programmable: a LIFO peripheral whose ports are assigned `PUSH`,
`POP`, `PEEK`, or `SIZE`. Processors use it with ordinary port reads and
writes, for example `MOV ACC, P1` into a `PUSH` port. See
[hardware.md](hardware.md#the-passive-stack).

### `input` / `output`

Not programmable. See [execution.md](execution.md#streams).
