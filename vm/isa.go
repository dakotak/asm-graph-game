package vm

import (
	"fmt"
	"sort"
	"strings"
)

// SlotKind describes what an instruction operand position accepts.
type SlotKind uint8

const (
	// SlotSource accepts a register, a port, or an integer literal.
	SlotSource SlotKind = iota + 1
	// SlotDest accepts a register or a port.
	SlotDest
	// SlotTarget accepts a jump target (label or instruction index).
	SlotTarget
)

func (k SlotKind) String() string {
	switch k {
	case SlotSource:
		return "source"
	case SlotDest:
		return "destination"
	case SlotTarget:
		return "target"
	}
	return "unknown"
}

// Accepts reports whether an operand of kind ok may occupy this slot.
func (k SlotKind) Accepts(ok OperandKind) bool {
	switch k {
	case SlotSource:
		return ok == OperandRegister || ok == OperandPort || ok == OperandImmediate
	case SlotDest:
		return ok == OperandRegister || ok == OperandPort
	case SlotTarget:
		return ok == OperandTarget
	}
	return false
}

// InstructionDef is the immutable description of one instruction.
type InstructionDef struct {
	Opcode   byte       `json:"opcode"`
	Mnemonic string     `json:"mnemonic"`
	Slots    []SlotKind `json:"slots"`
	Summary  string     `json:"summary"`
}

// DestIndex returns the operand index of the destination slot, or -1.
func (d *InstructionDef) DestIndex() int {
	for i, s := range d.Slots {
		if s == SlotDest {
			return i
		}
	}
	return -1
}

