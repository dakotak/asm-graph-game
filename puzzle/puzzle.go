// Package puzzle loads and validates puzzle definitions. A puzzle and all
// of its test cases live in a single YAML file; nothing references
// external test files.
package puzzle

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/internal/yamlpath"
	"asmgraph/vm"
)

// SchemaVersion is the newest puzzle file schema version. Version 2 added
// player hardware (the `hardware` section), per-cell port counts, and
// port functions. Version-1 files are still read; see Migrate.
const SchemaVersion = 2

// MinSchemaVersion is the oldest schema version still read.
const MinSchemaVersion = 1

// LegacyTypes maps cell types renamed since schema version 1 to their
// current names. In v1, "stack" was the programmable stack processor; in
// v2 it is the passive stack peripheral.
var LegacyTypes = map[string]string{"stack": "stack_processor"}

// Execution limits.
const (
	DefaultMaxTicks = 100_000
	MaxMaxTicks     = 100_000_000
)

// Puzzle is a complete puzzle definition: hardware plus every test case.
type Puzzle struct {
	SchemaVersion int        `yaml:"schema_version" json:"schema_version"`
	ID            string     `yaml:"id" json:"id"`
	Name          string     `yaml:"name,omitempty" json:"name,omitempty"`
	Description   string     `yaml:"description,omitempty" json:"description,omitempty"`
	Cells         []CellDef  `yaml:"cells" json:"cells"`
	Tests         []TestCase `yaml:"tests" json:"tests"`
	Limits        Limits     `yaml:"limits,omitempty" json:"limits,omitempty"`
	// Hardware, when present (schema v2), lets players instantiate their own
	// hardware within these rules.
	Hardware *HardwareRules `yaml:"hardware,omitempty" json:"hardware,omitempty"`
	// Metadata is free-form presentation data for editors (layout hints,
	// difficulty, ...). The engine never reads it.
	Metadata map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`

	file string
	root *yaml.Node
	// migrated lists v1 → v2 conversions applied while parsing.
	migrated []string
}

// CellDef declares one puzzle-supplied (fixed) cell.
type CellDef struct {
	ID   string `yaml:"id" json:"id"`
	Type string `yaml:"type" json:"type"`
	// ProgramMemory is the program budget in bytes (programmable cells only).
	ProgramMemory int `yaml:"program_memory,omitempty" json:"program_memory,omitempty"`
	// PortCount is the number of physical ports (schema v2; defaults to the
	// type's default). Schema-v1 cells always have four.
	PortCount int `yaml:"port_count,omitempty" json:"port_count,omitempty"`
	// Ports lists the enabled physical ports. Omitted means all of them.
	Ports []string `yaml:"ports,omitempty" json:"ports,omitempty"`
	// PortFunctions assigns a function to each port of a peripheral that
	// declares port functions (schema v2; defaults to the type's default).
	PortFunctions []string       `yaml:"port_functions,omitempty" json:"port_functions,omitempty"`
	Config        map[string]any `yaml:"config,omitempty" json:"config,omitempty"`
	Metadata      map[string]any `yaml:"metadata,omitempty" json:"metadata,omitempty"`
}

// NumPorts returns the cell's port count in puzzle p: four for schema-v1
// cells, otherwise port_count or the type's default.
func (p *Puzzle) NumPorts(reg *cell.Registry, c *CellDef) int {
	if p.SchemaVersion < 2 {
		return vm.LegacyPorts
	}
	if c.PortCount > 0 {
		return c.PortCount
	}
	if a, ok := reg.Lookup(c.Type); ok {
		return cell.HardwareOf(a).Ports.Default
	}
	return vm.LegacyPorts
}

// EnabledPorts returns the enabled port mask for a cell with n ports.
// Invalid names are ignored (Validate reports them).
func (c *CellDef) EnabledPorts(n int) []bool {
	m := make([]bool, n)
	if c.Ports == nil {
		for i := range m {
			m[i] = true
		}
		return m
	}
	for _, s := range c.Ports {
		if p, ok := vm.ParsePhysicalPort(s); ok && p.Index() < n {
			m[p] = true
		}
	}
	return m
}

// CellSpec builds the engine spec of fixed cell c.
func (p *Puzzle) CellSpec(reg *cell.Registry, c *CellDef) cell.Spec {
	n := p.NumPorts(reg, c)
	return cell.Spec{ID: c.ID, Type: c.Type, ProgramMemory: c.ProgramMemory, PortCount: n, Ports: c.EnabledPorts(n), PortFunctions: c.PortFunctions, Config: c.Config}
}

// Migrated lists the schema-v1 conversions applied when the puzzle was
// parsed (for example a renamed cell type).
func (p *Puzzle) Migrated() []string { return p.migrated }

// TestCase is one input/expected-output scenario.
type TestCase struct {
	Name    string             `yaml:"name" json:"name"`
	Inputs  map[string][]int64 `yaml:"inputs" json:"inputs"`
	Outputs map[string][]int64 `yaml:"outputs" json:"outputs"`
}

// InputWords returns the input streams as Words (valid after Validate).
func (t *TestCase) InputWords() map[string][]vm.Word { return toWords(t.Inputs) }

// OutputWords returns the expected output streams as Words.
func (t *TestCase) OutputWords() map[string][]vm.Word { return toWords(t.Outputs) }

func toWords(m map[string][]int64) map[string][]vm.Word {
	out := make(map[string][]vm.Word, len(m))
	for k, vs := range m {
		ws := make([]vm.Word, len(vs))
		for i, v := range vs {
			ws[i] = vm.Word(v)
		}
		out[k] = ws
	}
	return out
}

// Limits are execution constraints applied to every test case.
type Limits struct {
	MaxTicks int `yaml:"max_ticks,omitempty" json:"max_ticks,omitempty"`
}

// File returns the path the puzzle was loaded from, if any.
func (p *Puzzle) File() string { return p.file }

// MaxTicks returns the effective tick limit.
func (p *Puzzle) MaxTicks() int {
	if p.Limits.MaxTicks > 0 {
		return p.Limits.MaxTicks
	}
	return DefaultMaxTicks
}

// CellIndex finds a cell by id.
func (p *Puzzle) CellIndex(id string) (int, bool) {
	for i := range p.Cells {
		if p.Cells[i].ID == id {
			return i, true
		}
	}
	return -1, false
}

// TestIndex finds a test case by name.
func (p *Puzzle) TestIndex(name string) (int, bool) {
	for i := range p.Tests {
		if p.Tests[i].Name == name {
			return i, true
		}
	}
	return -1, false
}

// Marshal renders the puzzle as YAML (for editors that export puzzles).
func (p *Puzzle) Marshal() ([]byte, error) { return yaml.Marshal(p) }

// LoadFile reads and parses a puzzle file. It does not validate it.
func LoadFile(path string) (*Puzzle, diag.List) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, diag.List{{Severity: diag.Error, Code: "io", File: path, Message: err.Error()}}
	}
	return Parse(data, path)
}

// Parse decodes puzzle YAML. Unknown fields are errors. It does not
// validate semantics; call Validate.
func Parse(data []byte, file string) (*Puzzle, diag.List) {
	root, l := decodeStrict(data, file, "puzzle")
	if l.HasErrors() {
		return nil, l
	}
	p := &Puzzle{file: file, root: root}
	if err := strictDecoder(data).Decode(p); err != nil {
		return nil, yamlErrors(err, file, "puzzle.yaml")
	}
	p.file, p.root = file, root
	p.migrateTypes()
	return p, nil
}

// migrateTypes renames v1 cell types that changed meaning in v2. The rest
// of a v1 puzzle already means the same thing in v2: every cell has four
// ports, and players cannot add hardware.
func (p *Puzzle) migrateTypes() {
	if p.SchemaVersion >= 2 {
		return
	}
	for i := range p.Cells {
		if n, ok := LegacyTypes[p.Cells[i].Type]; ok {
			p.migrated = append(p.migrated, fmt.Sprintf("cells[%d].type: %s → %s", i, p.Cells[i].Type, n))
			p.Cells[i].Type = n
		}
	}
}

func strictDecoder(data []byte) *yaml.Decoder {
	d := yaml.NewDecoder(bytes.NewReader(data))
	d.KnownFields(true)
	return d
}

func decodeStrict(data []byte, file, what string) (*yaml.Node, diag.List) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, yamlErrors(err, file, what+".yaml")
	}
	if root.Kind == 0 || len(root.Content) == 0 {
		return nil, diag.List{{Severity: diag.Error, Code: what + ".yaml", File: file, Message: what + " file is empty"}}
	}
	return &root, nil
}

func yamlErrors(err error, file, code string) diag.List {
	var l diag.List
	for _, e := range yamlpath.SplitError(err) {
		l = append(l, diag.Diagnostic{Severity: diag.Error, Code: code, File: file, Line: e.Line, Message: e.Msg})
	}
	return l
}

var idRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)

// Validate checks the puzzle against the registry and returns every
// problem found, annotated with YAML paths and (when available) lines.
func (p *Puzzle) Validate(reg *cell.Registry) diag.List {
	var l diag.List
	if p.SchemaVersion < MinSchemaVersion || p.SchemaVersion > SchemaVersion {
		l.Errorf("puzzle.schema_version", "schema_version", "unsupported schema_version %d (this build reads versions %d-%d)", p.SchemaVersion, MinSchemaVersion, SchemaVersion)
	}
	v1 := p.SchemaVersion < 2
	if v1 && p.Hardware != nil {
		l.Errorf("puzzle.schema_version", "hardware", "the hardware section needs schema_version: 2")
	}
	if p.ID == "" {
		l.Errorf("puzzle.id", "id", "puzzle id is required")
	} else if !idRe.MatchString(p.ID) {
		l.Errorf("puzzle.id", "id", "invalid puzzle id %q (letters, digits, '.', '_' and '-')", p.ID)
	}
	if len(p.Cells) == 0 {
		l.Errorf("puzzle.cells", "cells", "a puzzle must define at least one cell")
	}

	inputs := map[string]string{}  // stream -> cell id
	outputs := map[string]string{} // stream -> cell id
	ids := map[string]int{}
	for i := range p.Cells {
		c := &p.Cells[i]
		path := fmt.Sprintf("cells[%d]", i)
		switch {
		case c.ID == "":
			l.Errorf("puzzle.cell.id", path+".id", "cell id is required")
		case !idRe.MatchString(c.ID):
			l.Errorf("puzzle.cell.id", path+".id", "invalid cell id %q", c.ID)
		default:
			if prev, dup := ids[c.ID]; dup {
				l.Errorf("puzzle.cell.id", path+".id", "duplicate cell id %q (also cells[%d])", c.ID, prev)
			} else {
				ids[c.ID] = i
			}
		}
		if v1 && (c.PortCount != 0 || c.PortFunctions != nil) {
			l.Errorf("puzzle.schema_version", path, "port_count and port_functions need schema_version: 2")
		}
		n := p.NumPorts(reg, c)
		seenPorts := map[vm.Port]bool{}
		for k, s := range c.Ports {
			pp, ok := vm.ParsePhysicalPort(s)
			if !ok || pp.Index() >= n {
				l.Errorf("puzzle.cell.port", fmt.Sprintf("%s.ports[%d]", path, k), "invalid port %q (this cell has %d port(s), %s)", s, n, vm.PortRange(n))
				continue
			}
			if seenPorts[pp] {
				l.Errorf("puzzle.cell.port", fmt.Sprintf("%s.ports[%d]", path, k), "port %s listed twice", pp)
			}
			seenPorts[pp] = true
		}
		if c.Type == "" {
			l.Errorf("puzzle.cell.type", path+".type", "cell type is required")
			continue
		}
		arch, ok := reg.Lookup(c.Type)
		if !ok {
			l.Errorf("puzzle.cell.type", path+".type", "unknown cell type %q (registered: %s)", c.Type, strings.Join(reg.Types(), ", "))
			continue
		}
		if arch.ISA() != nil {
			if c.ProgramMemory <= 0 {
				l.Errorf("puzzle.cell.program_memory", path+".program_memory", "programmable cell %q needs a positive program_memory (bytes)", c.ID)
			} else if c.ProgramMemory > vm.MaxProgramBytes {
				l.Errorf("puzzle.cell.program_memory", path+".program_memory", "program_memory %d exceeds the maximum of %d bytes", c.ProgramMemory, vm.MaxProgramBytes)
			}
		} else if c.ProgramMemory != 0 {
			l.Errorf("puzzle.cell.program_memory", path+".program_memory", "cell type %q is not programmable and cannot have program_memory", c.Type)
		}
		cfgErrs := arch.ValidateConfig(c.Config)
		for _, e := range cfgErrs {
			fp := path + ".config"
			if e.Field != "" {
				fp += "." + e.Field
			}
			l.Errorf("puzzle.cell.config", fp, "%s", e.Message)
		}
		if !v1 && len(cfgErrs) == 0 {
			// Fixed cells must respect the plugin's physical limits too.
			hc := cell.HardwareConfig{Ports: n, PortFunctions: c.PortFunctions, ProgramMemory: c.ProgramMemory, Config: c.Config}
			for _, e := range cell.ValidateHardware(arch, hc) {
				if strings.HasPrefix(e.Field, "config") {
					continue // reported above
				}
				f := e.Field
				if f == "ports" {
					f = "port_count"
				}
				l.Errorf("puzzle.cell.hardware", path+"."+f, "%s", e.Message)
			}
		}
		if decl, ok := arch.(cell.StreamDeclarer); ok && len(cfgErrs) == 0 {
			for _, s := range decl.InputStreams(c.Config) {
				if prev, dup := inputs[s]; dup {
					l.Errorf("puzzle.stream", path+".config", "input stream %q is already bound to cell %q", s, prev)
				}
				inputs[s] = c.ID
			}
			for _, s := range decl.OutputStreams(c.Config) {
				if prev, dup := outputs[s]; dup {
					l.Errorf("puzzle.stream", path+".config", "output stream %q is already bound to cell %q", s, prev)
				}
				outputs[s] = c.ID
			}
		}
	}
	if len(p.Cells) > 0 && len(outputs) == 0 {
		l.Errorf("puzzle.cells", "cells", "a puzzle needs at least one output cell so test cases can be judged")
	}

	if len(p.Tests) == 0 {
		l.Errorf("puzzle.tests", "tests", "a puzzle must define at least one test case")
	}
	names := map[string]int{}
	for j := range p.Tests {
		t := &p.Tests[j]
		path := fmt.Sprintf("tests[%d]", j)
		if t.Name == "" {
			l.Errorf("puzzle.test.name", path+".name", "test name is required")
		} else if prev, dup := names[t.Name]; dup {
			l.Errorf("puzzle.test.name", path+".name", "duplicate test name %q (also tests[%d])", t.Name, prev)
		} else {
			names[t.Name] = j
		}
		checkStreams(&l, path+".inputs", "input", inputs, t.Inputs)
		checkStreams(&l, path+".outputs", "output", outputs, t.Outputs)
	}
	if p.Limits.MaxTicks < 0 || p.Limits.MaxTicks > MaxMaxTicks {
		l.Errorf("puzzle.limits", "limits.max_ticks", "max_ticks must be between 1 and %d", MaxMaxTicks)
	}
	p.validateHardware(&l, reg)
	return p.Annotate(l)
}

func checkStreams(l *diag.List, path, kind string, declared map[string]string, got map[string][]int64) {
	for _, s := range sortedKeys(declared) {
		if _, ok := got[s]; !ok {
			l.Errorf("puzzle.test.stream", path, "missing %s stream %q (bound to cell %q)", kind, s, declared[s])
		}
	}
	for _, s := range sortedKeys(got) {
		if _, ok := declared[s]; !ok {
			l.Errorf("puzzle.test.stream", path+"."+s, "%s stream %q is not bound to any %s cell", kind, s, kind)
			continue
		}
		for k, v := range got[s] {
			if _, ok := vm.FromInt64(v); !ok {
				l.Errorf("puzzle.test.value", fmt.Sprintf("%s.%s[%d]", path, s, k), "value %d is outside the 32-bit range", v)
			}
		}
	}
}

// Annotate fills File/Line/Column of diagnostics that have a YAML path.
func (p *Puzzle) Annotate(l diag.List) diag.List {
	for i := range l {
		if l[i].File == "" {
			l[i].File = p.file
		}
		if l[i].Path != "" && l[i].Line == 0 && p.root != nil {
			l[i].Line, l[i].Column = yamlpath.Locate(p.root, l[i].Path)
		}
	}
	return l
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
