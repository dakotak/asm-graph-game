package puzzle_test

import (
	"fmt"
	"strings"
	"testing"

	"asmgraph/cell"
	"asmgraph/cells"
	"asmgraph/diag"
	"asmgraph/puzzle"
)

const valid = `schema_version: 1
id: p
name: P
cells:
  - id: in
    type: input
    ports: [P0]
    config: {stream: xs}
  - id: cpu
    type: standard
    program_memory: 16
  - id: out
    type: output
    ports: [P0]
    config: {stream: ys}
tests:
  - name: one
    inputs: {xs: [1]}
    outputs: {ys: [1]}
  - name: two
    inputs: {xs: [1, 2]}
    outputs: {ys: [1, 2]}
  - name: three
    inputs: {xs: [-2147483648, 2147483647]}
    outputs: {ys: []}
`

func load(t *testing.T, src string) (*puzzle.Puzzle, diag.List) {
	t.Helper()
	p, l := puzzle.Parse([]byte(src), "p.yaml")
	if l.HasErrors() {
		return nil, l
	}
	return p, p.Validate(cells.NewRegistry())
}

func TestSingleFileWithMultipleTests(t *testing.T) {
	p, l := load(t, valid)
	if l.HasErrors() {
		t.Fatal(l)
	}
	if len(p.Tests) != 3 || p.Tests[1].Name != "two" || len(p.Tests[1].Inputs["xs"]) != 2 {
		t.Fatalf("tests not loaded: %+v", p.Tests)
	}
	if w := p.Tests[2].InputWords()["xs"]; w[0] != -2147483648 || w[1] != 2147483647 {
		t.Errorf("boundary words = %v", w)
	}
	if p.MaxTicks() != puzzle.DefaultMaxTicks {
		t.Errorf("default max ticks = %d", p.MaxTicks())
	}
	if ports := p.Cells[1].EnabledPorts(4); !ports[0] || !ports[3] {
		t.Error("omitted ports should enable all four")
	}
	if ports := p.Cells[0].EnabledPorts(4); !ports[0] || ports[1] {
		t.Error("explicit ports not honored")
	}
}

func expectErr(t *testing.T, src, path, msg string) {
	t.Helper()
	_, l := load(t, src)
	for _, d := range l {
		if d.Severity == diag.Error && d.Path == path && strings.Contains(d.Message, msg) {
			if d.Line == 0 && !strings.HasPrefix(path, "tests") {
				t.Errorf("diagnostic for %s has no line: %+v", path, d)
			}
			return
		}
	}
	t.Errorf("want error %q at %q, got:\n%v", msg, path, l)
}

func TestValidation(t *testing.T) {
	r := strings.NewReplacer
	cases := []struct {
		name, from, to, path, msg string
	}{
		{"schema", "schema_version: 1", "schema_version: 3", "schema_version", "unsupported schema_version"},
		{"dup cell", "id: cpu", "id: in", "cells[1].id", "duplicate cell id"},
		{"unknown type", "type: standard", "type: quantum", "cells[1].type", "unknown cell type"},
		{"bad port", "ports: [P0]\n    config: {stream: xs}", "ports: [P7]\n    config: {stream: xs}", "cells[0].ports[0]", "invalid port"},
		{"memory missing", "    program_memory: 16\n", "", "cells[1].program_memory", "positive program_memory"},
		{"memory on io", "    type: output\n", "    type: output\n    program_memory: 4\n", "cells[2].program_memory", "not programmable"},
		{"memory too big", "program_memory: 16", "program_memory: 70000", "cells[1].program_memory", "exceeds"},
		{"config missing", "config: {stream: ys}", "config: {}", "cells[2].config.stream", "required"},
		{"config unknown", "config: {stream: ys}", "config: {stream: ys, color: red}", "cells[2].config.color", "unknown configuration field"},
		{"dup test", "name: two", "name: one", "tests[1].name", "duplicate test name"},
		{"missing input", "inputs: {xs: [1]}", "inputs: {}", "tests[0].inputs", "missing input stream"},
		{"unknown output", "outputs: {ys: [1]}", "outputs: {ys: [1], zs: [2]}", "tests[0].outputs.zs", "not bound"},
		{"int range", "xs: [1, 2]", "xs: [1, 2147483648]", "tests[1].inputs.xs[1]", "outside the 32-bit range"},
		{"bad limit", "tests:", "limits: {max_ticks: -1}\ntests:", "limits.max_ticks", "max_ticks"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			src := r(c.from, c.to).Replace(valid)
			if src == valid {
				t.Fatal("replacement did not apply")
			}
			expectErr(t, src, c.path, c.msg)
		})
	}
}

