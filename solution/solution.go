// Package solution loads and validates player solutions: connections and
// assembly programs for a puzzle. Solutions are separate files and cannot
// redefine anything the puzzle author owns (cells, types, memory limits,
// ports, or tests).
package solution

import (
	"bytes"
	"fmt"
	"os"
	"sort"

	"gopkg.in/yaml.v3"

	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/graph"
	"asmgraph/internal/yamlpath"
	"asmgraph/puzzle"
)

// SchemaVersion is the solution file schema version this build reads.
const SchemaVersion = 1

// Solution is a player's answer to one puzzle.
type Solution struct {
	SchemaVersion int                `yaml:"schema_version" json:"schema_version"`
	Puzzle        string             `yaml:"puzzle" json:"puzzle"`
	Connections   []graph.Connection `yaml:"connections" json:"connections"`
	// Programs maps cell id to assembly source.
	Programs map[string]string `yaml:"programs" json:"programs"`
	// Metadata is free-form frontend presentation data (cell positions,
	// notes, ...). The engine never reads it.
	Metadata map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`

	file string
	root *yaml.Node
}

// File returns the path the solution was loaded from, if any.
func (s *Solution) File() string { return s.file }

// Marshal renders the solution as YAML (for frontends that save solutions).
func (s *Solution) Marshal() ([]byte, error) { return yaml.Marshal(s) }

// LoadFile reads and parses a solution file. It does not validate it.
func LoadFile(path string) (*Solution, diag.List) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, diag.List{{Severity: diag.Error, Code: "io", File: path, Message: err.Error()}}
	}
	return Parse(data, path)
}

// Parse decodes solution YAML. Unknown top-level fields (for example an
// attempt to declare "cells" or "tests") are errors.
func Parse(data []byte, file string) (*Solution, diag.List) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, yamlErrors(err, file)
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return nil, diag.List{{Severity: diag.Error, Code: "solution.yaml", File: file, Message: "solution file is empty"}}
	}
	s := &Solution{}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(s); err != nil {
		return nil, yamlErrors(err, file)
	}
	s.file, s.root = file, &root
	return s, nil
}

func yamlErrors(err error, file string) diag.List {
	var l diag.List
	for _, e := range yamlpath.SplitError(err) {
		l = append(l, diag.Diagnostic{Severity: diag.Error, Code: "solution.yaml", File: file, Line: e.Line, Message: e.Msg})
	}
	return l
}

// Validate checks the solution against a validated puzzle: schema, puzzle
// identity, program targets, and the connection topology. It does not
// compile programs (see engine.Prepare).
func (s *Solution) Validate(p *puzzle.Puzzle, reg *cell.Registry) diag.List {
	var l diag.List
	if s.SchemaVersion != SchemaVersion {
		l.Errorf("solution.schema_version", "schema_version", "unsupported schema_version %d (this build reads version %d)", s.SchemaVersion, SchemaVersion)
	}
	if s.Puzzle != p.ID {
		l.Errorf("solution.puzzle", "puzzle", "solution is for puzzle %q but the loaded puzzle is %q", s.Puzzle, p.ID)
	}
	for _, id := range s.ProgramCells() {
		i, ok := p.CellIndex(id)
		if !ok {
			l.Errorf("solution.program", "programs."+id, "puzzle has no cell %q", id)
			continue
		}
		if arch, ok := reg.Lookup(p.Cells[i].Type); ok && arch.ISA() == nil {
			l.Errorf("solution.program", "programs."+id, "cell %q (type %s) is not programmable", id, p.Cells[i].Type)
		}
	}
	_, tl := s.Topology(p)
	l = append(l, tl...)
	return s.Annotate(l)
}

// Topology builds the connection graph for the puzzle's cells.
func (s *Solution) Topology(p *puzzle.Puzzle) (*graph.Topology, diag.List) {
	cells := make([]graph.CellPorts, len(p.Cells))
	for i := range p.Cells {
		cells[i] = graph.CellPorts{ID: p.Cells[i].ID, Enabled: p.Cells[i].EnabledPorts()}
	}
	t, errs := graph.Build(cells, s.Connections)
	var l diag.List
	for _, e := range errs {
		l = append(l, diag.Diagnostic{Severity: diag.Error, Code: "solution.connection", Path: e.Path(), Message: e.Msg})
	}
	return t, s.Annotate(l)
}

// ProgramCells lists the cells that have programs, sorted.
func (s *Solution) ProgramCells() []string {
	ids := make([]string, 0, len(s.Programs))
	for id := range s.Programs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Annotate fills File/Line/Column of diagnostics that have a YAML path.
// For assembly diagnostics (SourceLine set) on a block-scalar program,
// it maps the program line to the file line.
func (s *Solution) Annotate(l diag.List) diag.List {
	for i := range l {
		d := &l[i]
		if d.File == "" {
			d.File = s.file
		}
		if d.Path == "" || d.Line != 0 || s.root == nil {
			continue
		}
		d.Line, d.Column = yamlpath.Locate(s.root, d.Path)
		if d.SourceLine > 0 && d.Line > 0 {
			if n := programNode(s.root, d.Cell); n != nil && (n.Style == yaml.LiteralStyle || n.Style == yaml.FoldedStyle) {
				d.Line = n.Line + d.SourceLine
				d.Column = 0
			}
		}
	}
	return l
}

func programNode(root *yaml.Node, cellID string) *yaml.Node {
	doc := root
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		doc = doc.Content[0]
	}
	progs := mapValue(doc, "programs")
	if progs == nil {
		return nil
	}
	return mapValue(progs, cellID)
}

func mapValue(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// String renders a short description for logs.
func (s *Solution) String() string {
	return fmt.Sprintf("solution for %s (%d connections, %d programs)", s.Puzzle, len(s.Connections), len(s.Programs))
}
