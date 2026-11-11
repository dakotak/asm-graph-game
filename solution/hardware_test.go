package solution_test

import (
	"strings"
	"testing"

	"asmgraph/cells"
	"asmgraph/puzzle"
	"asmgraph/solution"
)

const rulesPuzzle = `schema_version: 2
id: rules
cells:
  - {id: in, type: input, config: {stream: xs}}
  - {id: out, type: output, config: {stream: ys}}
hardware:
  max_nodes: 3
  max_cost: 50
  ports: {max: 6}
  required: [stack]
  catalog:
    standard:
      max: 2
      ports: {min: 2}
      program_memory: {min: 8, max: 32, default: 16}
      cost:
        base: 10
        port: 2
        properties:
          program_memory: {cost: 0, per: 1}
    stack:
      port_functions: {allowed: [PUSH, POP, SIZE], max: {PUSH: 1}}
      config:
        capacity: {min: 2, max: 8}
tests:
  - {name: t, inputs: {xs: [1]}, outputs: {ys: [1]}}
`

func check(t *testing.T, hardware string) (solution.HardwareReport, string) {
	t.Helper()
	reg := cells.NewRegistry()
	p, l := puzzle.Parse([]byte(rulesPuzzle), "p.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	if l := p.Validate(reg); l.HasErrors() {
		t.Fatal(l)
	}
	s, l := solution.Parse([]byte("schema_version: 2\npuzzle: rules\nhardware:\n"+hardware+"\nconnections: []\nprograms: {}\n"), "s.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	return s.Report(p, reg), s.Validate(p, reg).Error()
}

func TestHardwareCost(t *testing.T) {
	// With the program-memory charge overridden to 0, a six-port processor
	// costs 10 + 6 × 2 = 22, as in the design notes.
	rep, errs := check(t, `  - {id: cpu, type: standard, port_count: 6}
  - {id: st, type: stack, port_count: 2, config: {capacity: 8}}`)
	if errs != "" {
		t.Fatal(errs)
	}
	if rep.Nodes[0].Cost != 22 || rep.Nodes[0].ProgramMemory != 16 {
		t.Errorf("cpu %+v", rep.Nodes[0])
	}
	// stack: 5 + 2 × 1 + ⌈8 / 4⌉ × 1 = 9 (plugin cost model, no override).
	if rep.Nodes[1].Cost != 9 || rep.TotalCost != 31 || rep.MaxCost != 50 {
		t.Errorf("stack %+v total %d", rep.Nodes[1], rep.TotalCost)
	}
	if got := strings.Join(rep.Nodes[1].PortFunctions, ","); got != "PUSH,POP" {
		t.Errorf("default functions %s", got)
	}
	// Unset configuration is costed at its default (capacity 8: ⌈8/4⌉ = 2).
	rep, errs = check(t, `  - {id: st, type: stack, port_count: 2}`)
	if errs != "" || rep.Nodes[0].Cost != 5+2+2 {
		t.Errorf("default capacity cost: %+v %s", rep.Nodes[0], errs)
	}
	// The default assignment respects the puzzle: PEEK is not allowed and
	// only one PUSH is, so the plugin default PUSH, POP, PEEK, SIZE, PUSH
	// becomes PUSH, POP, POP, SIZE, POP.
	rep, errs = check(t, `  - {id: st, type: stack, port_count: 5}`)
	if errs != "" {
		t.Fatal(errs)
	}
	if got := strings.Join(rep.Nodes[0].PortFunctions, ","); got != "PUSH,POP,POP,SIZE,POP" {
		t.Errorf("default functions %s", got)
	}
}

func TestHardwareConstraints(t *testing.T) {
	cases := []struct {
		name, hardware, want string
	}{
		{"required type missing", `  - {id: cpu, type: standard}`, "requires at least 1 stack node"},
		{"type not in catalog", `  - {id: st, type: stack}
  - {id: x, type: stack_processor}`, "does not allow stack_processor hardware"},
		{"too many of a type", `  - {id: st, type: stack}
  - {id: a, type: standard}
  - {id: b, type: standard}
  - {id: c, type: standard}`, "at most 2 standard node(s)"},
		{"too many nodes", `  - {id: st, type: stack}
  - {id: st2, type: stack}
  - {id: a, type: standard}
  - {id: b, type: standard}`, "at most 3 node(s)"},
		{"over budget", `  - {id: st, type: stack, port_count: 6, config: {capacity: 8}}
  - {id: a, type: standard, port_count: 6}
  - {id: b, type: standard, port_count: 6}`, "exceeds this puzzle's budget of 50"},
		{"plugin port limit", `  - {id: st, type: stack, port_count: 9}`, "stack supports 2-8 ports, not 9"},
		{"puzzle port limit", `  - {id: st, type: stack, port_count: 7}`, "this puzzle allows stack with 2-6 ports, not 7"},
		{"type port minimum", `  - {id: st, type: stack}
  - {id: a, type: standard, port_count: 1}`, "this puzzle allows standard with 2-6 ports, not 1"},
		{"memory range", `  - {id: st, type: stack}
  - {id: a, type: standard, program_memory: 64}`, "this puzzle allows standard with 8-32 bytes of program memory, not 64"},
		{"function not allowed", `  - {id: st, type: stack, port_count: 3, port_functions: [PUSH, POP, PEEK]}`, "does not allow PEEK ports"},
		{"function max", `  - {id: st, type: stack, port_count: 3, port_functions: [PUSH, PUSH, POP]}`, "at most 1 PUSH port(s)"},
		{"plugin function rule", `  - {id: st, type: stack, port_count: 2, port_functions: [POP, SIZE]}`, "needs at least 1 PUSH port(s)"},
		{"function count", `  - {id: st, type: stack, port_count: 3, port_functions: [PUSH, POP]}`, "exactly one function per port"},
		{"config range", `  - {id: st, type: stack, config: {capacity: 12}}`, "capacity 2-8, not 12"},
		{"plugin config", `  - {id: st, type: stack, config: {capacity: 999}}`, "must be <= 64"},
		{"id clash", `  - {id: in, type: stack}`, "already used by a puzzle cell"},
		{"duplicate id", `  - {id: st, type: stack}
  - {id: st, type: standard}`, "duplicate node id"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, errs := check(t, c.hardware)
			if !strings.Contains(errs, c.want) {
				t.Errorf("want %q, got:\n%s", c.want, errs)
			}
		})
	}
}

func TestFixedTopologyRejectsHardware(t *testing.T) {
	reg := cells.NewRegistry()
	p, _ := puzzle.Parse([]byte(`schema_version: 2
id: fixed
cells:
  - {id: in, type: input, config: {stream: xs}}
  - {id: out, type: output, config: {stream: ys}}
tests:
  - {name: t, inputs: {xs: [1]}, outputs: {ys: [1]}}
`), "p.yaml")
	s, _ := solution.Parse([]byte("schema_version: 2\npuzzle: fixed\nhardware:\n  - {id: a, type: standard}\nconnections: []\nprograms: {}\n"), "s.yaml")
	if errs := s.Validate(p, reg).Error(); !strings.Contains(errs, "fixed topology") {
		t.Error(errs)
	}
	s, _ = solution.Parse([]byte("schema_version: 1\npuzzle: fixed\nhardware:\n  - {id: a, type: standard}\nconnections: []\nprograms: {}\n"), "s.yaml")
	if errs := s.Validate(p, reg).Error(); !strings.Contains(errs, "needs schema_version: 2") {
		t.Error(errs)
	}
}