func TestNeedsOutputAndTests(t *testing.T) {
	_, l := load(t, "schema_version: 1\nid: x\ncells:\n  - id: c\n    type: standard\n    program_memory: 4\ntests: []\n")
	msgs := l.Error()
	if !strings.Contains(msgs, "at least one output") || !strings.Contains(msgs, "at least one test") {
		t.Errorf("got %v", msgs)
	}
}

func TestParseErrors(t *testing.T) {
	_, l := puzzle.Parse([]byte("schema_version: 1\nid: x\nbogus_field: 3\n"), "f.yaml")
	if !l.HasErrors() || l[0].Line != 3 || !strings.Contains(l[0].Message, "bogus_field") {
		t.Errorf("unknown field: %v", l)
	}
	_, l = puzzle.Parse([]byte("cells: [\n"), "f.yaml")
	if !l.HasErrors() {
		t.Error("syntax error not reported")
	}
	_, l = puzzle.Parse([]byte(""), "f.yaml")
	if !l.HasErrors() {
		t.Error("empty file not reported")
	}
}

func TestMarshalRoundTrip(t *testing.T) {
	p, _ := load(t, valid)
	b, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	q, l := load(t, string(b))
	if l.HasErrors() || len(q.Tests) != 3 || q.Cells[1].ProgramMemory != 16 {
		t.Fatalf("round trip failed: %v\n%s", l, b)
	}
}

func TestHardwareRulesValidation(t *testing.T) {
	reg := cells.NewRegistry()
	base := `schema_version: 2
id: hw
cells:
  - {id: in, type: input, config: {stream: xs}}
  - {id: out, type: output, config: {stream: ys}}
hardware:
%s
tests:
  - {name: t, inputs: {xs: [1]}, outputs: {ys: [1]}}
`
	cases := []struct{ name, rules, path, want string }{
		{"unknown type", "  catalog: {quantum: {}}", "hardware.catalog.quantum", "unknown hardware type"},
		{"puzzle-only type", "  catalog: {input: {}}", "hardware.catalog.input", "can only be placed by the puzzle"},
		{"beyond plugin ports", "  catalog: {standard: {ports: {min: 9}}}", "hardware.catalog.standard.ports", "does not overlap the supported range 1-8"},
		{"beyond plugin memory", "  catalog: {standard: {program_memory: {min: 2000}}}", "hardware.catalog.standard.program_memory", "does not overlap"},
		{"memory on peripheral", "  catalog: {stack: {program_memory: {max: 8}}}", "hardware.catalog.stack.program_memory", "is not programmable"},
		{"unknown function", "  catalog: {stack: {port_functions: {allowed: [PUSH, POP, SHIFT]}}}", "hardware.catalog.stack.port_functions.allowed[2]", "unknown port function"},
		{"required function disallowed", "  catalog: {stack: {port_functions: {allowed: [PUSH, PEEK]}}}", "hardware.catalog.stack.port_functions.allowed", "requires a POP port"},
		{"function max below plugin min", "  catalog: {stack: {port_functions: {max: {PUSH: 0}}}}", "hardware.catalog.stack.port_functions.max.PUSH", "requires at least 1 PUSH"},
		{"config beyond plugin", "  catalog: {stack: {config: {capacity: {max: 500}}}}", "hardware.catalog.stack.config.capacity", "exceeds what stack supports"},
		{"unknown config", "  catalog: {stack: {config: {colour: {values: [red]}}}}", "hardware.catalog.stack.config.colour", "no configuration field"},
		{"negative cost", "  catalog: {stack: {cost: {base: -1}}}", "hardware.catalog.stack.cost", "nonnegative"},
		{"required and prohibited", "  required: [stack]\n  prohibited: [stack]", "hardware.required[0]", "both required and prohibited"},
		{"minimums exceed max nodes", "  max_nodes: 1\n  catalog: {standard: {min: 1}, stack: {min: 1}}", "hardware.max_nodes", "less than the 2 node(s)"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, l := puzzle.Parse([]byte(fmt.Sprintf(base, c.rules)), "p.yaml")
			if l.HasErrors() {
				t.Fatal(l)
			}
			l = p.Validate(reg)
			for _, d := range l {
				if d.Path == c.path && strings.Contains(d.Message, c.want) {
					return
				}
			}
			t.Errorf("want %q at %s, got:\n%v", c.want, c.path, l)
		})
	}
	// v1 files cannot use v2 features.
	p, _ := puzzle.Parse([]byte(strings.Replace(fmt.Sprintf(base, "  max_cost: 5"), "schema_version: 2", "schema_version: 1", 1)), "p.yaml")
	if l := p.Validate(reg); !strings.Contains(l.Error(), "hardware section needs schema_version: 2") {
		t.Error(l)
	}
}

