package vm

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// BytecodeVersion identifies the instruction encoding described in
// docs/bytecode.md. Program images carry it so future encodings can coexist.
const BytecodeVersion uint8 = 1

// MaxProgramBytes is the largest program any cell may hold in V1.
const MaxProgramBytes = 65535

// OperandKind classifies a decoded operand.
type OperandKind uint8

const (
	OperandRegister OperandKind = iota + 1
	OperandPort
	OperandImmediate
	OperandTarget
)

func (k OperandKind) String() string {
	switch k {
	case OperandRegister:
		return "register"
	case OperandPort:
		return "port"
	case OperandImmediate:
		return "immediate"
	case OperandTarget:
		return "target"
	}
	return "unknown"
}

// Operand is a decoded instruction operand.
type Operand struct {
	Kind     OperandKind
	Register uint8 // OperandRegister
	Port     Port  // OperandPort
	Value    Word  // OperandImmediate
	// Target is the absolute byte address of a jump target (OperandTarget).
	Target uint32
	// TargetIndex is the instruction index at Target. It is filled in by
	// DecodeProgram and by the assembler.
	TargetIndex int
}

// Operand tag bytes for source/destination slots.
//
//	0x00..0x0F  register (0x00 = NIL, 0x01 = ACC, others per architecture)
//	0x10..0x15  port P0, P1, P2, P3, ANY, LAST
//	0x20        immediate, followed by a zigzag varint (int32 range)
//	0x80..0xFF  inline immediate -64..63 (low 7 bits, sign-extended)
//
// Target slots carry no tag: they are an unsigned LEB128 byte address.
const (
	tagPortBase   byte = 0x10
	tagPortMax    byte = 0x15
	tagImmVarint  byte = 0x20
	tagInlineImm  byte = 0x80
	InlineImmMin  Word = -64
	InlineImmMax  Word = 63
	maxVarintSize      = 5
)

// OperandSize returns the encoded size of op in bytes.
func OperandSize(op Operand) int {
	switch op.Kind {
	case OperandImmediate:
		if op.Value >= InlineImmMin && op.Value <= InlineImmMax {
			return 1
		}
		return 1 + varintLen(int64(op.Value))
	case OperandTarget:
		return uvarintLen(uint64(op.Target))
	}
	return 1
}

// InstructionSize returns the encoded size of an instruction.
func InstructionSize(ops []Operand) int {
	n := 1
	for _, op := range ops {
		n += OperandSize(op)
	}
	return n
}

// EncodeOperand appends the encoding of op for the given slot.
func EncodeOperand(dst []byte, slot SlotKind, op Operand) ([]byte, error) {
	if !slot.Accepts(op.Kind) {
		return dst, fmt.Errorf("operand kind %s not allowed in %s slot", op.Kind, slot)
	}
	switch op.Kind {
	case OperandRegister:
		if op.Register > MaxRegisterCode {
			return dst, fmt.Errorf("register code 0x%02X out of range", op.Register)
		}
		return append(dst, op.Register), nil
	case OperandPort:
		if op.Port > PortLast {
			return dst, fmt.Errorf("invalid port %d", op.Port)
		}
		return append(dst, tagPortBase+byte(op.Port)), nil
	case OperandImmediate:
		if op.Value >= InlineImmMin && op.Value <= InlineImmMax {
			return append(dst, tagInlineImm|(byte(op.Value)&0x7F)), nil
		}
		dst = append(dst, tagImmVarint)
		return binary.AppendVarint(dst, int64(op.Value)), nil
	case OperandTarget:
		return binary.AppendUvarint(dst, uint64(op.Target)), nil
	}
	return dst, fmt.Errorf("unknown operand kind %d", op.Kind)
}

// EncodeInstruction appends the encoding of one instruction.
func EncodeInstruction(dst []byte, def *InstructionDef, ops []Operand) ([]byte, error) {
	if len(ops) != len(def.Slots) {
		return dst, fmt.Errorf("%s expects %d operands, got %d", def.Mnemonic, len(def.Slots), len(ops))
	}
	dst = append(dst, def.Opcode)
	var err error
	for i, slot := range def.Slots {
		if dst, err = EncodeOperand(dst, slot, ops[i]); err != nil {
			return dst, fmt.Errorf("%s operand %d: %w", def.Mnemonic, i+1, err)
		}
	}
	return dst, nil
}