// RegisterDef names an addressable register of an architecture.
type RegisterDef struct {
	Code    uint8  `json:"code"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
}

// Register codes reserved by the core. NIL exists on every architecture.
// ACC, when an architecture provides it, must use code 0x01.
const (
	RegNIL          uint8 = 0x00
	RegACC          uint8 = 0x01
	MaxRegisterCode uint8 = 0x0F
)

// CoreOpcodeLimit: opcodes below this value are reserved for core
// instructions. Plugin instructions use 0x40..0xFF.
const CoreOpcodeLimit = 0x40

// Core opcodes. These values are part of bytecode format version 1 and
// must never change.
const (
	OpNOP byte = 0x00
	OpMOV byte = 0x01
	OpADD byte = 0x02
	OpSUB byte = 0x03
	OpNEG byte = 0x04
	OpSAV byte = 0x05
	OpSWP byte = 0x06
	OpJMP byte = 0x07
	OpJEZ byte = 0x08
	OpJNZ byte = 0x09
	OpJGZ byte = 0x0A
	OpJLZ byte = 0x0B
	OpHLT byte = 0x0F
)

var (
	src = []SlotKind{SlotSource}
	tgt = []SlotKind{SlotTarget}
)

var coreDefs = []InstructionDef{
	{OpNOP, "NOP", nil, "Do nothing."},
	{OpMOV, "MOV", []SlotKind{SlotSource, SlotDest}, "Copy <source> to <destination>."},
	{OpADD, "ADD", src, "ACC = ACC + <source> (wrapping)."},
	{OpSUB, "SUB", src, "ACC = ACC - <source> (wrapping)."},
	{OpNEG, "NEG", nil, "ACC = -ACC (wrapping)."},
	{OpSAV, "SAV", nil, "BAK = ACC."},
	{OpSWP, "SWP", nil, "Exchange ACC and BAK."},
	{OpJMP, "JMP", tgt, "Jump to <target>."},
	{OpJEZ, "JEZ", tgt, "Jump to <target> if ACC == 0."},
	{OpJNZ, "JNZ", tgt, "Jump to <target> if ACC != 0."},
	{OpJGZ, "JGZ", tgt, "Jump to <target> if ACC > 0."},
	{OpJLZ, "JLZ", tgt, "Jump to <target> if ACC < 0."},
	{OpHLT, "HLT", nil, "Halt this cell permanently."},
}

var (
	coreByOp   = map[byte]InstructionDef{}
	coreByName = map[string]InstructionDef{}
)

func init() {
	for _, d := range coreDefs {
		coreByOp[d.Opcode] = d
		coreByName[d.Mnemonic] = d
	}
}

// CoreInstructions returns a copy of every core instruction definition.
func CoreInstructions() []InstructionDef {
	out := make([]InstructionDef, len(coreDefs))
	for i, d := range coreDefs {
		d.Slots = append([]SlotKind(nil), d.Slots...)
		out[i] = d
	}
	return out
}

// Core returns the core definitions for the named mnemonics. It panics on
// an unknown mnemonic, which is a programming error in an architecture.
func Core(mnemonics ...string) []InstructionDef {
	out := make([]InstructionDef, 0, len(mnemonics))
	for _, m := range mnemonics {
		d, ok := coreByName[strings.ToUpper(m)]
		if !ok {
			panic("vm: unknown core instruction " + m)
		}
		d.Slots = append([]SlotKind(nil), d.Slots...)
		out = append(out, d)
	}
	return out
}

// IsCoreMnemonic reports whether m names a core instruction.
func IsCoreMnemonic(m string) bool {
	_, ok := coreByName[strings.ToUpper(m)]
	return ok
}

// ISA is the immutable instruction set and register file description of
// one architecture. It drives both the assembler and the decoder.
type ISA struct {
	name      string
	byOp      [256]*InstructionDef
	byName    map[string]*InstructionDef
	instrs    []InstructionDef
	regs      [int(MaxRegisterCode) + 1]*RegisterDef
	regByName map[string]*RegisterDef
	regList   []RegisterDef
}

// NewISA validates and builds an instruction set. It enforces that core
// opcodes keep their core mnemonic and operand layout, that plugin
// instructions do not reuse core mnemonics, and that register codes and
// names are unambiguous.
func NewISA(name string, instrs []InstructionDef, regs []RegisterDef) (*ISA, error) {
	isa := &ISA{
		name:      name,
		byName:    map[string]*InstructionDef{},
		regByName: map[string]*RegisterDef{},
	}
	isa.addReg(RegisterDef{Code: RegNIL, Name: "NIL", Summary: "Reads as 0; writes are discarded."})
	for _, r := range regs {
		r.Name = strings.ToUpper(r.Name)
		switch {
		case r.Code == RegNIL || r.Name == "NIL":
			return nil, fmt.Errorf("isa %s: register NIL is implicit and cannot be redefined", name)
		case r.Code > MaxRegisterCode:
			return nil, fmt.Errorf("isa %s: register %s code 0x%02X exceeds 0x%02X", name, r.Name, r.Code, MaxRegisterCode)
		case (r.Code == RegACC) != (r.Name == "ACC"):
			return nil, fmt.Errorf("isa %s: register ACC must use code 0x01 and code 0x01 must be ACC", name)
		case !validIdent(r.Name):
			return nil, fmt.Errorf("isa %s: invalid register name %q", name, r.Name)
		}
		if _, ok := ParsePort(r.Name); ok {
			return nil, fmt.Errorf("isa %s: register name %s collides with a port name", name, r.Name)
		}
		if isa.regs[r.Code] != nil || isa.regByName[r.Name] != nil {
			return nil, fmt.Errorf("isa %s: duplicate register %s (0x%02X)", name, r.Name, r.Code)
		}
		isa.addReg(r)
	}
	for _, d := range instrs {
		d.Mnemonic = strings.ToUpper(d.Mnemonic)
		d.Slots = append([]SlotKind(nil), d.Slots...)
		if d.Opcode < CoreOpcodeLimit {
			core, ok := coreByOp[d.Opcode]
			if !ok {
				return nil, fmt.Errorf("isa %s: opcode 0x%02X is reserved for core instructions", name, d.Opcode)
			}
			if core.Mnemonic != d.Mnemonic || !slotsEqual(core.Slots, d.Slots) {
				return nil, fmt.Errorf("isa %s: opcode 0x%02X belongs to core instruction %s and cannot be redefined", name, d.Opcode, core.Mnemonic)
			}
			if d.Summary == "" {
				d.Summary = core.Summary
			}
		} else if core, ok := coreByName[d.Mnemonic]; ok {
			return nil, fmt.Errorf("isa %s: mnemonic %s is a core instruction (opcode 0x%02X) and cannot be redefined", name, d.Mnemonic, core.Opcode)
		}
		if !validIdent(d.Mnemonic) {
			return nil, fmt.Errorf("isa %s: invalid mnemonic %q", name, d.Mnemonic)
		}
		if isa.byOp[d.Opcode] != nil {
			return nil, fmt.Errorf("isa %s: duplicate opcode 0x%02X", name, d.Opcode)
		}
		if isa.byName[d.Mnemonic] != nil {
			return nil, fmt.Errorf("isa %s: duplicate mnemonic %s", name, d.Mnemonic)
		}
		if len(d.Slots) > 3 {
			return nil, fmt.Errorf("isa %s: %s has more than 3 operands", name, d.Mnemonic)
		}
		dests := 0
		for _, s := range d.Slots {
			if s < SlotSource || s > SlotTarget {
				return nil, fmt.Errorf("isa %s: %s has an invalid operand slot", name, d.Mnemonic)
			}
			if s == SlotDest {
				dests++
			}
		}
		if dests > 1 {
			return nil, fmt.Errorf("isa %s: %s has more than one destination operand", name, d.Mnemonic)
		}
		def := d
		isa.byOp[d.Opcode] = &def
		isa.byName[d.Mnemonic] = &def
		isa.instrs = append(isa.instrs, def)
	}
	sort.Slice(isa.instrs, func(i, j int) bool { return isa.instrs[i].Opcode < isa.instrs[j].Opcode })
	return isa, nil
}

func (isa *ISA) addReg(r RegisterDef) {
	def := r
	isa.regs[r.Code] = &def
	isa.regByName[r.Name] = &def
	isa.regList = append(isa.regList, def)
}

// Name returns the architecture name this ISA belongs to.
func (isa *ISA) Name() string { return isa.name }

// Lookup finds an instruction by mnemonic (case-insensitive).
func (isa *ISA) Lookup(mnemonic string) (*InstructionDef, bool) {
	d, ok := isa.byName[strings.ToUpper(mnemonic)]
	return d, ok
}

// ByOpcode finds an instruction by opcode.
func (isa *ISA) ByOpcode(op byte) (*InstructionDef, bool) {
	d := isa.byOp[op]
	return d, d != nil
}

// Instructions lists the instruction set ordered by opcode.
func (isa *ISA) Instructions() []InstructionDef {
	return append([]InstructionDef(nil), isa.instrs...)
}

// Register finds a register code by name (case-insensitive).
func (isa *ISA) Register(name string) (uint8, bool) {
	r, ok := isa.regByName[strings.ToUpper(name)]
	if !ok {
		return 0, false
	}
	return r.Code, true
}

// RegisterName returns the name for a register code, or "" if undefined.
func (isa *ISA) RegisterName(code uint8) string {
	if int(code) >= len(isa.regs) || isa.regs[code] == nil {
		return ""
	}
	return isa.regs[code].Name
}

// Registers lists the addressable registers, including NIL.
func (isa *ISA) Registers() []RegisterDef {
	return append([]RegisterDef(nil), isa.regList...)
}

func slotsEqual(a, b []SlotKind) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func validIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r == '_':
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}
