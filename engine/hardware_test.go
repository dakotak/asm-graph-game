package engine_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"asmgraph/cells"
	"asmgraph/engine"
	"asmgraph/puzzle"
	"asmgraph/solution"
)

// hwPuzzle is a schema-v2 puzzle with one input, one output, and an open
// hardware catalog (standard processors and stacks).
func hwPuzzle(inputs, outputs string) string {
	return fmt.Sprintf(`schema_version: 2
id: hw
cells:
  - {id: in, type: input, config: {stream: xs}}
  - {id: out, type: output, config: {stream: ys}}
hardware:
  catalog:
    standard: {}
    stack: {}
limits: {max_ticks: 500}
tests:
  - name: t
    inputs: {xs: %s}
    outputs: {ys: %s}
`, inputs, outputs)
}

func hwSolution(hardware, connections, programs string) string {
	return fmt.Sprintf("schema_version: 2\npuzzle: hw\nhardware:\n%s\nconnections:\n%s\nprograms:\n%s\n", hardware, connections, programs)
}

func progBlock(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	return "    " + strings.Join(lines, "\n    ")
}

func TestPassiveStackExample(t *testing.T) {
	reg := cells.NewRegistry()
	p, l := puzzle.LoadFile("../examples/puzzles/stack-reverse.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	s, l := solution.LoadFile("../examples/solutions/stack-reverse.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	bp, l := engine.Prepare(reg, p, s)
	if l.HasErrors() {
		t.Fatal(l)
	}
	res, err := engine.RunAll(bp, engine.Options{CycleDetection: true})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Passed || res.Score == nil {
		t.Fatalf("expected pass with a score: %s", mustJSON(t, res))
	}
	// cpu: 10 + 4×2 + ⌈32/8⌉ = 22; stack: 5 + 2×1 + ⌈9/4⌉ = 10.
	want := engine.Score{Cost: 32, Ticks: res.Summary.MaxTicks, Bytes: 26}
	if *res.Score != want {
		t.Errorf("score %+v, want %+v", *res.Score, want)
	}
	// Each test case starts with fresh hardware: running one case alone
	// gives exactly the result it had inside the suite.
	for i, tr := range res.Tests {
		alone := run(t, bp, i, engine.Options{CycleDetection: true})
		if mustJSON(t, alone) != mustJSON(t, tr) {
			t.Errorf("test %d differs when run alone", i)
		}
	}
}

func TestStackPeekSizeAndPorts(t *testing.T) {
	// A five-port processor (P4 exists only because port_count is 5) and a
	// stack with one port of each function.
	sol := hwSolution(`  - {id: cpu, type: standard, port_count: 5, program_memory: 64}
  - {id: stack, type: stack, port_count: 4, port_functions: [PUSH, PEEK, SIZE, POP]}`,
		`  - {from: {cell: cpu, port: P0}, to: {cell: stack, port: P0}}
  - {from: {cell: stack, port: P1}, to: {cell: cpu, port: P1}}
  - {from: {cell: stack, port: P2}, to: {cell: cpu, port: P2}}
  - {from: {cell: stack, port: P3}, to: {cell: cpu, port: P3}}
  - {from: {cell: cpu, port: P4}, to: {cell: out, port: P0}}`,
		"  cpu: |\n"+progBlock(`MOV 5, P0
MOV 7, P0
MOV P2, P4   # SIZE -> 2
MOV P1, P4   # PEEK -> 7 (not removed)
MOV P3, P4   # POP  -> 7
MOV P2, P4   # SIZE -> 1
HLT`))
	bp := prepare(t, hwPuzzle("[]", "[2, 7, 7, 1]"), sol)
	r := run(t, bp, 0, engine.Options{})
	if r.Status != engine.StatusPassed {
		t.Fatalf("%s: %s", r.Status, r.Message)
	}
	m := machine(t, bp, engine.Options{})
	m.Run(0)
	st := cellAt(t, m, "stack")
	if got := st.State["stack"]; fmt.Sprint(got) != "[5]" {
		t.Errorf("final stack %v, want [5]", got)
	}
	if st.Category != "peripheral" || !st.Player || st.Ports[3].Function != "POP" {
		t.Errorf("stack snapshot: %+v", st)
	}
}

func TestStackBlocksWithoutPartialChanges(t *testing.T) {
	// Capacity 2: the third push blocks forever, and the stack keeps
	// exactly the two values.
	sol := hwSolution(`  - {id: cpu, type: standard, port_count: 2, program_memory: 64}
  - {id: stack, type: stack, port_count: 2, config: {capacity: 2}}`,
		`  - {from: {cell: cpu, port: P0}, to: {cell: stack, port: P0}}
  - {from: {cell: cpu, port: P1}, to: {cell: out, port: P0}}`,
		"  cpu: |\n"+progBlock("MOV 1, P0\nMOV 2, P0\nMOV 3, P0\nMOV 9, P1"))
	bp := prepare(t, hwPuzzle("[]", "[9]"), sol)
	r := run(t, bp, 0, engine.Options{})
	if r.Status != engine.StatusDeadlocked || len(r.Blocked) != 1 ||
		!strings.Contains(r.Blocked[0].Reason, "stack.P0 (PUSH) is not ready to read") {
		t.Fatalf("%s %+v", r.Status, r.Blocked)
	}
	m := machine(t, bp, engine.Options{})
	m.Run(0)
	if got := cellAt(t, m, "stack").State["stack"]; fmt.Sprint(got) != "[1 2]" {
		t.Errorf("stack %v, want [1 2]", got)
	}

	// Popping an empty stack blocks until data arrives (here: never).
	sol = hwSolution(`  - {id: cpu, type: standard, port_count: 2, program_memory: 64}
  - {id: stack, type: stack, port_count: 2}`,
		`  - {from: {cell: stack, port: P1}, to: {cell: cpu, port: P0}}
  - {from: {cell: cpu, port: P1}, to: {cell: out, port: P0}}`,
		"  cpu: |\n"+progBlock("MOV P0, P1"))
	r = run(t, prepare(t, hwPuzzle("[]", "[9]"), sol), 0, engine.Options{})
	if r.Status != engine.StatusDeadlocked || !strings.Contains(r.Blocked[0].Reason, "stack.P1 (POP) is not ready to write") {
		t.Fatalf("%s %+v", r.Status, r.Blocked)
	}
}

func TestStackWrongDirectionIsExplained(t *testing.T) {
	sol := hwSolution(`  - {id: cpu, type: standard, port_count: 2, program_memory: 64}
  - {id: stack, type: stack, port_count: 2}`,
		`  - {from: {cell: cpu, port: P0}, to: {cell: stack, port: P1}}
  - {from: {cell: cpu, port: P1}, to: {cell: out, port: P0}}`,
		"  cpu: |\n"+progBlock("MOV 4, P0"))
	r := run(t, prepare(t, hwPuzzle("[]", "[9]"), sol), 0, engine.Options{})
	if r.Status != engine.StatusDeadlocked || !strings.Contains(r.Blocked[0].Reason, "stack.P1 is a POP port, which only sends values") {
		t.Fatalf("%s %+v", r.Status, r.Blocked)
	}
}

// Two processors push in the same tick. The stack completes one transfer
// per tick, lowest-numbered stack port first, so the result is fixed no
// matter how the hardware or connections are listed.
func TestStackArbitrationIsDeterministic(t *testing.T) {
	nodes := []string{
		"  - {id: a, type: standard, port_count: 1, program_memory: 16}",
		"  - {id: b, type: standard, port_count: 1, program_memory: 16}",
		"  - {id: c, type: standard, port_count: 2, program_memory: 16}",
		"  - {id: stack, type: stack, port_count: 3, port_functions: [PUSH, PUSH, POP]}",
	}
	conns := []string{
		"  - {from: {cell: a, port: P0}, to: {cell: stack, port: P0}}",
		"  - {from: {cell: b, port: P0}, to: {cell: stack, port: P1}}",
		"  - {from: {cell: stack, port: P2}, to: {cell: c, port: P0}}",
		"  - {from: {cell: c, port: P1}, to: {cell: out, port: P0}}",
	}
	progs := "  a: |\n" + progBlock("MOV 1, P0\nHLT") + "\n  b: |\n" + progBlock("MOV 2, P0\nHLT") + "\n  c: |\n" + progBlock("MOV P0, P1")
	reverse := func(s []string) []string {
		out := make([]string, len(s))
		for i := range s {
			out[len(s)-1-i] = s[i]
		}
		return out
	}
	var transfers []string
	for _, order := range [][2][]string{{nodes, conns}, {reverse(nodes), reverse(conns)}} {
		bp := prepare(t, hwPuzzle("[]", "[1, 2]"), hwSolution(strings.Join(order[0], "\n"), strings.Join(order[1], "\n"), progs))
		var log []string
		r := run(t, bp, 0, engine.Options{Events: func(e engine.Event) {
			if d, ok := e.Data.(engine.TransferEvent); ok {
				log = append(log, fmt.Sprintf("%d %s->%s %d", e.Tick, d.From, d.To, d.Value))
			}
		}})
		if r.Status != engine.StatusPassed {
			t.Fatalf("%s: %s", r.Status, r.Message)
		}
		transfers = append(transfers, strings.Join(log, "; "))
	}
	if transfers[0] != transfers[1] {
		t.Errorf("listing order changed the run:\n%s\n%s", transfers[0], transfers[1])
	}
	// Tick 1: a pushes (stack P0 beats P1). Tick 2: the pop to c (rank 0
	// on c's side) beats b's push.
	if !strings.HasPrefix(transfers[0], "1 a.P0->stack.P0 1; 2 stack.P2->c.P0 1;") {
		t.Errorf("unexpected arbitration: %s", transfers[0])
	}
}

func TestVariablePortsAnyAndLast(t *testing.T) {
	// The input arrives on P5 of a six-port processor; ANY scans all six
	// configured ports and LAST remembers P5.
	sol := hwSolution(`  - {id: cpu, type: standard, port_count: 6, program_memory: 64}`,
		`  - {from: {cell: in, port: P0}, to: {cell: cpu, port: P5}}
  - {from: {cell: cpu, port: P2}, to: {cell: out, port: P0}}`,
		"  cpu: |\n"+progBlock("MOV ANY, ACC\nMOV ACC, P2"))
	bp := prepare(t, hwPuzzle("[4, 5]", "[4, 5]"), sol)
	m := machine(t, bp, engine.Options{})
	if s := m.Run(0); s != engine.StatusPassed {
		t.Fatal(s)
	}
	c := cellAt(t, m, "cpu")
	if c.Last != "P5" || len(c.Ports) != 6 {
		t.Errorf("last %q, %d ports", c.Last, len(c.Ports))
	}

	// A program may only name ports the cell has.
	reg := cells.NewRegistry()
	p, _ := puzzle.Parse([]byte(hwPuzzle("[4]", "[4]")), "p.yaml")
	s, _ := solution.Parse([]byte(hwSolution(`  - {id: cpu, type: standard, port_count: 2, program_memory: 64}`,
		`  - {from: {cell: in, port: P0}, to: {cell: cpu, port: P0}}
  - {from: {cell: cpu, port: P3}, to: {cell: out, port: P0}}`,
		"  cpu: |\n"+progBlock("MOV P0, P3"))), "s.yaml")
	_, l := engine.Prepare(reg, p, s)
	text := l.Error()
	for _, want := range []string{`cell "cpu" has no port P3`, "port P3 does not exist on this cell"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in %s", want, text)
		}
	}
}