// Instruction is a decoded instruction located in a program.
type Instruction struct {
	Def      *InstructionDef
	Operands []Operand
	Offset   uint32 // byte offset of the opcode
	Size     int    // encoded size in bytes
}

// Format renders the instruction as assembly. Jump targets are rendered
// as instruction indices, which the assembler accepts, so disassembly
// round-trips.
func (in *Instruction) Format(isa *ISA) string {
	var b strings.Builder
	b.WriteString(in.Def.Mnemonic)
	for i, op := range in.Operands {
		if i == 0 {
			b.WriteByte(' ')
		} else {
			b.WriteString(", ")
		}
		switch op.Kind {
		case OperandRegister:
			if isa != nil {
				b.WriteString(isa.RegisterName(op.Register))
			} else {
				fmt.Fprintf(&b, "R%d", op.Register)
			}
		case OperandPort:
			b.WriteString(op.Port.String())
		case OperandImmediate:
			b.WriteString(strconv.FormatInt(int64(op.Value), 10))
		case OperandTarget:
			b.WriteString(strconv.Itoa(op.TargetIndex))
		}
	}
	return b.String()
}

// DecodeError reports malformed bytecode.
type DecodeError struct {
	Offset int
	Msg    string
}

func (e *DecodeError) Error() string {
	return fmt.Sprintf("bytecode offset %d: %s", e.Offset, e.Msg)
}

// DecodeInstruction decodes the instruction starting at code[off].
// It never panics on malformed input.
func DecodeInstruction(isa *ISA, code []byte, off int) (Instruction, error) {
	if off < 0 || off >= len(code) {
		return Instruction{}, &DecodeError{off, "offset out of range"}
	}
	def, ok := isa.ByOpcode(code[off])
	if !ok {
		return Instruction{}, &DecodeError{off, fmt.Sprintf("unknown opcode 0x%02X for architecture %s", code[off], isa.Name())}
	}
	pos := off + 1
	ops := make([]Operand, len(def.Slots))
	for i, slot := range def.Slots {
		op, n, err := decodeOperand(isa, slot, code[pos:])
		if err != nil {
			return Instruction{}, &DecodeError{pos, fmt.Sprintf("%s operand %d: %v", def.Mnemonic, i+1, err)}
		}
		ops[i] = op
		pos += n
	}
	return Instruction{Def: def, Operands: ops, Offset: uint32(off), Size: pos - off}, nil
}

var errTruncated = errors.New("truncated operand")

func decodeOperand(isa *ISA, slot SlotKind, b []byte) (Operand, int, error) {
	if len(b) == 0 {
		return Operand{}, 0, errTruncated
	}
	if slot == SlotTarget {
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return Operand{}, 0, errors.New("malformed target varint")
		}
		if n != uvarintLen(v) {
			return Operand{}, 0, errors.New("non-canonical target encoding")
		}
		if v > MaxProgramBytes {
			return Operand{}, 0, fmt.Errorf("target %d out of range", v)
		}
		return Operand{Kind: OperandTarget, Target: uint32(v)}, n, nil
	}
	t := b[0]
	switch {
	case t >= tagInlineImm:
		if slot != SlotSource {
			return Operand{}, 0, fmt.Errorf("immediate not allowed in %s slot", slot)
		}
		return Operand{Kind: OperandImmediate, Value: Word(int8(t<<1) >> 1)}, 1, nil
	case t == tagImmVarint:
		if slot != SlotSource {
			return Operand{}, 0, fmt.Errorf("immediate not allowed in %s slot", slot)
		}
		rest := b[1:]
		if len(rest) > maxVarintSize {
			rest = rest[:maxVarintSize]
		}
		v, n := binary.Varint(rest)
		if n <= 0 {
			return Operand{}, 0, errors.New("malformed immediate varint")
		}
		w, ok := FromInt64(v)
		if !ok {
			return Operand{}, 0, fmt.Errorf("immediate %d outside int32 range", v)
		}
		if n != varintLen(v) || (w >= InlineImmMin && w <= InlineImmMax) {
			return Operand{}, 0, errors.New("non-canonical immediate encoding")
		}
		return Operand{Kind: OperandImmediate, Value: w}, 1 + n, nil
	case t <= MaxRegisterCode:
		if isa.RegisterName(t) == "" {
			return Operand{}, 0, fmt.Errorf("register 0x%02X not defined by architecture %s", t, isa.Name())
		}
		return Operand{Kind: OperandRegister, Register: t}, 1, nil
	case t >= tagPortBase && t <= tagPortMax:
		return Operand{Kind: OperandPort, Port: Port(t - tagPortBase)}, 1, nil
	}
	return Operand{}, 0, fmt.Errorf("invalid operand tag 0x%02X", t)
}

