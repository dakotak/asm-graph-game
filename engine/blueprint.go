// Package engine is the deterministic, tick-based simulator. It turns a
// validated puzzle and solution into an immutable Blueprint, creates a
// fresh Machine per test case, and owns all scheduling, port
// communication, completion, deadlock, and cycle detection.
package engine

import (
	"encoding/hex"
	"fmt"

	"asmgraph/asm"
	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/graph"
	"asmgraph/puzzle"
	"asmgraph/solution"
	"asmgraph/vm"
)

// CellBlueprint is the immutable, compiled description of one cell.
type CellBlueprint struct {
	Index        int
	ID           string
	Type         string
	Arch         cell.Architecture
	Spec         cell.Spec
	Hardware     cell.HardwareSpec
	Programmable bool
	// Player is true for hardware the player instantiated (as opposed to
	// the puzzle's fixed cells); only player hardware has a cost.
	Player      bool
	Cost        int
	Source      string
	Program     *vm.Program      // nil for non-programmable cells
	Code        []vm.Instruction // decoded program
	SourceLines []int            // source line per instruction
}

// Blueprint is a validated puzzle + solution with compiled programs. It is
// immutable and safe to share between machines and goroutines.
type Blueprint struct {
	Puzzle   *puzzle.Puzzle
	Solution *solution.Solution
	Topology *graph.Topology
	Cells    []*CellBlueprint
	// Hardware is the player's hardware and its cost.
	Hardware solution.HardwareReport
}

// Prepare validates the puzzle and solution, builds the topology, and
// compiles every program against its cell's architecture and memory limit.
// It reports all problems it can find in one pass.
func Prepare(reg *cell.Registry, p *puzzle.Puzzle, s *solution.Solution) (*Blueprint, diag.List) {
	if l := p.Validate(reg); l.HasErrors() {
		return nil, l
	}
	l := s.Validate(p, reg)
	if l.HasErrors() {
		// Topology and compilation need a structurally valid solution, but
		// keep going far enough to also report assembly errors.
		if hasCode(l, "solution.hardware") {
			return nil, s.Annotate(l)
		}
	}
	topo, _ := s.Topology(p, reg)
	rep := s.Report(p, reg)
	bp := &Blueprint{Puzzle: p, Solution: s, Topology: topo, Hardware: rep}
	costs := map[string]int{}
	for _, n := range rep.Nodes {
		costs[n.ID] = n.Cost
	}
	for i, spec := range s.Specs(p, reg) {
		arch, _ := reg.Lookup(spec.Type)
		cb := &CellBlueprint{
			Index: i, ID: spec.ID, Type: spec.Type, Arch: arch, Spec: spec,
			Hardware: cell.HardwareOf(arch), Player: i >= len(p.Cells), Cost: costs[spec.ID],
		}
		if isa := arch.ISA(); isa != nil {
			cb.Programmable = true
			cb.Source = s.Programs[spec.ID]
			res, errs := asm.Compile(cb.Source, isa, asm.Options{MemoryLimit: spec.ProgramMemory, Ports: spec.PortCount})
			for _, e := range errs {
				code := "asm.error"
				l = append(l, diag.Diagnostic{
					Severity: diag.Error, Code: code, Cell: spec.ID, Path: "programs." + spec.ID,
					SourceLine: e.Pos.Line, SourceColumn: e.Pos.Column, Message: e.Msg,
				})
			}
			if res != nil {
				cb.Program = &res.Program
				cb.Code = res.Instructions
				cb.SourceLines = res.SourceLines
			}
		}
		bp.Cells = append(bp.Cells, cb)
	}
	l = s.Annotate(l)
	if l.HasErrors() {
		return nil, l
	}
	return bp, l
}

func hasCode(l diag.List, prefix string) bool {
	for _, d := range l {
		if d.Severity == diag.Error && (d.Code == prefix || len(d.Code) > len(prefix) && d.Code[:len(prefix)+1] == prefix+".") {
			return true
		}
	}
	return false
}

// ProgramReport describes one compiled program for tooling and frontends.
type ProgramReport struct {
	Cell         string        `json:"cell"`
	Type         string        `json:"type"`
	Bytes        int           `json:"bytes"`
	Limit        int           `json:"limit"`
	Instructions int           `json:"instructions"`
	Hex          string        `json:"hex"`
	Disassembly  []DisasmEntry `json:"disassembly"`
}

// DisasmEntry is one disassembled instruction.
type DisasmEntry struct {
	Index      int    `json:"index"`
	Address    int    `json:"address"`
	Bytes      string `json:"bytes"`
	Text       string `json:"text"`
	SourceLine int    `json:"sourceLine"`
}

// Programs returns a report for every programmable cell, in puzzle order.
func (bp *Blueprint) Programs() []ProgramReport {
	var out []ProgramReport
	for _, c := range bp.Cells {
		if !c.Programmable {
			continue
		}
		r := ProgramReport{Cell: c.ID, Type: c.Type, Limit: c.Spec.ProgramMemory, Disassembly: []DisasmEntry{}}
		if c.Program != nil {
			r.Bytes = c.Program.Size()
			r.Hex = hex.EncodeToString(c.Program.Code)
			r.Instructions = len(c.Code)
			for i := range c.Code {
				in := &c.Code[i]
				r.Disassembly = append(r.Disassembly, DisasmEntry{
					Index: i, Address: int(in.Offset),
					Bytes:      hex.EncodeToString(c.Program.Code[in.Offset : int(in.Offset)+in.Size]),
					Text:       in.Format(c.Arch.ISA()),
					SourceLine: c.SourceLines[i],
				})
			}
		}
		out = append(out, r)
	}
	return out
}

// TotalProgramBytes sums program sizes across all cells.
func (bp *Blueprint) TotalProgramBytes() int {
	n := 0
	for _, c := range bp.Cells {
		if c.Program != nil {
			n += c.Program.Size()
		}
	}
	return n
}

// TestIndex resolves a test by name or decimal index string.
func (bp *Blueprint) TestIndex(sel string) (int, error) {
	if sel == "" {
		return 0, nil
	}
	if i, ok := bp.Puzzle.TestIndex(sel); ok {
		return i, nil
	}
	var n int
	if _, err := fmt.Sscanf(sel, "%d", &n); err == nil && fmt.Sprint(n) == sel && n >= 0 && n < len(bp.Puzzle.Tests) {
		return n, nil
	}
	return 0, fmt.Errorf("puzzle %s has no test %q", bp.Puzzle.ID, sel)
}
