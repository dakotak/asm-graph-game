package asm

import (
	"fmt"
	"strings"

	"asmgraph/vm"
)

// Options controls compilation.
type Options struct {
	// MemoryLimit is the program-memory budget in bytes. Zero or negative
	// means unlimited (used by tooling such as `asmgraph compile --nolimit`).
	MemoryLimit int
	// Ports is the cell's configured port count. When positive, physical
	// port operands must name one of P0..P(Ports-1). Zero accepts any port
	// the encoding supports (P0..P63).
	Ports int
}

// Result is a successfully compiled program.
type Result struct {
	Program      vm.Program
	Instructions []vm.Instruction // decoded from Program.Code
	SourceLines  []int            // source line of each instruction
	Labels       map[string]int   // upper-cased label -> instruction index
}

// Resolver turns an operand token into an encoded operand for one slot
// kind. The table is keyed by slot kind so new operand categories can be
// added without changing the parser.
type Resolver func(c *Context, tok Token) (vm.Operand, error)

// Context is the information available to operand resolvers.
type Context struct {
	ISA    *vm.ISA
	Labels map[string]int
	Count  int // number of instructions in the program
	Ports  int // configured port count; 0 = unchecked
}

var resolvers = map[vm.SlotKind]Resolver{
	vm.SlotSource: resolveSource,
	vm.SlotDest:   resolveDest,
	vm.SlotTarget: resolveTarget,
}