// DecodeProgram decodes a complete program, verifying that every byte
// belongs to exactly one instruction and that every jump target lands on
// an instruction boundary. It fills in Operand.TargetIndex.
func DecodeProgram(isa *ISA, code []byte) ([]Instruction, error) {
	if len(code) > MaxProgramBytes {
		return nil, &DecodeError{0, fmt.Sprintf("program of %d bytes exceeds %d", len(code), MaxProgramBytes)}
	}
	var out []Instruction
	starts := map[uint32]int{}
	for off := 0; off < len(code); {
		in, err := DecodeInstruction(isa, code, off)
		if err != nil {
			return nil, err
		}
		starts[uint32(off)] = len(out)
		out = append(out, in)
		off += in.Size
	}
	for i := range out {
		for j := range out[i].Operands {
			op := &out[i].Operands[j]
			if op.Kind != OperandTarget {
				continue
			}
			idx, ok := starts[op.Target]
			if !ok {
				return nil, &DecodeError{int(out[i].Offset), fmt.Sprintf("jump target %d is not an instruction boundary", op.Target)}
			}
			op.TargetIndex = idx
		}
	}
	return out, nil
}

func varintLen(v int64) int   { return len(binary.AppendVarint(nil, v)) }
func uvarintLen(v uint64) int { return len(binary.AppendUvarint(nil, v)) }

// Program is a compiled program image. Only Code counts toward a cell's
// program memory; the version and architecture tag are container
// metadata.
type Program struct {
	Version uint8  `json:"version"`
	Arch    string `json:"arch"`
	Code    []byte `json:"code"`
}

// Size returns the program-memory footprint in bytes.
func (p *Program) Size() int { return len(p.Code) }

var programMagic = [4]byte{'A', 'G', 'B', 'C'}

// MarshalBinary serializes the program image:
// "AGBC" | version u8 | uvarint len(arch) | arch | uvarint len(code) | code.
func (p *Program) MarshalBinary() ([]byte, error) {
	b := append([]byte(nil), programMagic[:]...)
	b = append(b, p.Version)
	b = binary.AppendUvarint(b, uint64(len(p.Arch)))
	b = append(b, p.Arch...)
	b = binary.AppendUvarint(b, uint64(len(p.Code)))
	return append(b, p.Code...), nil
}

// UnmarshalBinary parses a program image produced by MarshalBinary.
func (p *Program) UnmarshalBinary(b []byte) error {
	if len(b) < 5 || [4]byte(b[:4]) != programMagic {
		return errors.New("not an asmgraph program image")
	}
	ver := b[4]
	if ver != BytecodeVersion {
		return fmt.Errorf("unsupported bytecode version %d", ver)
	}
	rest := b[5:]
	readChunk := func(max uint64) ([]byte, error) {
		n, k := binary.Uvarint(rest)
		if k <= 0 || n > max || n > uint64(len(rest)-k) {
			return nil, errors.New("malformed program image")
		}
		chunk := rest[k : k+int(n)]
		rest = rest[k+int(n):]
		return chunk, nil
	}
	arch, err := readChunk(255)
	if err != nil {
		return err
	}
	code, err := readChunk(MaxProgramBytes)
	if err != nil {
		return err
	}
	if len(rest) != 0 {
		return errors.New("trailing bytes after program image")
	}
	p.Version, p.Arch, p.Code = ver, string(arch), append([]byte(nil), code...)
	return nil
}
