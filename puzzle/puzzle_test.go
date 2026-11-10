package puzzle_test

import (
	"strings"
	"testing"

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
	if ports := p.Cells[1].EnabledPorts(); !ports[0] || !ports[3] {
		t.Error("omitted ports should enable all four")
	}
	if ports := p.Cells[0].EnabledPorts(); !ports[0] || ports[1] {
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
		{"schema", "schema_version: 1", "schema_version: 2", "schema_version", "unsupported schema_version"},
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