func TestMigratedPuzzleBehavesIdentically(t *testing.T) {
	reg := cells.NewRegistry()
	for _, name := range []string{"reverse-sequences", "double-input", "combine-streams"} {
		data, err := os.ReadFile("../examples/puzzles/" + name + ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		migrated, notes, err := puzzle.MigrateYAML(data, reg)
		if err != nil || len(notes) == 0 {
			t.Fatalf("%s: %v %v", name, notes, err)
		}
		solData, _ := os.ReadFile("../examples/solutions/" + name + ".yaml")
		migSol, _, err := puzzle.MigrateSolutionYAML(solData)
		if err != nil {
			t.Fatal(err)
		}
		results := []string{}
		for _, pair := range [][2][]byte{{data, solData}, {migrated, migSol}} {
			p, l := puzzle.Parse(pair[0], name)
			if l.HasErrors() {
				t.Fatal(l)
			}
			s, l := solution.Parse(pair[1], name)
			if l.HasErrors() {
				t.Fatal(l)
			}
			bp, l := engine.Prepare(reg, p, s)
			if l.HasErrors() {
				t.Fatalf("%s: %v\n%s", name, l, pair[0])
			}
			res, err := engine.RunAll(bp, engine.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if !res.Passed {
				t.Fatalf("%s did not pass", name)
			}
			results = append(results, mustJSON(t, res))
		}
		if results[0] != results[1] {
			t.Errorf("%s: migration changed the results", name)
		}
	}
}

func TestScoreOnlyForPassingSolutions(t *testing.T) {
	sol := hwSolution(`  - {id: cpu, type: standard, port_count: 2, program_memory: 16}`,
		`  - {from: {cell: in, port: P0}, to: {cell: cpu, port: P0}}
  - {from: {cell: cpu, port: P1}, to: {cell: out, port: P0}}`,
		"  cpu: |\n"+progBlock("MOV P0, ACC\nADD 1\nMOV ACC, P1"))
	bp := prepare(t, hwPuzzle("[1, 2]", "[2, 3]"), sol)
	res, err := engine.RunAll(bp, engine.Options{})
	if err != nil || res.Score == nil {
		t.Fatalf("passing suite needs a score: %v", err)
	}
	// 10 + 2 ports × 2 + ⌈16 / 8⌉ = 16; MOV P0,ACC (3) + ADD 1 (2) + MOV ACC,P1 (3).
	if res.Score.Cost != 16 || res.Score.Bytes != 8 || res.Score.Ticks != res.Summary.MaxTicks {
		t.Errorf("score %+v", *res.Score)
	}
	bp = prepare(t, hwPuzzle("[1, 2]", "[5, 5]"), sol)
	res, _ = engine.RunAll(bp, engine.Options{})
	if res.Passed || res.Score != nil {
		t.Error("a failing suite must not be scored")
	}
}
