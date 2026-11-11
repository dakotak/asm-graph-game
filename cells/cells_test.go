package cells_test

import (
	"fmt"
	"strings"
	"testing"

	"asmgraph/asm"
	"asmgraph/cell"
	"asmgraph/cells"
	"asmgraph/vm"
)

// execAll runs a straight-line program (no ports) directly on an instance.
func execAll(t *testing.T, arch cell.Architecture, inst cell.Processor, src string) cell.Outcome {
	t.Helper()
	res, errs := asm.Compile(src, arch.ISA(), asm.Options{})
	if errs != nil {
		t.Fatal(errs)
	}
	var last cell.Outcome
	for i := range res.Instructions {
		ins := &res.Instructions[i]
		var srcVals []vm.Word
		for k, s := range ins.Def.Slots {
			if s != vm.SlotSource {
				continue
			}
			op := ins.Operands[k]
			switch op.Kind {
			case vm.OperandImmediate:
				srcVals = append(srcVals, op.Value)
			case vm.OperandRegister:
				v := vm.Word(0)
				if op.Register != vm.RegNIL {
					v, _ = inst.ReadRegister(op.Register)
				}
				srcVals = append(srcVals, v)
			default:
				t.Fatal("port operands are not supported by this helper")
			}
		}
		last = inst.Execute(ins, srcVals)
		if last.Err != nil {
			return last
		}
		if d := ins.Def.DestIndex(); d >= 0 && ins.Operands[d].Kind == vm.OperandRegister && ins.Operands[d].Register != vm.RegNIL {
			_ = inst.WriteRegister(ins.Operands[d].Register, last.Output)
		}
	}
	return last
}

func newProc(t *testing.T, arch cell.Architecture, cfg map[string]any) cell.Processor {
	t.Helper()
	inst, err := arch.New(cell.Spec{Config: cfg}, cell.Env{})
	if err != nil {
		t.Fatal(err)
	}
	return inst.(cell.Processor)
}

func TestStandardArithmeticAndRegisters(t *testing.T) {
	arch := cells.NewStandard()
	p := newProc(t, arch, nil)
	execAll(t, arch, p, "MOV 2147483647, ACC\nADD 1")
	if v, _ := p.ReadRegister(vm.RegACC); v != vm.MinWord {
		t.Errorf("overflow: ACC = %d", v)
	}
	execAll(t, arch, p, "MOV 10, ACC\nSAV\nSUB 15\nNEG\nSWP")
	st := p.Inspect()
	if st["ACC"] != vm.Word(10) || st["BAK"] != vm.Word(5) {
		t.Errorf("state = %v", st)
	}
	execAll(t, arch, p, "MOV -2147483648, ACC\nNEG")
	if v, _ := p.ReadRegister(vm.RegACC); v != vm.MinWord {
		t.Errorf("NEG MinWord = %d", v)
	}
	execAll(t, arch, p, "MOV -2147483648, ACC\nSUB 1")
	if v, _ := p.ReadRegister(vm.RegACC); v != vm.MaxWord {
		t.Errorf("MinWord - 1 = %d", v)
	}
}

func TestCoreBranching(t *testing.T) {
	arch := cells.NewStandard()
	isa := arch.ISA()
	cases := []struct {
		acc  vm.Word
		op   string
		jump bool
	}{
		{0, "JEZ", true}, {1, "JEZ", false},
		{0, "JNZ", false}, {-1, "JNZ", true},
		{5, "JGZ", true}, {0, "JGZ", false}, {-5, "JGZ", false},
		{-5, "JLZ", true}, {0, "JLZ", false},
	}
	for _, c := range cases {
		p := newProc(t, arch, nil)
		_ = p.WriteRegister(vm.RegACC, c.acc)
		res, _ := asm.Compile("NOP\n"+c.op+" 0", isa, asm.Options{})
		out := p.Execute(&res.Instructions[1], nil)
		if out.Jump != c.jump || (c.jump && out.Target != 0) {
			t.Errorf("%s with ACC=%d: jump=%v", c.op, c.acc, out.Jump)
		}
	}
	p := newProc(t, arch, nil)
	if out := execAll(t, arch, p, "HLT"); !out.Halt {
		t.Error("HLT should halt")
	}
}

