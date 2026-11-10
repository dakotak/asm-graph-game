package asm_test

import (
	"bytes"
	"strings"
	"testing"

	"asmgraph/asm"
	"asmgraph/cells"
	"asmgraph/vm"
)

var std = cells.NewStandard().ISA()
var stk = cells.NewStack().ISA()

func compile(t *testing.T, src string, isa *vm.ISA, limit int) *asm.Result {
	t.Helper()
	res, errs := asm.Compile(src, isa, asm.Options{MemoryLimit: limit})
	if len(errs) > 0 {
		t.Fatalf("compile failed:\n%v", errs)
	}
	return res
}

func compileErr(t *testing.T, src string, isa *vm.ISA, limit int, want string) asm.ErrorList {
	t.Helper()
	_, errs := asm.Compile(src, isa, asm.Options{MemoryLimit: limit})
	if len(errs) == 0 {
		t.Fatalf("expected error containing %q, compiled fine", want)
	}
	if !strings.Contains(errs.Error(), want) {
		t.Fatalf("expected error containing %q, got:\n%v", want, errs)
	}
	return errs
}

func TestValidProgramEncoding(t *testing.T) {
	res := compile(t, `
		# double the input
		MOV P0, ACC
		ADD ACC     ; comment styles
		MOV ACC P1  # comma optional
		JMP 0
	`, std, 32)
	want := []byte{
		vm.OpMOV, 0x10, vm.RegACC,
		vm.OpADD, vm.RegACC,
		vm.OpMOV, vm.RegACC, 0x11,
		vm.OpJMP, 0x00,
	}
	if !bytes.Equal(res.Program.Code, want) {
		t.Fatalf("code = % x, want % x", res.Program.Code, want)
	}
	if res.Program.Size() != 10 || len(res.Instructions) != 4 {
		t.Fatalf("size %d, %d instructions", res.Program.Size(), len(res.Instructions))
	}
	if res.SourceLines[0] != 3 || res.SourceLines[3] != 6 {
		t.Errorf("source lines = %v", res.SourceLines)
	}
	if res.Program.Version != vm.BytecodeVersion || res.Program.Arch != "standard" {
		t.Errorf("program header = %+v", res.Program)
	}
}

func TestCaseInsensitivity(t *testing.T) {
	a := compile(t, "loop: mov p0, acc\njmp LOOP", std, 0)
	b := compile(t, "LOOP: MOV P0, ACC\nJMP loop", std, 0)
	if !bytes.Equal(a.Program.Code, b.Program.Code) {
		t.Error("case should not matter")
	}
}

func TestLabels(t *testing.T) {
	res := compile(t, `
	START: A: MOV P0, ACC
	       JGZ OUT
	       JMP START
	OUT:   MOV ACC, P1
	END:`, std, 0)
	if res.Labels["START"] != 0 || res.Labels["A"] != 0 || res.Labels["OUT"] != 3 {
		t.Errorf("labels = %v", res.Labels)
	}
	if res.Labels["END"] != 0 {
		t.Errorf("trailing label should wrap to 0, got %d", res.Labels["END"])
	}
	// JGZ OUT encodes the byte address of instruction 3 (3+2+2 = 7).
	if res.Instructions[1].Operands[0].Target != 7 || res.Instructions[1].Operands[0].TargetIndex != 3 {
		t.Errorf("JGZ target = %+v", res.Instructions[1].Operands[0])
	}
}

func TestNumericTargetsAreInstructionIndices(t *testing.T) {
	res := compile(t, "MOV 1000, ACC\nNOP\nJMP 1\nJMP 4", std, 0)
	// MOV 1000, ACC = 1 + (1+2) + 1 = 5 bytes, so instruction 1 is at byte 5.
	if res.Instructions[2].Operands[0].Target != 5 {
		t.Errorf("JMP 1 target = %d", res.Instructions[2].Operands[0].Target)
	}
	// JMP <count> wraps to 0.
	if res.Instructions[3].Operands[0].TargetIndex != 0 {
		t.Errorf("JMP 4 should wrap to 0")
	}
	compileErr(t, "JMP 5", std, 0, "out of range")
	compileErr(t, "JMP -1", std, 0, "out of range")
}

func TestLongJumpLayoutConverges(t *testing.T) {
	// More than 127 bytes forces two-byte target encodings.
	var b strings.Builder
	b.WriteString("TOP: NOP\n")
	for i := 0; i < 70; i++ {
		b.WriteString("ADD 1000\n") // 4 bytes each
	}
	b.WriteString("JMP TOP\nJMP END\nEND: JMP TOP\n")
	res := compile(t, b.String(), std, 0)
	n := len(res.Instructions)
	if res.Instructions[n-2].Operands[0].TargetIndex != n-1 {
		t.Error("forward jump resolved incorrectly")
	}
	if int(res.Instructions[n-2].Operands[0].Target) != int(res.Instructions[n-1].Offset) {
		t.Errorf("forward target %d != offset %d", res.Instructions[n-2].Operands[0].Target, res.Instructions[n-1].Offset)
	}
}

