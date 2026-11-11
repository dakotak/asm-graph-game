// Package solution loads and validates player solutions: the hardware the
// player instantiated (schema v2), connections, and assembly programs.
// Solutions are separate files and cannot redefine anything the puzzle
// author owns (fixed cells, their memory limits and ports, or tests).
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

// SchemaVersion is the newest solution file schema version. Version 2
// added the `hardware` list; version-1 files are still read.
const SchemaVersion = 2

// MinSchemaVersion is the oldest schema version still read.
const MinSchemaVersion = 1

// Solution is a player's answer to one puzzle.
type Solution struct {
	SchemaVersion int    `yaml:"schema_version" json:"schema_version"`
	Puzzle        string `yaml:"puzzle" json:"puzzle"`
	// Hardware lists the nodes the player instantiated (schema v2).
	Hardware    []Node             `yaml:"hardware,omitempty" json:"hardware,omitempty"`
	Connections []graph.Connection `yaml:"connections" json:"connections"`
	// Programs maps cell id to assembly source.
	Programs map[string]string `yaml:"programs" json:"programs"`
	// Metadata is free-form frontend presentation data (cell positions,
	// notes, ...). The engine never reads it.
	Metadata map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`

	file string
	root *yaml.Node
}

// Node is one player-instantiated hardware node.
type Node struct {
	ID   string `yaml:"id" json:"id"`
	Type string `yaml:"type" json:"type"`
	// PortCount defaults to the effective default for the type.
	PortCount int `yaml:"port_count,omitempty" json:"port_count,omitempty"`
	// ProgramMemory (processors) defaults to the effective default.
	ProgramMemory int `yaml:"program_memory,omitempty" json:"program_memory,omitempty"`
	// PortFunctions (peripherals) defaults to the type's default assignment.
	PortFunctions []string       `yaml:"port_functions,omitempty" json:"port_functions,omitempty"`
	Config        map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
	// Metadata is free-form presentation data; the engine never reads it.
	Metadata map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
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
	if s.SchemaVersion < MinSchemaVersion || s.SchemaVersion > SchemaVersion {
		l.Errorf("solution.schema_version", "schema_version", "unsupported schema_version %d (this build reads versions %d-%d)", s.SchemaVersion, MinSchemaVersion, SchemaVersion)
	}
	if s.SchemaVersion < 2 && s.Hardware != nil {
		l.Errorf("solution.schema_version", "hardware", "the hardware list needs schema_version: 2")
	}
	if s.Puzzle != p.ID {
		l.Errorf("solution.puzzle", "puzzle", "solution is for puzzle %q but the loaded puzzle is %q", s.Puzzle, p.ID)
	}
	l = append(l, s.validateHardware(p, reg)...)
	for _, id := range s.ProgramCells() {
		typ, ok := s.cellType(p, id)
		if !ok {
			l.Errorf("solution.program", "programs."+id, "there is no cell %q", id)
			continue
		}
		if arch, ok := reg.Lookup(typ); ok && arch.ISA() == nil {
			l.Errorf("solution.program", "programs."+id, "cell %q (type %s) is not programmable", id, typ)
		}
	}
	_, tl := s.Topology(p, reg)
	l = append(l, tl...)
	return s.Annotate(l)
}

func (s *Solution) cellType(p *puzzle.Puzzle, id string) (string, bool) {
	if i, ok := p.CellIndex(id); ok {
		return p.Cells[i].Type, true
	}
	for _, n := range s.Hardware {
		if n.ID == id {
			return n.Type, true
		}
	}
	return "", false
}

// Specs returns the engine specs of every cell: the puzzle's fixed cells
// in puzzle order, then the player's nodes in solution order. Unset node
// fields get their effective defaults. It assumes a validated solution
// (unknown node types are skipped).
func (s *Solution) Specs(p *puzzle.Puzzle, reg *cell.Registry) []cell.Spec {
	out := make([]cell.Spec, 0, len(p.Cells)+len(s.Hardware))
	for i := range p.Cells {
		out = append(out, p.CellSpec(reg, &p.Cells[i]))
	}
	for i := range s.Hardware {
		if sp, ok := s.NodeSpec(p, reg, i); ok {
			out = append(out, sp)
		}
	}
	return out
}

// NodeSpec builds the spec of player node i.
func (s *Solution) NodeSpec(p *puzzle.Puzzle, reg *cell.Registry, i int) (cell.Spec, bool) {
	n := &s.Hardware[i]
	hc := s.nodeConfig(p, reg, i)
	if _, ok := reg.Lookup(n.Type); !ok {
		return cell.Spec{}, false
	}
	return cell.Spec{
		ID: n.ID, Type: n.Type, ProgramMemory: hc.ProgramMemory,
		PortCount: hc.Ports, Ports: graph.AllPorts(hc.Ports), PortFunctions: hc.PortFunctions, Config: hc.Config,
	}, true
}

func (s *Solution) nodeConfig(p *puzzle.Puzzle, reg *cell.Registry, i int) cell.HardwareConfig {
	n := &s.Hardware[i]
	hc := cell.HardwareConfig{Ports: n.PortCount, PortFunctions: n.PortFunctions, ProgramMemory: n.ProgramMemory, Config: n.Config}
	if e, ok := p.EffectiveHardware(reg, n.Type); ok {
		return e.NodeDefaults(hc)
	}
	if a, ok := reg.Lookup(n.Type); ok {
		h := cell.HardwareOf(a)
		if hc.Ports == 0 {
			hc.Ports = h.Ports.Default
		}
		if hc.ProgramMemory == 0 && h.ProgramMemory != nil {
			hc.ProgramMemory = h.ProgramMemory.Default
		}
		hc.Config = cell.WithDefaults(a.ConfigSchema(), hc.Config)
	}
	return hc
}

// Topology builds the connection graph over every cell.
func (s *Solution) Topology(p *puzzle.Puzzle, reg *cell.Registry) (*graph.Topology, diag.List) {
	specs := s.Specs(p, reg)
	cells := make([]graph.CellPorts, len(specs))
	for i, sp := range specs {
		cells[i] = graph.CellPorts{ID: sp.ID, Enabled: sp.Ports}
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
