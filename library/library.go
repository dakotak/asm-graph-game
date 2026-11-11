// Package library summarises puzzle files for puzzle browsers. It is shared
// by the HTTP server (puzzles from a directory) and the WebAssembly engine
// (puzzles bundled with the web IDE or imported by the player), so both
// describe puzzles identically.
package library

import (
	"sort"
	"strings"

	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/puzzle"
)

// MaxPuzzleBytes bounds the size of a puzzle file that is summarised.
const MaxPuzzleBytes = 4 << 20

// Summary describes one puzzle for a puzzle browser.
type Summary struct {
	ID                string    `json:"id"`
	Name              string    `json:"name"`
	Description       string    `json:"description"`
	File              string    `json:"file"`
	CellTypes         []string  `json:"cellTypes"`
	Cells             int       `json:"cells"`
	ProgrammableCells int       `json:"programmableCells"`
	Tests             int       `json:"tests"`
	Difficulty        any       `json:"difficulty,omitempty"`
	Valid             bool      `json:"valid"`
	Diagnostics       diag.List `json:"diagnostics,omitempty"`
	// Solutions is the number of saved solutions (filled in by the host).
	Solutions int `json:"solutions"`
	// Hardware is true when players build their own hardware.
	Hardware bool `json:"hardware"`
	// Bests are the personal bests per dimension (filled in by the host).
	Bests any `json:"bests,omitempty"`
}

// Summarize parses and validates one puzzle file. The returned puzzle is
// nil when the file does not parse; the summary then carries the parse
// diagnostics and an id derived from the file name.
func Summarize(reg *cell.Registry, file string, data []byte) (Summary, *puzzle.Puzzle) {
	sum := Summary{File: file, CellTypes: []string{}}
	if len(data) > MaxPuzzleBytes {
		sum.ID = baseName(file)
		sum.Name = sum.ID
		sum.Diagnostics = diag.List{{Severity: diag.Error, Code: "puzzle.size", File: file, Message: "puzzle file is larger than 4 MiB"}}
		return sum, nil
	}
	p, l := puzzle.Parse(data, file)
	if l.HasErrors() {
		sum.ID = baseName(file)
		sum.Name = sum.ID
		sum.Diagnostics = l
		return sum, nil
	}
	l = p.Validate(reg)
	sum.ID, sum.Name, sum.Description = p.ID, p.Name, p.Description
	if sum.Name == "" {
		sum.Name = p.ID
	}
	sum.Valid = !l.HasErrors()
	sum.Diagnostics = l
	sum.Cells, sum.Tests = len(p.Cells), len(p.Tests)
	types := map[string]bool{}
	for _, c := range p.Cells {
		types[c.Type] = true
		if a, ok := reg.Lookup(c.Type); ok && a.ISA() != nil {
			sum.ProgrammableCells++
		}
	}
	for t := range types {
		sum.CellTypes = append(sum.CellTypes, t)
	}
	sort.Strings(sum.CellTypes)
	if p.Metadata != nil {
		sum.Difficulty = p.Metadata["difficulty"]
	}
	sum.Hardware = p.AllowsHardware()
	return sum, p
}

func baseName(file string) string {
	if i := strings.LastIndexAny(file, "/\\"); i >= 0 {
		file = file[i+1:]
	}
	return strings.TrimSuffix(strings.TrimSuffix(file, ".yaml"), ".yml")
}