// Compile assembles src for the given instruction set.
func Compile(src string, isa *vm.ISA, opts Options) (*Result, ErrorList) {
	f, errs := Parse(src)
	if len(errs) > 0 {
		return nil, errs
	}
	ctx := &Context{ISA: isa, Labels: map[string]int{}, Count: len(f.Statements), Ports: opts.Ports}
	define := func(l Token, idx int) {
		key := strings.ToUpper(l.Text)
		if _, ok := vm.ParsePort(key); ok {
			errs.add(l.Pos, "label %q collides with a port name", l.Text)
			return
		}
		if _, ok := isa.Register(key); ok {
			errs.add(l.Pos, "label %q collides with a register name", l.Text)
			return
		}
		if _, dup := ctx.Labels[key]; dup {
			errs.add(l.Pos, "label %q is defined more than once", l.Text)
			return
		}
		ctx.Labels[key] = idx
	}
	for i, st := range f.Statements {
		for _, l := range st.Labels {
			define(l, i)
		}
	}
	for _, l := range f.TrailingLabels {
		define(l, 0)
	}

	type item struct {
		def *vm.InstructionDef
		ops []vm.Operand
		pos Pos
	}
	items := make([]item, 0, len(f.Statements))
	for _, st := range f.Statements {
		def, ok := isa.Lookup(st.Mnemonic.Text)
		if !ok {
			errs.add(st.Mnemonic.Pos, "unknown instruction %s for cell type %s (supported: %s)",
				st.Mnemonic.Text, isa.Name(), mnemonicList(isa))
			continue
		}
		if len(st.Operands) != len(def.Slots) {
			errs.add(st.Mnemonic.Pos, "%s expects %d operand(s) (%s), got %d",
				def.Mnemonic, len(def.Slots), slotList(def.Slots), len(st.Operands))
			continue
		}
		ops := make([]vm.Operand, len(def.Slots))
		okAll := true
		for k, slot := range def.Slots {
			op, err := resolvers[slot](ctx, st.Operands[k])
			if err != nil {
				errs.add(st.Operands[k].Pos, "%s %s operand: %v", def.Mnemonic, slot, err)
				okAll = false
				continue
			}
			ops[k] = op
		}
		if okAll {
			items = append(items, item{def, ops, st.Mnemonic.Pos})
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}

	// Layout. Jump targets are variable-length byte addresses, so iterate
	// until addresses are stable. Sizes only grow, so this converges.
	n := len(items)
	addrs := make([]uint32, n)
	for iter := 0; ; iter++ {
		var a uint32
		changed := false
		for i := range items {
			if addrs[i] != a {
				addrs[i] = a
				changed = true
			}
			for k := range items[i].ops {
				if items[i].ops[k].Kind == vm.OperandTarget {
					items[i].ops[k].Target = addrs[items[i].ops[k].TargetIndex]
				}
			}
			a += uint32(vm.InstructionSize(items[i].ops))
		}
		// Re-run once more to propagate target addresses computed this pass.
		if !changed && iter > 0 {
			break
		}
		if iter > 64 {
			errs.add(Pos{1, 1}, "internal error: layout did not converge")
			return nil, errs
		}
	}

	var code []byte
	lines := make([]int, n)
	for i, it := range items {
		var err error
		code, err = vm.EncodeInstruction(code, it.def, it.ops)
		if err != nil {
			errs.add(it.pos, "internal encoding error: %v", err)
			return nil, errs
		}
		lines[i] = it.pos.Line
	}
	if len(code) > vm.MaxProgramBytes {
		errs.add(Pos{1, 1}, "program is %d bytes, exceeding the absolute maximum of %d", len(code), vm.MaxProgramBytes)
		return nil, errs
	}
	if opts.MemoryLimit > 0 && len(code) > opts.MemoryLimit {
		at := items[0].pos
		for i, it := range items {
			if int(addrs[i])+vm.InstructionSize(it.ops) > opts.MemoryLimit {
				at = it.pos
				break
			}
		}
		errs.add(at, "program is %d bytes but this cell has %d bytes of program memory (this instruction crosses the limit)",
			len(code), opts.MemoryLimit)
		return nil, errs
	}
	decoded, err := vm.DecodeProgram(isa, code)
	if err != nil {
		errs.add(Pos{1, 1}, "internal error: compiled program failed to decode: %v", err)
		return nil, errs
	}
	return &Result{
		Program:      vm.Program{Version: vm.BytecodeVersion, Arch: isa.Name(), Code: code},
		Instructions: decoded,
		SourceLines:  lines,
		Labels:       ctx.Labels,
	}, nil
}

func resolveSource(c *Context, tok Token) (vm.Operand, error) {
	if tok.Kind == TokNumber {
		v, err := ParseInteger(tok.Text)
		if err != nil {
			return vm.Operand{}, err
		}
		return vm.Operand{Kind: vm.OperandImmediate, Value: v}, nil
	}
	if op, ok, err := resolveLocation(c, tok); ok {
		return op, err
	}
	return vm.Operand{}, fmt.Errorf("%q is not a valid source: expected a register (%s), a port (%s, ANY, LAST) or an integer literal",
		tok.Text, registerList(c.ISA), portList(c))
}

func resolveDest(c *Context, tok Token) (vm.Operand, error) {
	if tok.Kind == TokNumber {
		return vm.Operand{}, fmt.Errorf("cannot write to integer literal %s", tok.Text)
	}
	if op, ok, err := resolveLocation(c, tok); ok {
		return op, err
	}
	return vm.Operand{}, fmt.Errorf("%q is not a valid destination: expected a register (%s) or a port (%s, ANY, LAST)",
		tok.Text, registerList(c.ISA), portList(c))
}

func resolveLocation(c *Context, tok Token) (vm.Operand, bool, error) {
	if p, ok := vm.ParsePort(tok.Text); ok {
		if p.Physical() && c.Ports > 0 && p.Index() >= c.Ports {
			return vm.Operand{}, true, fmt.Errorf("port %s does not exist on this cell: it has %d port(s), %s", p, c.Ports, vm.PortRange(c.Ports))
		}
		return vm.Operand{Kind: vm.OperandPort, Port: p}, true, nil
	}
	if code, ok := c.ISA.Register(tok.Text); ok {
		return vm.Operand{Kind: vm.OperandRegister, Register: code}, true, nil
	}
	return vm.Operand{}, false, nil
}

func portList(c *Context) string {
	if c.Ports > 0 {
		return vm.PortRange(c.Ports)
	}
	return "P0, P1, ..."
}

func resolveTarget(c *Context, tok Token) (vm.Operand, error) {
	if tok.Kind == TokNumber {
		v, err := ParseInteger(tok.Text)
		if err != nil {
			return vm.Operand{}, err
		}
		if v < 0 || int(v) > c.Count {
			return vm.Operand{}, fmt.Errorf("instruction index %d out of range [0, %d]", v, c.Count)
		}
		idx := int(v)
		if idx == c.Count { // one past the end wraps to the start
			idx = 0
		}
		return vm.Operand{Kind: vm.OperandTarget, TargetIndex: idx}, nil
	}
	idx, ok := c.Labels[strings.ToUpper(tok.Text)]
	if !ok {
		return vm.Operand{}, fmt.Errorf("undefined label %q", tok.Text)
	}
	return vm.Operand{Kind: vm.OperandTarget, TargetIndex: idx}, nil
}

func mnemonicList(isa *vm.ISA) string {
	var names []string
	for _, d := range isa.Instructions() {
		names = append(names, d.Mnemonic)
	}
	return strings.Join(names, " ")
}

func registerList(isa *vm.ISA) string {
	var names []string
	for _, r := range isa.Registers() {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}

func slotList(slots []vm.SlotKind) string {
	if len(slots) == 0 {
		return "none"
	}
	parts := make([]string, len(slots))
	for i, s := range slots {
		parts[i] = s.String()
	}
	return strings.Join(parts, ", ")
}