// A plugin that does not allow cost or default overrides.
type rigid struct{ cells.Stack }

func (rigid) Type() string { return "rigid" }
func (r rigid) Hardware() cell.HardwareSpec {
	h := r.Stack.Hardware()
	h.Overrides = cell.Overrides{}
	return h
}

func TestOverridesNeedPluginPermission(t *testing.T) {
	reg := cells.NewRegistry()
	if err := reg.Register(rigid{}); err != nil {
		t.Fatal(err)
	}
	p, _ := puzzle.Parse([]byte(`schema_version: 2
id: hw
cells:
  - {id: in, type: input, config: {stream: xs}}
  - {id: out, type: output, config: {stream: ys}}
hardware:
  catalog:
    rigid: {cost: {base: 1}, ports: {default: 3}}
    stack: {cost: {base: 1}}
tests:
  - {name: t, inputs: {xs: [1]}, outputs: {ys: [1]}}
`), "p.yaml")
	text := p.Validate(reg).Error()
	for _, want := range []string{"rigid does not allow puzzles to override its cost", "rigid does not allow puzzles to override its defaults"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
	if strings.Contains(text, "stack does not allow") {
		t.Errorf("stack permits overrides: %s", text)
	}
	e, ok := p.EffectiveHardware(reg, "stack")
	if !ok || e.Cost.Base != 1 {
		t.Errorf("stack override not applied: %+v", e.Cost)
	}
}

func TestMigrateV1(t *testing.T) {
	reg := cells.NewRegistry()
	v1 := `schema_version: 1
id: m
cells:
  # the input keeps its comment
  - {id: in, type: input, ports: [P0], config: {stream: xs}}
  - {id: out, type: output, ports: [P2], config: {stream: ys}}
  - {id: s, type: stack, program_memory: 8}
tests:
  - {name: t, inputs: {xs: [1]}, outputs: {ys: [1]}}
`
	p, _ := puzzle.Parse([]byte(v1), "p.yaml")
	if p.Cells[2].Type != "stack_processor" || len(p.Migrated()) != 1 {
		t.Errorf("parse-time migration: %v", p.Migrated())
	}
	out, notes, err := puzzle.MigrateYAML([]byte(v1), reg)
	if err != nil {
		t.Fatal(err)
	}
	text := string(out)
	for _, want := range []string{"schema_version: 2", "type: stack_processor", "# the input keeps its comment", "port_count: 4"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if len(notes) != 4 {
		t.Errorf("notes: %v", notes)
	}
	p2, l := puzzle.Parse(out, "p2.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	if l := p2.Validate(reg); l.HasErrors() {
		t.Fatal(l)
	}
	// in: one port (ports list dropped); out: four ports with only P2 enabled.
	if n := p2.NumPorts(reg, &p2.Cells[0]); n != 1 {
		t.Errorf("in has %d ports", n)
	}
	if n := p2.NumPorts(reg, &p2.Cells[1]); n != 4 || !p2.Cells[1].EnabledPorts(4)[2] || p2.Cells[1].EnabledPorts(4)[0] {
		t.Errorf("out ports changed")
	}
	if again, notes, _ := puzzle.MigrateYAML(out, reg); len(notes) != 0 || string(again) != string(out) {
		t.Error("migrating a v2 file must be a no-op")
	}
}
