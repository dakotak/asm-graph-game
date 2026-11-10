// Package multiplier is an example third-party cell architecture: an
// accumulator processor with a one-instruction multiplier (MUL). It shows
// how a plugin reuses core-instruction semantics (cells.ExecCore), adds an
// architecture-specific opcode, and registers itself.
//
// Link it statically with plugins.Static{ID: "multiplier", Register: multiplier.Register},
// or build the native plugin in ./plugin with:
//
//	go build -buildmode=plugin -o multiplier.so ./examples/plugins/multiplier/plugin
package multiplier

import (
	"fmt"

	"asmgraph/cell"
	"asmgraph/cells"
	"asmgraph/vm"
)

// Type is the cell type identifier used in puzzle files.
const Type = "multiplier"

// OpMUL is the architecture-specific opcode for MUL (plugin range 0x40+).
const OpMUL byte = 0x40

// Arch is the multiplier architecture.
type Arch struct{ isa *vm.ISA }

// New builds the architecture.
func New() *Arch {
	instrs := vm.Core("NOP", "MOV", "ADD", "SUB", "NEG", "JMP", "JEZ", "JNZ", "JGZ", "JLZ", "HLT")
	instrs = append(instrs, vm.InstructionDef{
		Opcode: OpMUL, Mnemonic: "MUL", Slots: []vm.SlotKind{vm.SlotSource},
		Summary: "ACC = ACC * <source> (wrapping, low 32 bits).",
	})
	isa, err := vm.NewISA(Type, instrs, []vm.RegisterDef{{Code: vm.RegACC, Name: "ACC", Summary: "Accumulator."}})
	if err != nil {
		panic(err)
	}
	return &Arch{isa: isa}
}

// Register adds the architecture to a registry.
func Register(r *cell.Registry) error { return r.Register(New()) }

func (*Arch) Type() string { return Type }
func (*Arch) Description() string {
	return "Accumulator processor with a single-instruction multiplier (MUL); no BAK."
}
func (a *Arch) ISA() *vm.ISA { return a.isa }
func (*Arch) ConfigSchema() []cell.ConfigField {
	return nil
}
func (*Arch) ValidateConfig(cfg map[string]any) []cell.ConfigError {
	return cell.ValidateConfig(nil, cfg)
}
func (*Arch) New(cell.Spec, cell.Env) (cell.Instance, error) { return &instance{}, nil }

type instance struct{ acc vm.Word }

func (c *instance) Inspect() map[string]any { return map[string]any{"ACC": c.acc} }

func (c *instance) AppendState(b []byte) []byte {
	u := uint32(c.acc)
	return append(b, byte(u>>24), byte(u>>16), byte(u>>8), byte(u))
}

func (c *instance) ReadRegister(code uint8) (vm.Word, error) {
	if code == vm.RegACC {
		return c.acc, nil
	}
	return 0, fmt.Errorf("unknown register 0x%02X", code)
}

func (c *instance) WriteRegister(code uint8, v vm.Word) error {
	if code == vm.RegACC {
		c.acc = v
		return nil
	}
	return fmt.Errorf("unknown register 0x%02X", code)
}

func (c *instance) Execute(ins *vm.Instruction, src []vm.Word) cell.Outcome {
	if out, ok := cells.ExecCore(ins, src, cells.CoreRegs{ACC: &c.acc}); ok {
		return out
	}
	if ins.Def.Opcode == OpMUL {
		c.acc *= src[0] // int32 multiplication wraps (two's complement)
		return cell.Outcome{}
	}
	return cell.Outcome{Err: fmt.Errorf("unsupported instruction %s", ins.Def.Mnemonic)}
}