func TestInvalidInstructions(t *testing.T) {
	compileErr(t, "MUL 2", std, 0, "unknown instruction MUL")
	compileErr(t, "SAV", stk, 0, "unknown instruction SAV")
	compileErr(t, "PUSH 1", std, 0, "unknown instruction PUSH")
	compileErr(t, "MOV P0", std, 0, "expects 2 operand")
	compileErr(t, "NEG ACC", std, 0, "expects 0 operand")
}

func TestOperandValidation(t *testing.T) {
	compileErr(t, "MOV 1, 2", std, 0, "cannot write to integer literal")
	compileErr(t, "MOV P0, P9", std, 0, `"P9" is not a valid destination`)
	compileErr(t, "ADD BAK", std, 0, `"BAK" is not a valid source`)
	compileErr(t, "JMP NOWHERE", std, 0, "undefined label")
	compileErr(t, "A: NOP\nA: NOP", std, 0, "defined more than once")
	compileErr(t, "ACC: NOP", std, 0, "collides with a register")
	compileErr(t, "ANY: NOP", std, 0, "collides with a port")
	compile(t, "MOV NIL, ANY\nMOV LAST, NIL", std, 0)
}

func TestSyntaxErrors(t *testing.T) {
	errs := compileErr(t, "MOV P0,, ACC\nADD @\nMOV P0, ACC,\n5 NOP", std, 0, "unexpected ','")
	if len(errs) != 4 {
		t.Errorf("want 4 errors, got %d:\n%v", len(errs), errs)
	}
	if errs[1].Pos.Line != 2 || errs[1].Pos.Column != 5 {
		t.Errorf("position = %v", errs[1].Pos)
	}
	compileErr(t, "MOV P0: ACC", std, 0, "unexpected ':'")
	compileErr(t, "L: :", std, 0, "expected instruction mnemonic")
}

func TestIntegerLiterals(t *testing.T) {
	cases := map[string]int32{
		"0": 0, "-0": 0, "+5": 5, "010": 10, "0x10": 16, "-0x10": -16, "0b101": 5, "0o17": 15,
		"2147483647": 2147483647, "-2147483648": -2147483648, "0x7FFFFFFF": 2147483647,
	}
	for s, want := range cases {
		got, err := asm.ParseInteger(s)
		if err != nil || got != want {
			t.Errorf("ParseInteger(%q) = %d, %v; want %d", s, got, err, want)
		}
	}
	for _, s := range []string{"2147483648", "-2147483649", "0xFFFFFFFF", "99999999999999999999", "0x", "12abc", "1_000"} {
		if _, err := asm.ParseInteger(s); err == nil {
			t.Errorf("ParseInteger(%q) should fail", s)
		}
	}
	compileErr(t, "ADD 2147483648", std, 0, "outside the 32-bit range")
	res := compile(t, "ADD -2147483648\nADD 2147483647", std, 0)
	if res.Instructions[0].Operands[0].Value != vm.MinWord || res.Instructions[1].Operands[0].Value != vm.MaxWord {
		t.Error("boundary literals decoded incorrectly")
	}
}

func TestProgramMemoryLimit(t *testing.T) {
	src := "MOV P0, ACC\nADD ACC\nMOV ACC, P1\nJMP 0" // 10 bytes
	compile(t, src, std, 10)
	errs := compileErr(t, src, std, 9, "program is 10 bytes but this cell has 9 bytes")
	if errs[0].Pos.Line != 4 {
		t.Errorf("limit error should point at the crossing instruction (line 4), got %v", errs[0].Pos)
	}
	compile(t, src, std, 0) // unlimited
	// Large immediates cost more bytes than small ones.
	small := compile(t, "ADD 63", std, 0)
	large := compile(t, "ADD 64", std, 0)
	if small.Program.Size() != 2 || large.Program.Size() != 4 {
		t.Errorf("immediate sizes: small %d, large %d", small.Program.Size(), large.Program.Size())
	}
}

func TestStackInstructionsAreCompact(t *testing.T) {
	res := compile(t, "PADD\nDUP\nSWAP\nDROP\nDEPTH\nPUSH 1\nPOP P0\nJSE 0", stk, 0)
	if res.Program.Size() != 1+1+1+1+1+2+2+2 {
		t.Errorf("stack program size = %d", res.Program.Size())
	}
}

func TestEmptyProgram(t *testing.T) {
	res := compile(t, "# nothing\n\n", std, 4)
	if res.Program.Size() != 0 || len(res.Instructions) != 0 {
		t.Error("empty program should compile to zero bytes")
	}
}