func TestStackOperations(t *testing.T) {
	arch := cells.NewStackProcessor()
	p := newProc(t, arch, map[string]any{"stack_size": 4})
	execAll(t, arch, p, "PUSH 1\nPUSH 2\nPUSH 3\nSWAP\nDUP\nDEPTH")
	st := p.Inspect()
	if got := st["stack"].([]vm.Word); len(got) != 4 || got[0] != 1 || got[1] != 3 || got[2] != 2 || got[3] != 2 {
		t.Errorf("stack = %v", got)
	}
	if st["ACC"] != vm.Word(4) {
		t.Errorf("DEPTH -> ACC = %v", st["ACC"])
	}
	execAll(t, arch, p, "MOV 0, ACC\nPADD\nPADD\nDROP\nPOP ACC")
	if v, _ := p.ReadRegister(vm.RegACC); v != 1 {
		t.Errorf("after pops ACC = %d", v)
	}
	if out := execAll(t, arch, p, "POP NIL"); out.Err == nil || !strings.Contains(out.Err.Error(), "underflow") {
		t.Errorf("underflow error = %v", out.Err)
	}
	if out := execAll(t, arch, p, "PUSH 1\nPUSH 1\nPUSH 1\nPUSH 1\nPUSH 1"); out.Err == nil || !strings.Contains(out.Err.Error(), "overflow") {
		t.Errorf("overflow error = %v", out.Err)
	}
}

func TestStackJSE(t *testing.T) {
	arch := cells.NewStackProcessor()
	p := newProc(t, arch, nil)
	res, _ := asm.Compile("JSE 0", arch.ISA(), asm.Options{})
	if out := p.Execute(&res.Instructions[0], nil); !out.Jump {
		t.Error("JSE should jump on empty stack")
	}
	execAll(t, arch, p, "PUSH 1")
	if out := p.Execute(&res.Instructions[0], nil); out.Jump {
		t.Error("JSE should not jump on non-empty stack")
	}
}

func TestStackConfig(t *testing.T) {
	arch := cells.NewStackProcessor()
	if errs := arch.ValidateConfig(map[string]any{"stack_size": 0}); len(errs) == 0 {
		t.Error("stack_size 0 accepted")
	}
	if errs := arch.ValidateConfig(map[string]any{"stack_size": 1000}); len(errs) == 0 {
		t.Error("stack_size 1000 accepted")
	}
	if errs := arch.ValidateConfig(map[string]any{"bogus": 1}); len(errs) == 0 {
		t.Error("unknown field accepted")
	}
	if errs := arch.ValidateConfig(map[string]any{"stack_size": 8}); len(errs) != 0 {
		t.Error(errs)
	}
	p := newProc(t, arch, nil)
	if p.Inspect()["capacity"] != cells.DefaultStackSize {
		t.Error("default capacity not applied")
	}
}

func TestStreamCells(t *testing.T) {
	in, err := cells.Input{}.New(cell.Spec{Config: map[string]any{"stream": "s"}}, cell.Env{Inputs: map[string][]vm.Word{"s": {7, 8}}})
	if err != nil {
		t.Fatal(err)
	}
	dev := in.(cell.Device)
	for _, want := range []vm.Word{7, 8} {
		r := dev.Request()
		if r.Op != cell.RequestWrite || r.Port != vm.PortAny || r.Value != want {
			t.Fatalf("request = %+v", r)
		}
		dev.CommitWrite(vm.P0)
	}
	if dev.Request().Op != cell.RequestNone {
		t.Error("exhausted input must stop producing values")
	}
	if _, err := (cells.Input{}).New(cell.Spec{Config: map[string]any{"stream": "missing"}}, cell.Env{}); err == nil {
		t.Error("missing stream accepted")
	}
	if errs := (cells.Input{}).ValidateConfig(nil); len(errs) == 0 {
		t.Error("stream should be required")
	}
	out, _ := cells.Output{}.New(cell.Spec{Config: map[string]any{"stream": "o"}}, cell.Env{})
	od := out.(cell.Device)
	if od.Request().Op != cell.RequestRead {
		t.Error("output should read")
	}
	od.CommitRead(vm.P0, 5)
	if got := out.(cell.Sink).Values(); len(got) != 1 || got[0] != 5 {
		t.Errorf("received %v", got)
	}
}

func TestRegistry(t *testing.T) {
	r := cells.NewRegistry()
	if got := strings.Join(r.Types(), ","); got != "input,output,stack,stack_processor,standard" {
		t.Errorf("types = %s", got)
	}
	if err := r.Register(cells.NewStandard()); err == nil {
		t.Error("duplicate registration accepted")
	}
}

// Declared state fields must match what Inspect actually reports, since
// frontends lay out cells from the declaration before a run starts.
func TestStateFieldsMatchInspect(t *testing.T) {
	for _, arch := range []cell.Architecture{&cells.Standard{}, cells.NewStackProcessor()} {
		inst, err := arch.New(cell.Spec{Config: map[string]any{}}, cell.Env{})
		if err != nil {
			t.Fatal(err)
		}
		state := inst.Inspect()
		for _, f := range arch.(cell.StateDescriber).StateFields() {
			v, ok := state[f.Name]
			if !ok {
				t.Errorf("%s: declared field %q missing from Inspect", arch.Type(), f.Name)
				continue
			}
			if _, isList := v.([]vm.Word); isList != (f.Kind == "list") {
				t.Errorf("%s: field %q kind %q does not match %T", arch.Type(), f.Name, f.Kind, v)
			}
		}
	}
}

