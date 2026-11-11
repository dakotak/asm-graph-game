# Bytecode format (version 2)

Assembly compiles to a byte-oriented, variable-length encoding. **Program
memory is the length of this encoding in bytes.** The source text, line
count, instruction count, Go struct sizes, and host memory do not count.
There is no separate instruction-count limit.

## Instruction layout

```
opcode (1 byte) | operand 1 | operand 2 | …
```

The opcode identifies the instruction in the cell's ISA. The operand slots
and their kinds come from the instruction definition, so operands need no
count or length prefix.

| Opcode range | Owner |
|---|---|
| `0x00`–`0x3F` | Core instructions. Fixed across all architectures. Unassigned values are reserved. |
| `0x40`–`0xFF` | Architecture-specific instructions. The same byte can mean different instructions on different architectures. |

## Operand encoding

**Source and destination slots** start with a tag byte:

| Tag | Meaning | Total size |
|---|---|---|
| `0x00` | `NIL` | 1 |
| `0x01` | `ACC` | 1 |
| `0x02`–`0x0F` | other architecture registers | 1 |
| `0x10`–`0x13` | `P0`–`P3` | 1 |
| `0x14` | `ANY` | 1 |
| `0x15` | `LAST` | 1 |
| `0x44`–`0x7F` | `P4`–`P63` (`0x40` + port number) | 1 |
| `0x80`–`0xFF` | inline immediate −64…63 (low 7 bits, sign-extended) | 1 |
| `0x20` | immediate as a zigzag LEB128 varint (sources only) | 2–6 |

Every port costs one byte, whatever its number. `0x40`–`0x43` are
invalid, so each port has exactly one encoding. The bytecode can name up
to 64 physical ports; each architecture declares how many it actually
supports, and the assembler rejects ports the cell does not have.

**Target slots** have no tag. They hold the absolute **byte address** of
the target instruction as an unsigned LEB128 varint: 1 byte for addresses
below 128, 2 bytes below 16384, and 3 bytes up to 65535. The assembler
works out addresses by repeating the layout until every address is stable.
Addresses only grow from pass to pass, so this always finishes.

## Size examples

| Assembly | Bytes | Encoding |
|---|---|---|
| `NOP` | 1 | `00` |
| `ADD ACC` | 2 | `02 01` |
| `ADD 63` | 2 | `02 bf` |
| `ADD 64` | 4 | `02 20 80 01` |
| `ADD -2147483648` | 7 | `02 20 ff ff ff ff 0f` |
| `MOV P0, ACC` | 3 | `01 10 01` |
| `JMP 0` | 2 | `07 00` |
| `MOV ACC, P5` | 3 | `01 01 45` |
| `PADD` (stack_processor) | 1 | `46` |

## Determinism and validation

Encoding is deterministic: the same source and ISA always give the same
bytes. Every encoding is canonical, and decoding rejects anything that is
not:

- unknown opcodes for the architecture
- truncated operands
- invalid tags
- registers the architecture does not define
- immediates in destination slots
- varints that are overlong or non-canonical, including a `0x20` immediate
  whose value would fit in an inline byte
- values outside the int32 range
- jump targets that are not the start of an instruction

`vm.DecodeProgram` never panics. The tests decode every 1- and 2-byte
input to check this. The engine runs only decoded instruction lists, and
every compiled program is decoded again before it runs.

## Program images and versioning

`vm.Program` has `Version` (= `vm.BytecodeVersion` = 2), `Arch` (the cell
type), and `Code`. Version 2 added the `P4`–`P63` tags. Every version-1
image is also valid version 2, so loaders accept both. `MarshalBinary` produces:

```
"AGBC" | version u8 | uvarint len(arch) | arch | uvarint len(code) | code
```

Only `Code` counts toward program memory. The container header does not.
A future encoding would increase `BytecodeVersion`. Loaders reject
versions they do not know.

## Budgeting

```
asmgraph compile puzzle.yaml solution.yaml --disasm
```

This prints the bytes of every instruction. Ways to save space:

- Use small literals (−64…63).
- Rely on wraparound at the end of the program instead of `JMP 0`.
- Use architecture-specific instructions.
- Split the work across cells.
