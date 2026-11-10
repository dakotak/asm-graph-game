// Package cells provides the built-in cell architectures: the standard
// accumulator processor, the stack processor, and the input and output
// stream cells. It also exports ExecCore, the single implementation of
// core-instruction semantics that every processor (including third-party
// plugins) should reuse so core instructions behave identically everywhere.
package cells

import (
	"errors"
	"fmt"

	"asmgraph/cell"
	"asmgraph/vm"
)

// CoreRegs exposes an architecture's accumulator registers to ExecCore.
// BAK may be nil for architectures without a backup register.
type CoreRegs struct {
	ACC *vm.Word
	BAK *vm.Word
}

// ExecCore executes a core instruction. It returns handled=false for
// non-core opcodes so the caller can dispatch plugin instructions.
func ExecCore(ins *vm.Instruction, src []vm.Word, r CoreRegs) (out cell.Outcome, handled bool) {
	needACC := func() bool {
		if r.ACC == nil {
			out.Err = fmt.Errorf("%s requires an ACC register", ins.Def.Mnemonic)
			return false
		}
		return true
	}
	jumpIf := func(cond func(vm.Word) bool) {
		if needACC() && cond(*r.ACC) {
			out.Jump, out.Target = true, ins.Operands[0].TargetIndex
		}
	}
	switch ins.Def.Opcode {
	case vm.OpNOP:
	case vm.OpMOV:
		out.Output, out.HasOutput = src[0], true
	case vm.OpADD:
		if needACC() {
			*r.ACC = vm.Add(*r.ACC, src[0])
		}
	case vm.OpSUB:
		if needACC() {
			*r.ACC = vm.Sub(*r.ACC, src[0])
		}
	case vm.OpNEG:
		if needACC() {
			*r.ACC = vm.Neg(*r.ACC)
		}
	case vm.OpSAV:
		if needACC() {
			if r.BAK == nil {
				out.Err = errors.New("SAV requires a BAK register")
			} else {
				*r.BAK = *r.ACC
			}
		}
	case vm.OpSWP:
		if needACC() {
			if r.BAK == nil {
				out.Err = errors.New("SWP requires a BAK register")
			} else {
				*r.ACC, *r.BAK = *r.BAK, *r.ACC
			}
		}
	case vm.OpJMP:
		out.Jump, out.Target = true, ins.Operands[0].TargetIndex
	case vm.OpJEZ:
		jumpIf(func(v vm.Word) bool { return v == 0 })
	case vm.OpJNZ:
		jumpIf(func(v vm.Word) bool { return v != 0 })
	case vm.OpJGZ:
		jumpIf(func(v vm.Word) bool { return v > 0 })
	case vm.OpJLZ:
		jumpIf(func(v vm.Word) bool { return v < 0 })
	case vm.OpHLT:
		out.Halt = true
	default:
		return cell.Outcome{}, false
	}
	return out, true
}

func appendWord(b []byte, v vm.Word) []byte {
	u := uint32(v)
	return append(b, byte(u>>24), byte(u>>16), byte(u>>8), byte(u))
}

func configErrors(schema []cell.ConfigField, cfg map[string]any) []cell.ConfigError {
	return cell.ValidateConfig(schema, cfg)
}