func TestPassiveStackOffers(t *testing.T) {
	inst, err := cells.Stack{}.New(cell.Spec{PortCount: 4, Ports: []bool{true, true, true, true},
		PortFunctions: []string{"PUSH", "pop", "PEEK", "SIZE"}, Config: map[string]any{"capacity": 2}}, cell.Env{})
	if err != nil {
		t.Fatal(err)
	}
	per := inst.(cell.Peripheral)
	offers := func() string {
		var parts []string
		for _, o := range per.Offers(nil) {
			parts = append(parts, fmt.Sprintf("%s:%s=%d", o.Port, o.Op, o.Value))
		}
		return strings.Join(parts, " ")
	}
	// Empty: only PUSH and SIZE are ready.
	if got := offers(); got != "P0:read=0 P3:write=0" {
		t.Errorf("empty: %s", got)
	}
	per.Commit(vm.P0, cell.RequestRead, 5)
	per.Commit(vm.P0, cell.RequestRead, 6)
	// Full: PUSH is withdrawn; POP and PEEK offer the top.
	if got := offers(); got != "P1:write=6 P2:write=6 P3:write=2" {
		t.Errorf("full: %s", got)
	}
	per.Commit(vm.P2, cell.RequestWrite, 6) // PEEK leaves the stack alone
	per.Commit(vm.P1, cell.RequestWrite, 6) // POP removes the top
	if st := inst.Inspect()["stack"].([]vm.Word); len(st) != 1 || st[0] != 5 {
		t.Errorf("stack after peek+pop: %v", st)
	}
	if inst.(cell.PortDescriber).PortFunction(vm.P1) != "POP" {
		t.Error("port functions are normalised to upper case")
	}
	// Default assignment when the player does not choose.
	inst, _ = cells.Stack{}.New(cell.Spec{PortCount: 6, Ports: make([]bool, 6)}, cell.Env{})
	if got := inst.(cell.PortDescriber).PortFunction(vm.PortN(5)); got != "POP" {
		t.Errorf("default P5 = %s", got)
	}
}

func TestHardwareSpecs(t *testing.T) {
	for _, a := range cells.Builtins() {
		if err := cell.CheckHardwareSpec(a); err != nil {
			t.Error(err)
		}
	}
	h := cell.HardwareOf(cells.Stack{})
	if h.Category != cell.CategoryPeripheral || !h.Ports.Configurable() || h.PortFunctions == nil {
		t.Errorf("stack spec %+v", h)
	}
	if h := cell.HardwareOf(cells.Input{}); !h.PuzzleOnly || h.Category != cell.CategoryIO {
		t.Errorf("input spec %+v", h)
	}
	// Cost: base + per port + properties rounded up.
	m := cell.CostModel{Base: 10, PerPort: 2, Properties: []cell.PropertyCost{{Property: "program_memory", Cost: 1, Per: 8}, {Property: "capacity", Cost: 3, Per: 4}}}
	if got := cell.Cost(m, cell.HardwareConfig{Ports: 6, ProgramMemory: 9, Config: map[string]any{"capacity": 4}}); got != 10+12+2+3 {
		t.Errorf("cost %d", got)
	}
	// The plugin's own limits.
	errs := cell.ValidateHardware(cells.Stack{}, cell.HardwareConfig{Ports: 2, PortFunctions: []string{"PUSH", "SHIFT"}})
	if len(errs) != 1 || errs[0].Field != "port_functions[1]" {
		t.Errorf("unknown function: %+v", errs)
	}
	errs = cell.ValidateHardware(cells.NewStandard(), cell.HardwareConfig{Ports: 4, ProgramMemory: 0})
	if len(errs) != 1 || errs[0].Field != "program_memory" {
		t.Errorf("program memory: %+v", errs)
	}
	errs = cell.ValidateHardware(cells.Stack{}, cell.HardwareConfig{Ports: 4, ProgramMemory: 8})
	if len(errs) != 1 || !strings.Contains(errs[0].Message, "not programmable") {
		t.Errorf("memory on a peripheral: %+v", errs)
	}
}

// Registration rejects hardware descriptions that are impossible.
type badPorts struct{ cells.Stack }

func (badPorts) Type() string { return "bad" }
func (b badPorts) Hardware() cell.HardwareSpec {
	h := b.Stack.Hardware()
	h.Ports = cell.PortSpec{Min: 2, Max: 100, Default: 4}
	return h
}

func TestRegistryChecksHardware(t *testing.T) {
	if err := cell.NewRegistry().Register(badPorts{}); err == nil || !strings.Contains(err.Error(), "invalid port range") {
		t.Errorf("got %v", err)
	}
}
