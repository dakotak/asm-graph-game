package vm

import (
	"bytes"
	"math"
	"strings"
	"testing"
)

func testISA(t *testing.T) *ISA {
	t.Helper()
	instrs := CoreInstructions()
	instrs = append(instrs, InstructionDef{Opcode: 0x40, Mnemonic: "PUSH", Slots: []SlotKind{SlotSource}})
	isa, err := NewISA("test", instrs, []RegisterDef{{Code: RegACC, Name: "ACC"}, {Code: 0x02, Name: "X"}})
	if err != nil {
		t.Fatal(err)
	}
	return isa
}

func TestWrappingArithmetic(t *testing.T) {
	if Add(MaxWord, 1) != MinWord {
		t.Error("MaxWord + 1 should wrap to MinWord")
	}
	if Sub(MinWord, 1) != MaxWord {
		t.Error("MinWord - 1 should wrap to MaxWord")
	}
	if Neg(MinWord) != MinWord {
		t.Error("-MinWord should be MinWord")
	}
	if Add(MaxWord, MaxWord) != -2 {
		t.Error("MaxWord*2 should be -2")
	}
	if _, ok := FromInt64(math.MaxInt32 + 1); ok {
		t.Error("FromInt64 accepted out-of-range value")
	}
	if v, ok := FromInt64(math.MinInt32); !ok || v != MinWord {
		t.Error("FromInt64 rejected MinInt32")
	}
}

func TestISARejectsCoreRedefinition(t *testing.T) {
	cases := []struct {
		name   string
		instrs []InstructionDef
		regs   []RegisterDef
		want   string
	}{
		{"core opcode renamed", []InstructionDef{{Opcode: OpADD, Mnemonic: "PLUS", Slots: []SlotKind{SlotSource}}}, nil, "cannot be redefined"},
		{"core opcode reshaped", []InstructionDef{{Opcode: OpADD, Mnemonic: "ADD"}}, nil, "cannot be redefined"},
		{"core mnemonic reused", []InstructionDef{{Opcode: 0x50, Mnemonic: "MOV", Slots: []SlotKind{SlotSource, SlotDest}}}, nil, "core instruction"},
		{"reserved opcode", []InstructionDef{{Opcode: 0x20, Mnemonic: "FOO"}}, nil, "reserved"},
		{"duplicate opcode", []InstructionDef{{Opcode: 0x50, Mnemonic: "FOO"}, {Opcode: 0x50, Mnemonic: "BAR"}}, nil, "duplicate opcode"},
		{"two destinations", []InstructionDef{{Opcode: 0x50, Mnemonic: "FOO", Slots: []SlotKind{SlotDest, SlotDest}}}, nil, "more than one destination"},
		{"redefine NIL", nil, []RegisterDef{{Code: 0x03, Name: "NIL"}}, "NIL"},
		{"ACC wrong code", nil, []RegisterDef{{Code: 0x03, Name: "ACC"}}, "ACC"},
		{"register named like port", nil, []RegisterDef{{Code: 0x03, Name: "ANY"}}, "port"},
		{"register code too big", nil, []RegisterDef{{Code: 0x10, Name: "BIG"}}, "exceeds"},
	}
	for _, c := range cases {
		_, err := NewISA("x", c.instrs, c.regs)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want error containing %q", c.name, err, c.want)
		}
	}
}

func TestOperandEncodingRoundTrip(t *testing.T) {
	isa := testISA(t)
	mov, _ := isa.Lookup("MOV")
	values := []Word{0, 1, -1, 63, -64, 64, -65, 127, 128, -129, 1000, -1000, 1 << 20, MaxWord, MinWord}
	for _, v := range values {
		ops := []Operand{{Kind: OperandImmediate, Value: v}, {Kind: OperandPort, Port: P3}}
		code, err := EncodeInstruction(nil, mov, ops)
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != InstructionSize(ops) {
			t.Errorf("value %d: size %d, InstructionSize %d", v, len(code), InstructionSize(ops))
		}
		in, err := DecodeInstruction(isa, code, 0)
		if err != nil {
			t.Fatalf("value %d: %v", v, err)
		}
		if in.Operands[0].Value != v || in.Operands[1].Port != P3 || in.Size != len(code) {
			t.Errorf("value %d: decoded %+v", v, in)
		}
	}
	// Inline immediates are a single byte.
	code, _ := EncodeInstruction(nil, mov, []Operand{{Kind: OperandImmediate, Value: -64}, {Kind: OperandRegister, Register: RegACC}})
	if !bytes.Equal(code, []byte{OpMOV, 0xC0, RegACC}) {
		t.Errorf("MOV -64, ACC encoded as % x", code)
	}
	code, _ = EncodeInstruction(nil, mov, []Operand{{Kind: OperandImmediate, Value: 64}, {Kind: OperandRegister, Register: RegACC}})
	if !bytes.Equal(code, []byte{OpMOV, tagImmVarint, 0x80, 0x01, RegACC}) {
		t.Errorf("MOV 64, ACC encoded as % x", code)
	}
}

func TestEncodeRejectsBadOperands(t *testing.T) {
	isa := testISA(t)
	mov, _ := isa.Lookup("MOV")
	if _, err := EncodeInstruction(nil, mov, []Operand{{Kind: OperandRegister}}); err == nil {
		t.Error("wrong operand count accepted")
	}
	if _, err := EncodeInstruction(nil, mov, []Operand{{Kind: OperandImmediate}, {Kind: OperandImmediate}}); err == nil {
		t.Error("immediate destination accepted")
	}
}

