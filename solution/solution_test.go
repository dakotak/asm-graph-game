package solution_test

import (
	"strings"
	"testing"

	"asmgraph/cells"
	"asmgraph/diag"
	"asmgraph/puzzle"
	"asmgraph/solution"
)

const puz = `schema_version: 1
id: p
cells:
  - {id: in, type: input, ports: [P0], config: {stream: xs}}
  - {id: cpu, type: standard, program_memory: 16}
  - {id: out, type: output, ports: [P0], config: {stream: ys}}
tests:
  - {name: t, inputs: {xs: [1]}, outputs: {ys: [1]}}
`

func validate(t *testing.T, src string) diag.List {
	t.Helper()
	reg := cells.NewRegistry()
	p, l := puzzle.Parse([]byte(puz), "p.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	if l := p.Validate(reg); l.HasErrors() {
		t.Fatal(l)
	}
	s, l := solution.Parse([]byte(src), "s.yaml")
	if l.HasErrors() {
		return l
	}
	return s.Validate(p, reg)
}

func TestValidSolution(t *testing.T) {
	l := validate(t, `schema_version: 1
puzzle: p
connections:
  - {from: {cell: in, port: P0}, to: {cell: cpu, port: P0}}
  - {from: {cell: cpu, port: P1}, to: {cell: out, port: P0}}
programs:
  cpu: |
    MOV P0, P1
metadata:
  positions: {cpu: [10, 20]}
`)
	if l.HasErrors() {
		t.Fatal(l)
	}
}

func TestSolutionCannotRedefineHardware(t *testing.T) {
	for _, field := range []string{"cells: []", "tests: []", "program_memory: 99"} {
		l := validate(t, "schema_version: 1\npuzzle: p\n"+field+"\n")
		if !l.HasErrors() || !strings.Contains(l.Error(), "not found") {
			t.Errorf("%s: want unknown-field error, got %v", field, l)
		}
	}
}

func TestSolutionErrors(t *testing.T) {
	l := validate(t, `schema_version: 1
puzzle: other
connections:
  - {from: {cell: in, port: P1}, to: {cell: cpu, port: P0}}
  - {from: {cell: cpu, port: P0}, to: {cell: cpu, port: P1}}
programs:
  out: "MOV 1, P0"
  ghost: "NOP"
`)
	want := []struct{ path, msg string }{
		{"puzzle", "is for puzzle"},
		{"connections[0].from.port", "disabled"},
		{"connections[1]", "itself"},
		{"programs.out", "not programmable"},
		{"programs.ghost", "no cell"},
	}
	for _, w := range want {
		found := false
		for _, d := range l {
			if d.Path == w.path && strings.Contains(d.Message, w.msg) {
				found = true
				if d.Line == 0 {
					t.Errorf("%s: no line number", w.path)
				}
			}
		}
		if !found {
			t.Errorf("missing %q at %s in:\n%v", w.msg, w.path, l)
		}
	}
}
