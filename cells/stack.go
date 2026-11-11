package cells

import (
	"encoding/binary"
	"fmt"

	"asmgraph/cell"
	"asmgraph/vm"
)

// StackType is the type identifier of the stack processor.
const StackType = "stack"

// Stack processor opcodes (architecture-specific range 0x40+).
const (
	OpPUSH  byte = 0x40
	OpPOP   byte = 0x41
	OpDUP   byte = 0x42
	OpDROP  byte = 0x43
	OpSWAP  byte = 0x44
	OpJSE   byte = 0x45
	OpPADD  byte = 0x46
	OpDEPTH byte = 0x47
)

// DefaultStackSize is the stack capacity when the puzzle does not set one.
const DefaultStackSize = 16

// MaxStackSize bounds the configurable stack capacity.
const MaxStackSize = 256

var stackSchema = []cell.ConfigField{{
	Name: "stack_size", Type: cell.FieldInt, Default: DefaultStackSize,
	Min: cell.Int64(1), Max: cell.Int64(MaxStackSize),
	Description: "Maximum number of values on the stack.",
}}

// Stack is a processor with ACC and a bounded LIFO stack instead of BAK.
// It trades SAV/SWP for stack instructions, including compact one-byte
// operations (PADD, DUP, SWAP) and a stack-empty branch (JSE).
type Stack struct{ isa *vm.ISA }

// NewStack builds the stack architecture.
func NewStack() *Stack {
	instrs := vm.Core("NOP", "MOV", "ADD", "SUB", "NEG", "JMP", "JEZ", "JNZ", "JGZ", "JLZ", "HLT")
	instrs = append(instrs,
		vm.InstructionDef{Opcode: OpPUSH, Mnemonic: "PUSH", Slots: []vm.SlotKind{vm.SlotSource}, Summary: "Push <source> onto the stack. Overflow is a runtime error."},
		vm.InstructionDef{Opcode: OpPOP, Mnemonic: "POP", Slots: []vm.SlotKind{vm.SlotDest}, Summary: "Pop the top of the stack into <destination>. Underflow is a runtime error."},
		vm.InstructionDef{Opcode: OpDUP, Mnemonic: "DUP", Summary: "Push a copy of the top of the stack."},
		vm.InstructionDef{Opcode: OpDROP, Mnemonic: "DROP", Summary: "Discard the top of the stack."},
		vm.InstructionDef{Opcode: OpSWAP, Mnemonic: "SWAP", Summary: "Exchange the top two stack values."},
		vm.InstructionDef{Opcode: OpJSE, Mnemonic: "JSE", Slots: []vm.SlotKind{vm.SlotTarget}, Summary: "Jump to <target> if the stack is empty."},
		vm.InstructionDef{Opcode: OpPADD, Mnemonic: "PADD", Summary: "Pop the top of the stack and add it to ACC (wrapping)."},
		vm.InstructionDef{Opcode: OpDEPTH, Mnemonic: "DEPTH", Summary: "ACC = number of values on the stack."},
	)
	isa, err := vm.NewISA(StackType, instrs, []vm.RegisterDef{
		{Code: vm.RegACC, Name: "ACC", Summary: "Accumulator."},
	})
	if err != nil {
		panic(err)
	}
	return &Stack{isa: isa}
}

func (*Stack) Type() string { return StackType }
func (*Stack) Description() string {
	return "Processor with ACC and a bounded stack (PUSH/POP/DUP/DROP/SWAP/PADD/DEPTH/JSE); no BAK."
}
func (s *Stack) ISA() *vm.ISA                   { return s.isa }
func (*Stack) StateFields() []cell.StateField {
	return []cell.StateField{
		{Name: "ACC", Kind: "word", Summary: "Accumulator."},
		{Name: "stack", Kind: "list", Summary: "LIFO stack; the last element is the top."},
	}
}
func (*Stack) ConfigSchema() []cell.ConfigField { return stackSchema }
func (*Stack) ValidateConfig(cfg map[string]any) []cell.ConfigError {
	return configErrors(stackSchema, cfg)
}

func (*Stack) New(spec cell.Spec, _ cell.Env) (cell.Instance, error) {
	n := cell.ConfigInt(spec.Config, "stack_size", DefaultStackSize)
	if n < 1 || n > MaxStackSize {
		return nil, fmt.Errorf("stack_size %d out of range", n)
	}
	return &stackCell{capacity: int(n), stack: make([]vm.Word, 0, n)}, nil
}

type stackCell struct {
	acc      vm.Word
	stack    []vm.Word
	capacity int
}

func (c *stackCell) Inspect() map[string]any {
	return map[string]any{
		"ACC":      c.acc,
		"stack":    append([]vm.Word{}, c.stack...),
		"capacity": c.capacity,
	}
}

func (c *stackCell) AppendState(b []byte) []byte {
	b = appendWord(b, c.acc)
	b = binary.AppendUvarint(b, uint64(len(c.stack)))
	for _, v := range c.stack {
		b = appendWord(b, v)
	}
	return b
}

func (c *stackCell) ReadRegister(code uint8) (vm.Word, error) {
	if code == vm.RegACC {
		return c.acc, nil
	}
	return 0, fmt.Errorf("unknown register 0x%02X", code)
}

func (c *stackCell) WriteRegister(code uint8, v vm.Word) error {
	if code == vm.RegACC {
		c.acc = v
		return nil
	}
	return fmt.Errorf("unknown register 0x%02X", code)
}

func (c *stackCell) push(v vm.Word) error {
	if len(c.stack) >= c.capacity {
		return fmt.Errorf("stack overflow (capacity %d)", c.capacity)
	}
	c.stack = append(c.stack, v)
	return nil
}

func (c *stackCell) pop() (vm.Word, error) {
	if len(c.stack) == 0 {
		return 0, fmt.Errorf("stack underflow")
	}
	v := c.stack[len(c.stack)-1]
	c.stack = c.stack[:len(c.stack)-1]
	return v, nil
}

func (c *stackCell) Execute(ins *vm.Instruction, src []vm.Word) cell.Outcome {
	if out, ok := ExecCore(ins, src, CoreRegs{ACC: &c.acc}); ok {
		return out
	}
	var out cell.Outcome
	switch ins.Def.Opcode {
	case OpPUSH:
		out.Err = c.push(src[0])
	case OpPOP:
		v, err := c.pop()
		out.Output, out.HasOutput, out.Err = v, true, err
	case OpDUP:
		if len(c.stack) == 0 {
			out.Err = fmt.Errorf("DUP on empty stack")
		} else {
			out.Err = c.push(c.stack[len(c.stack)-1])
		}
	case OpDROP:
		_, out.Err = c.pop()
	case OpSWAP:
		n := len(c.stack)
		if n < 2 {
			out.Err = fmt.Errorf("SWAP needs two stack values, have %d", n)
		} else {
			c.stack[n-1], c.stack[n-2] = c.stack[n-2], c.stack[n-1]
		}
	case OpJSE:
		if len(c.stack) == 0 {
			out.Jump, out.Target = true, ins.Operands[0].TargetIndex
		}
	case OpPADD:
		v, err := c.pop()
		if err == nil {
			c.acc = vm.Add(c.acc, v)
		}
		out.Err = err
	case OpDEPTH:
		c.acc = vm.Word(len(c.stack))
	default:
		out.Err = fmt.Errorf("unsupported instruction %s", ins.Def.Mnemonic)
	}
	return out
}