func TestDecodeMalformed(t *testing.T) {
	isa := testISA(t)
	cases := map[string][]byte{
		"unknown opcode":         {0x3E},
		"truncated operand":      {OpMOV, 0x10},
		"truncated varint":       {OpADD, tagImmVarint, 0x80},
		"bad tag":                {OpADD, 0x30},
		"undefined register":     {OpADD, 0x05},
		"immediate destination":  {OpMOV, 0x10, 0x81},
		"non-canonical small":    {OpADD, tagImmVarint, 0x02},
		"out of int32 range":     {OpADD, tagImmVarint, 0x80, 0x80, 0x80, 0x80, 0x10},
		"overlong varint":        {OpADD, tagImmVarint, 0x80, 0x80, 0x80, 0x80, 0x80, 0x01},
		"target mid-instruction": {OpMOV, 0x10, 0x01, OpJMP, 0x01},
		"target past end":        {OpJMP, 0x09},
		"non-canonical target":   {OpJMP, 0x80, 0x00},
	}
	for name, code := range cases {
		if _, err := DecodeProgram(isa, code); err == nil {
			t.Errorf("%s: decoded % x without error", name, code)
		}
	}
}

func TestDecodeNeverPanics(t *testing.T) {
	isa := testISA(t)
	// Exhaustively decode every 1- and 2-byte program and a sample of 3-byte ones.
	for a := 0; a < 256; a++ {
		_, _ = DecodeProgram(isa, []byte{byte(a)})
		for b := 0; b < 256; b++ {
			_, _ = DecodeProgram(isa, []byte{byte(a), byte(b)})
			_, _ = DecodeProgram(isa, []byte{byte(a), byte(b), byte(a ^ b)})
		}
	}
}

func TestDecodeProgramTargets(t *testing.T) {
	isa := testISA(t)
	// 0: MOV P0, ACC (3 bytes)  3: JMP 0
	code := []byte{OpMOV, 0x10, RegACC, OpJMP, 0x00}
	ins, err := DecodeProgram(isa, code)
	if err != nil {
		t.Fatal(err)
	}
	if len(ins) != 2 || ins[1].Operands[0].TargetIndex != 0 || ins[1].Offset != 3 {
		t.Fatalf("unexpected decode: %+v", ins)
	}
	if got := ins[0].Format(isa); got != "MOV P0, ACC" {
		t.Errorf("format = %q", got)
	}
	if got := ins[1].Format(isa); got != "JMP 0" {
		t.Errorf("format = %q", got)
	}
}

func TestProgramImage(t *testing.T) {
	p := Program{Version: BytecodeVersion, Arch: "standard", Code: []byte{1, 2, 3}}
	b, _ := p.MarshalBinary()
	var q Program
	if err := q.UnmarshalBinary(b); err != nil {
		t.Fatal(err)
	}
	if q.Arch != "standard" || !bytes.Equal(q.Code, p.Code) || q.Size() != 3 {
		t.Errorf("round trip mismatch: %+v", q)
	}
	bad := append([]byte(nil), b...)
	bad[4] = 99
	if err := q.UnmarshalBinary(bad); err == nil {
		t.Error("accepted unknown version")
	}
	if err := q.UnmarshalBinary(append(b, 0)); err == nil {
		t.Error("accepted trailing bytes")
	}
	if err := q.UnmarshalBinary(b[:len(b)-1]); err == nil {
		t.Error("accepted truncated image")
	}
}

func TestParsePort(t *testing.T) {
	for _, s := range []string{"p0", "P3", "any", "LAST"} {
		if _, ok := ParsePort(s); !ok {
			t.Errorf("ParsePort(%q) failed", s)
		}
	}
	if _, ok := ParsePhysicalPort("ANY"); ok {
		t.Error("ANY is not physical")
	}
	for _, s := range []string{"P4", "p7", "P63"} {
		if _, ok := ParsePhysicalPort(s); !ok {
			t.Errorf("%s should be a physical port", s)
		}
	}
	for _, s := range []string{"P64", "P04", "P", "PX", "P-1"} {
		if _, ok := ParsePort(s); ok {
			t.Errorf("%s should not be a port", s)
		}
	}
	if PortN(12).String() != "P12" || PortAny.String() != "ANY" || PortLast.String() != "LAST" {
		t.Error("port names")
	}
}

// Every port has exactly one single-byte encoding; P0..P3, ANY and LAST
// keep their version-1 tags so old programs decode unchanged.
func TestPortEncoding(t *testing.T) {
	isa := testISA(t)
	mov, _ := isa.Lookup("MOV")
	want := map[Port]byte{P0: 0x10, P3: 0x13, PortAny: 0x14, PortLast: 0x15, PortN(4): 0x44, PortN(63): 0x7F}
	for p, tag := range want {
		ops := []Operand{{Kind: OperandRegister, Register: RegACC}, {Kind: OperandPort, Port: p}}
		code, err := EncodeInstruction(nil, mov, ops)
		if err != nil || len(code) != 3 || code[2] != tag {
			t.Fatalf("%s: % x %v", p, code, err)
		}
		in, err := DecodeInstruction(isa, code, 0)
		if err != nil || in.Operands[1].Port != p {
			t.Fatalf("%s: decoded %+v %v", p, in, err)
		}
	}
	for _, bad := range []byte{0x40, 0x43, 0x16} {
		if _, err := DecodeInstruction(isa, []byte{OpMOV, RegACC, bad}, 0); err == nil {
			t.Errorf("tag 0x%02X should be invalid", bad)
		}
	}
}
