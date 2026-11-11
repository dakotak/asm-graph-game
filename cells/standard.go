package cells

import (
	"fmt"

	"asmgraph/cell"
	"asmgraph/vm"
)

// StandardType is the type identifier of the standard processor.
const StandardType = "standard"

// Standard is a TIS-100-inspired accumulator processor with ACC and a
// non-addressable BAK register. It supports every core instruction.
type Standard struct{ isa *vm.ISA }

// NewStandard builds the standard architecture.
func NewStandard() *Standard {
	isa, err := vm.NewISA(StandardType, vm.CoreInstructions(), []vm.RegisterDef{
		{Code: vm.RegACC, Name: "ACC", Summary: "Accumulator."},
	})
	if err != nil {
		panic(err)
	}
	return &Standard{isa: isa}
}

func (*Standard) Type() string { return StandardType }
func (*Standard) Description() string {
	return "Accumulator processor with ACC and a backup register BAK (accessed only via SAV/SWP)."
}
func (s *Standard) ISA() *vm.ISA { return s.isa }
func (*Standard) StateFields() []cell.StateField {
	return []cell.StateField{
		{Name: "ACC", Kind: "word", Summary: "Accumulator."},
		{Name: "BAK", Kind: "word", Summary: "Backup register, accessed only via SAV and SWP."},
	}
}
func (*Standard) Hardware() cell.HardwareSpec {
	return cell.HardwareSpec{
		Category:      cell.CategoryProcessor,
		Ports:         cell.PortSpec{Min: 1, Max: 8, Default: 4},
		ProgramMemory: &cell.MemorySpec{Min: 1, Max: 1024, Default: 32},
		Cost: cell.CostModel{Base: 10, PerPort: 2, Properties: []cell.PropertyCost{
			{Property: "program_memory", Cost: 1, Per: 8},
		}},
		Overrides: cell.Overrides{Cost: true, Defaults: true},
	}
}
func (*Standard) ConfigSchema() []cell.ConfigField { return nil }
func (*Standard) ValidateConfig(cfg map[string]any) []cell.ConfigError {
	return configErrors(nil, cfg)
}
func (*Standard) New(cell.Spec, cell.Env) (cell.Instance, error) { return &standardCell{}, nil }

type standardCell struct{ acc, bak vm.Word }

func (c *standardCell) Inspect() map[string]any {
	return map[string]any{"ACC": c.acc, "BAK": c.bak}
}

func (c *standardCell) AppendState(b []byte) []byte {
	return appendWord(appendWord(b, c.acc), c.bak)
}

func (c *standardCell) ReadRegister(code uint8) (vm.Word, error) {
	if code == vm.RegACC {
		return c.acc, nil
	}
	return 0, fmt.Errorf("unknown register 0x%02X", code)
}

func (c *standardCell) WriteRegister(code uint8, v vm.Word) error {
	if code == vm.RegACC {
		c.acc = v
		return nil
	}
	return fmt.Errorf("unknown register 0x%02X", code)
}

func (c *standardCell) Execute(ins *vm.Instruction, src []vm.Word) cell.Outcome {
	if out, ok := ExecCore(ins, src, CoreRegs{ACC: &c.acc, BAK: &c.bak}); ok {
		return out
	}
	return cell.Outcome{Err: fmt.Errorf("unsupported instruction %s", ins.Def.Mnemonic)}
}
