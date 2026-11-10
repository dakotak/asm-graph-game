package engine_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"asmgraph/cells"
	"asmgraph/engine"
	"asmgraph/puzzle"
	"asmgraph/solution"
)

// prepare builds a blueprint from inline YAML.
func prepare(t *testing.T, puz, sol string) *engine.Blueprint {
	t.Helper()
	reg := cells.NewRegistry()
	p, l := puzzle.Parse([]byte(puz), "puzzle.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	s, l := solution.Parse([]byte(sol), "solution.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	bp, l := engine.Prepare(reg, p, s)
	if l.HasErrors() {
		t.Fatal(l)
	}
	return bp
}

func run(t *testing.T, bp *engine.Blueprint, test int, opts engine.Options) *engine.TestResult {
	t.Helper()
	r, err := engine.RunTest(bp, test, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func machine(t *testing.T, bp *engine.Blueprint, opts engine.Options) *engine.Machine {
	t.Helper()
	m, err := engine.NewMachine(bp, 0, opts)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func cellAt(t *testing.T, m *engine.Machine, id string) *engine.CellSnapshot {
	t.Helper()
	c, ok := m.CellSnapshot(id)
	if !ok {
		t.Fatalf("no cell %s", id)
	}
	return c
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// pipeline: in -> cpu(P0), cpu(P1) -> out.
func pipeline(prog string, inputs, outputs string, extra string) (string, string) {
	puz := fmt.Sprintf(`schema_version: 1
id: t
cells:
  - {id: in, type: input, ports: [P0], config: {stream: xs}}
  - {id: cpu, type: standard, program_memory: 64}
  - {id: out, type: output, ports: [P0], config: {stream: ys}}
%s
tests:
  - {name: only, inputs: {xs: %s}, outputs: {ys: %s}}
`, extra, inputs, outputs)
	sol := fmt.Sprintf(`schema_version: 1
puzzle: t
connections:
  - {from: {cell: in, port: P0}, to: {cell: cpu, port: P0}}
  - {from: {cell: cpu, port: P1}, to: {cell: out, port: P0}}
programs:
  cpu: |
%s
`, indent(prog))
	return puz, sol
}

func indent(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := range lines {
		lines[i] = "    " + strings.TrimSpace(lines[i])
	}
	return strings.Join(lines, "\n")
}

func TestArithmeticBranchingAndWrap(t *testing.T) {
	// Output |x| - 1 for each input. The program has no JMP back to the
	// top: execution wraps from the end to instruction 0.
	puz, sol := pipeline(`
		MOV P0, ACC
		JGZ POS
		NEG
		POS: SUB 1
		MOV ACC, P1`, "[5, -3, 0, -2147483648]", "[4, 2, -1, 2147483647]", "")
	r := run(t, prepare(t, puz, sol), 0, engine.Options{})
	if !r.Passed {
		t.Fatalf("%s: %s %+v", r.Status, r.Message, r.Outputs)
	}
}

func TestRendezvousIsSameTick(t *testing.T) {
	puz, sol := pipeline("MOV P0, ACC\nMOV ACC, P1", "[7]", "[7]", "")
	m := machine(t, prepare(t, puz, sol), engine.Options{})
	m.Step()
	if acc := cellAt(t, m, "cpu").State["ACC"]; acc != int32(7) {
		t.Fatalf("after tick 1 ACC = %v (read should complete in the same tick both sides are ready)", acc)
	}
	m.Step()
	if m.Status() != engine.StatusPassed || m.Tick() != 2 {
		t.Fatalf("status %s at tick %d", m.Status(), m.Tick())
	}
}

const twoCPU = `schema_version: 1
id: t
cells:
  - {id: in, type: input, ports: [P0], config: {stream: xs}}
  - {id: a, type: standard, program_memory: 64}
  - {id: b, type: standard, program_memory: 64}
  - {id: out, type: output, ports: [P0], config: {stream: ys}}
tests:
  - {name: only, inputs: {xs: %s}, outputs: {ys: %s}}
`

func twoCPUSolution(a, b string) string {
	return fmt.Sprintf(`schema_version: 1
puzzle: t
connections:
  - {from: {cell: in, port: P0}, to: {cell: a, port: P0}}
  - {from: {cell: a, port: P1}, to: {cell: b, port: P0}}
  - {from: {cell: b, port: P1}, to: {cell: out, port: P0}}
programs:
  a: |
%s
  b: |
%s
`, indent(a), indent(b))
}

func TestBlockingWrite(t *testing.T) {
	bp := prepare(t, fmt.Sprintf(twoCPU, "[]", "[5]"),
		twoCPUSolution("MOV 5, P1\nHLT", "NOP\nNOP\nNOP\nMOV P0, ACC\nMOV ACC, P1"))
	m := machine(t, bp, engine.Options{})
	for tick := 1; tick <= 3; tick++ {
		m.Step()
		a := cellAt(t, m, "a")
		if !a.Blocked || a.PC != 0 || a.Pending == nil || *a.Pending.Output != 5 {
			t.Fatalf("tick %d: a should be blocked holding 5: %+v", tick, a)
		}
		if !strings.Contains(a.BlockReason, "b.P0 is not ready to read") {
			t.Errorf("reason = %q", a.BlockReason)
		}
	}
	m.Step() // b reads at tick 4
	if a := cellAt(t, m, "a"); a.Blocked || a.PC != 1 || a.Pending != nil {
		t.Fatalf("tick 4: a should have completed: %+v", a)
	}
	m.Run(0)
	if m.Status() != engine.StatusPassed || m.Tick() != 5 {
		t.Fatalf("status %s at tick %d", m.Status(), m.Tick())
	}
}

func TestBlockingRead(t *testing.T) {
	bp := prepare(t, fmt.Sprintf(twoCPU, "[]", "[9]"),
		twoCPUSolution("NOP\nNOP\nMOV 9, P1\nHLT", "MOV P0, ACC\nMOV ACC, P1"))
	m := machine(t, bp, engine.Options{})
	m.Step()
	m.Step()
	b := cellAt(t, m, "b")
	if !b.Blocked || !strings.Contains(b.BlockReason, "a.P1 is not ready to write") || b.Request != "read P0" {
		t.Fatalf("b should block reading: %+v", b)
	}
	m.Step()
	if b := cellAt(t, m, "b"); b.Blocked || b.State["ACC"] != int32(9) {
		t.Fatalf("tick 3: b should have read 9: %+v", b)
	}
}

func TestMultiStageInstructionRetainsValue(t *testing.T) {
	// a: MOV P0, P1 receives from the input, then must wait 3 ticks for b.
	bp := prepare(t, fmt.Sprintf(twoCPU, "[11, 22]", "[11, 22]"),
		twoCPUSolution("MOV P0, P1", "NOP\nNOP\nNOP\nMOV P0, ACC\nMOV ACC, P1"))
	m := machine(t, bp, engine.Options{})
	m.Step()
	a := cellAt(t, m, "a")
	if a.Stage != "write" || a.Pending == nil || *a.Pending.Output != 11 || a.Pending.Destination != "P1" {
		t.Fatalf("after tick 1 a should hold 11 for P1: %+v", a)
	}
	for i := 0; i < 2; i++ {
		m.Step()
		snap := m.Snapshot()
		if snap.Inputs[0].Position != 1 {
			t.Fatalf("input must not be read again while a is blocked (position %d)", snap.Inputs[0].Position)
		}
		if a := cellAt(t, m, "a"); *a.Pending.Output != 11 || !a.Blocked {
			t.Fatalf("pending value lost: %+v", a)
		}
	}
	m.Step() // tick 4: b reads 11
	if a := cellAt(t, m, "a"); a.Pending != nil || a.Completed != 1 {
		t.Fatalf("tick 4: a should complete: %+v", a)
	}
	m.Run(0)
	if m.Status() != engine.StatusPassed {
		t.Fatal(m.Result().Message)
	}
}

func TestOneTransferPerCellPerTick(t *testing.T) {
	// MOV P0, P1 needs one tick to receive and another to send, even when
	// both neighbours are ready immediately.
	puz, sol := pipeline("MOV P0, P1", "[1, 2, 3]", "[1, 2, 3]", "")
	m := machine(t, prepare(t, puz, sol), engine.Options{})
	m.Run(0)
	r := m.Result()
	if !r.Passed || r.Metrics.Ticks != 6 || r.Metrics.Transfers != 6 {
		t.Fatalf("status %s ticks %d transfers %d", r.Status, r.Metrics.Ticks, r.Metrics.Transfers)
	}
}

func TestSimultaneousCommunication(t *testing.T) {
	puz := `schema_version: 1
id: t
cells:
  - {id: a, type: input, ports: [P0], config: {stream: a}}
  - {id: b, type: input, ports: [P0], config: {stream: b}}
  - {id: x, type: output, ports: [P0], config: {stream: x}}
  - {id: y, type: output, ports: [P0], config: {stream: y}}
tests:
  - {name: t, inputs: {a: [1, 2], b: [3, 4]}, outputs: {x: [1, 2], y: [3, 4]}}
`
	sol := `schema_version: 1
puzzle: t
connections:
  - {from: {cell: a, port: P0}, to: {cell: x, port: P0}}
  - {from: {cell: b, port: P0}, to: {cell: y, port: P0}}
programs: {}
`
	var transfersAtTick1 int
	opts := engine.Options{Events: func(e engine.Event) {
		if e.Type == engine.EventPortTransfer && e.Tick == 1 {
			transfersAtTick1++
		}
	}}
	r := run(t, prepare(t, puz, sol), 0, opts)
	if !r.Passed || r.Metrics.Ticks != 2 || transfersAtTick1 != 2 {
		t.Fatalf("status %s ticks %d transfers@1 %d", r.Status, r.Metrics.Ticks, transfersAtTick1)
	}
}

const anyPuzzle = `schema_version: 1
id: t
cells:
  - {id: a, type: input, ports: [P0], config: {stream: a}}
  - {id: b, type: input, ports: [P0], config: {stream: b}}
  - {id: cpu, type: standard, program_memory: 32}
  - {id: out, type: output, ports: [P0], config: {stream: out}}
tests:
  - {name: t, inputs: {a: [10, 20], b: [30, 40]}, outputs: {out: [10, 20, 30, 40]}}
`

func anySolution(reverse bool) string {
	conns := []string{
		"  - {from: {cell: a, port: P0}, to: {cell: cpu, port: P1}}",
		"  - {from: {cell: b, port: P0}, to: {cell: cpu, port: P3}}",
		"  - {from: {cell: cpu, port: P0}, to: {cell: out, port: P0}}",
	}
	if reverse {
		conns[0], conns[2] = conns[2], conns[0]
		conns[1] = "  - {from: {cell: cpu, port: P3}, to: {cell: b, port: P0}}"
	}
	return `schema_version: 1
puzzle: t
connections:
` + strings.Join(conns, "\n") + `
programs:
  cpu: |
    MOV ANY, ACC
    MOV ACC, P0
    MOV LAST, ACC
    MOV ACC, P0
`
}

func TestAnyReadArbitrationAndLast(t *testing.T) {
	// Both inputs are ready at tick 1; ANY prefers the lowest port (P1, a).
	// LAST then keeps reading from a. After a is exhausted ANY selects b.
	for _, rev := range []bool{false, true} {
		bp := prepare(t, anyPuzzle, anySolution(rev))
		m := machine(t, bp, engine.Options{})
		m.Step()
		if c := cellAt(t, m, "cpu"); c.Last != "P1" || c.State["ACC"] != int32(10) {
			t.Fatalf("reverse=%v: tick 1 cpu = %+v", rev, c)
		}
		m.Run(0)
		if r := m.Result(); !r.Passed {
			t.Fatalf("reverse=%v: %s %+v", rev, r.Message, r.Outputs)
		}
	}
}

func TestAnyWriteArbitration(t *testing.T) {
	puz := `schema_version: 1
id: t
cells:
  - {id: cpu, type: standard, program_memory: 8}
  - {id: o0, type: output, config: {stream: o0}}
  - {id: o2, type: output, config: {stream: o2}}
tests:
  - {name: t, inputs: {}, outputs: {o0: [7], o2: []}}
`
	sol := `schema_version: 1
puzzle: t
connections:
  - {from: {cell: cpu, port: P2}, to: {cell: o2, port: P0}}
  - {from: {cell: cpu, port: P0}, to: {cell: o0, port: P0}}
programs:
  cpu: MOV 7, ANY
`
	r := run(t, prepare(t, puz, sol), 0, engine.Options{})
	if !r.Passed || r.Metrics.Ticks != 1 {
		t.Fatalf("ANY write should pick P0: %s %s %+v", r.Status, r.Message, r.Outputs)
	}
}

func TestLastBlocksUntilInitialized(t *testing.T) {
	puz, sol := pipeline("MOV LAST, ACC", "[1]", "[1]", "")
	r := run(t, prepare(t, puz, sol), 0, engine.Options{})
	if r.Status != engine.StatusDeadlocked || len(r.Blocked) != 1 || !strings.Contains(r.Blocked[0].Reason, "LAST is not set") {
		t.Fatalf("got %s %+v", r.Status, r.Blocked)
	}
}

func TestUnavailablePortsBlockWithDiagnostics(t *testing.T) {
	puz := `schema_version: 1
id: t
cells:
  - {id: in, type: input, ports: [P0], config: {stream: xs}}
  - {id: cpu, type: standard, program_memory: 32, ports: [P0, P1]}
  - {id: out, type: output, ports: [P0], config: {stream: ys}}
tests:
  - {name: t, inputs: {xs: [1]}, outputs: {ys: [1]}}
`
	for prog, want := range map[string]string{
		"MOV P3, ACC": "cannot read from P3: port is disabled on this cell",
		"MOV ACC, P1": "cannot write to P1: port is not connected",
	} {
		sol := "schema_version: 1\npuzzle: t\nconnections:\n  - {from: {cell: in, port: P0}, to: {cell: cpu, port: P0}}\nprograms:\n  cpu: " + prog + "\n"
		r := run(t, prepare(t, puz, sol), 0, engine.Options{})
		if r.Status != engine.StatusDeadlocked || r.Blocked[0].Reason != want {
			t.Errorf("%s: got %s %+v", prog, r.Status, r.Blocked)
		}
	}
}

func TestPermanentlyBlockedCellIsNotFailure(t *testing.T) {
	bp := prepare(t, fmt.Sprintf(twoCPU, "[3]", "[3]"),
		`schema_version: 1
puzzle: t
connections:
  - {from: {cell: in, port: P0}, to: {cell: b, port: P0}}
  - {from: {cell: b, port: P1}, to: {cell: out, port: P0}}
programs:
  a: MOV P2, ACC
  b: MOV P0, P1
`)
	r := run(t, bp, 0, engine.Options{})
	if !r.Passed {
		t.Fatalf("a is stuck forever but b satisfies the output: %s %s", r.Status, r.Message)
	}
}

func TestDeadlockDetection(t *testing.T) {
	// a and b both write to each other first: a classic deadlock.
	bp := prepare(t, fmt.Sprintf(twoCPU, "[1]", "[1]"),
		`schema_version: 1
puzzle: t
connections:
  - {from: {cell: a, port: P1}, to: {cell: b, port: P1}}
programs:
  a: MOV 1, P1
  b: MOV 2, P1
`)
	var got []engine.EventType
	r := run(t, bp, 0, engine.Options{Events: func(e engine.Event) {
		if e.Type == engine.EventDeadlockDetected || e.Type == engine.EventSimulationCompleted {
			got = append(got, e.Type)
		}
	}})
	if r.Status != engine.StatusDeadlocked || r.Metrics.Ticks != 2 || len(r.Blocked) != 2 {
		t.Fatalf("got %s at %d: %+v", r.Status, r.Metrics.Ticks, r.Blocked)
	}
	if len(got) != 2 || got[0] != engine.EventDeadlockDetected {
		t.Errorf("events = %v", got)
	}
}

func TestHaltedCellsLeadToDeadlock(t *testing.T) {
	puz, sol := pipeline("HLT", "[1]", "[1]", "")
	m := machine(t, prepare(t, puz, sol), engine.Options{})
	m.Run(0)
	if m.Status() != engine.StatusDeadlocked || !cellAt(t, m, "cpu").Halted {
		t.Fatalf("got %s", m.Status())
	}
}

func TestCycleDetectionAndTickLimit(t *testing.T) {
	puz, sol := pipeline("ADD 1\nSUB 1", "[1]", "[1]", "limits: {max_ticks: 100}")
	bp := prepare(t, puz, sol)
	r := run(t, bp, 0, engine.Options{CycleDetection: true})
	if r.Status != engine.StatusCycleDetected || r.Cycle == nil || r.Cycle.Period != 2 || r.Cycle.FirstTick != 0 {
		t.Fatalf("got %s %+v", r.Status, r.Cycle)
	}
	r = run(t, bp, 0, engine.Options{})
	if r.Status != engine.StatusTickLimit || r.Metrics.Ticks != 100 {
		t.Fatalf("without cycle detection: %s at %d", r.Status, r.Metrics.Ticks)
	}
	// A counter never repeats within the limit: no false positive.
	puz, sol = pipeline("ADD 1", "[1]", "[1]", "limits: {max_ticks: 100}")
	r = run(t, prepare(t, puz, sol), 0, engine.Options{CycleDetection: true})
	if r.Status != engine.StatusTickLimit {
		t.Fatalf("counter: %s", r.Status)
	}
	// MaxTicks option overrides the puzzle limit.
	r = run(t, prepare(t, puz, sol), 0, engine.Options{MaxTicks: 10})
	if r.Metrics.Ticks != 10 {
		t.Fatalf("override: %d ticks", r.Metrics.Ticks)
	}
}

func TestCycleDetectionMemoryLimit(t *testing.T) {
	puz, sol := pipeline("ADD 1", "[1]", "[1]", "limits: {max_ticks: 50}")
	r := run(t, prepare(t, puz, sol), 0, engine.Options{CycleDetection: true, MaxCycleStates: 10})
	if r.Status != engine.StatusTickLimit || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "memory limit") {
		t.Fatalf("got %s %v", r.Status, r.Notes)
	}
}

const twoStreams = `schema_version: 1
id: t
cells:
  - {id: a, type: input, ports: [P0], config: {stream: a}}
  - {id: b, type: input, ports: [P0], config: {stream: b}}
  - {id: x, type: output, ports: [P0], config: {stream: x}}
  - {id: y, type: output, ports: [P0], config: {stream: y}}
tests:
  - {name: t, inputs: {a: %s, b: %s}, outputs: {x: %s, y: %s}}
`

const twoStreamsSol = `schema_version: 1
puzzle: t
connections:
  - {from: {cell: a, port: P0}, to: {cell: x, port: P0}}
  - {from: {cell: b, port: P0}, to: {cell: y, port: P0}}
programs: {}
`

func TestFailureTakesPrecedenceOverSuccess(t *testing.T) {
	// In tick 1, x receives its final correct value and y a wrong one.
	r := run(t, prepare(t, fmt.Sprintf(twoStreams, "[1]", "[5]", "[1]", "[6]"), twoStreamsSol), 0, engine.Options{})
	if r.Status != engine.StatusFailed || r.Metrics.Ticks != 1 || !strings.Contains(r.Message, "expected 6") {
		t.Fatalf("got %s at %d: %s", r.Status, r.Metrics.Ticks, r.Message)
	}
	// Control: when y is right too, the test passes in the same tick.
	r = run(t, prepare(t, fmt.Sprintf(twoStreams, "[1]", "[5]", "[1]", "[5]"), twoStreamsSol), 0, engine.Options{})
	if !r.Passed || r.Metrics.Ticks != 1 {
		t.Fatalf("control: %s at %d", r.Status, r.Metrics.Ticks)
	}
}

func TestExtraOutputAfterStreamPassedFails(t *testing.T) {
	r := run(t, prepare(t, fmt.Sprintf(twoStreams, "[1, 1]", "[2, 3]", "[1]", "[2, 3]"), twoStreamsSol), 0, engine.Options{})
	if r.Status != engine.StatusFailed || !strings.Contains(r.Message, "unexpected extra value") {
		t.Fatalf("got %s: %s", r.Status, r.Message)
	}
	if r.Outputs[0].Mismatch == nil || r.Outputs[0].Mismatch.Expected != nil {
		t.Errorf("mismatch = %+v", r.Outputs[0].Mismatch)
	}
}

func TestWrongValueFailsImmediately(t *testing.T) {
	puz, sol := pipeline("MOV P0, ACC\nADD 1\nMOV ACC, P1", "[1, 2, 3]", "[2, 4, 4]", "")
	r := run(t, prepare(t, puz, sol), 0, engine.Options{})
	if r.Status != engine.StatusFailed || r.Outputs[0].Mismatch.Index != 1 || len(r.Outputs[0].Received) != 2 {
		t.Fatalf("got %s %+v", r.Status, r.Outputs)
	}
}

func TestEmptyExpectedOutputsPassAtTickZero(t *testing.T) {
	puz, sol := pipeline("MOV P0, P1", "[1]", "[]", "")
	r := run(t, prepare(t, puz, sol), 0, engine.Options{})
	if !r.Passed || r.Metrics.Ticks != 0 {
		t.Fatalf("got %s at %d", r.Status, r.Metrics.Ticks)
	}
}

func TestCompletionStopsImmediately(t *testing.T) {
	// The program keeps running forever; the test ends on the tick of the
	// final correct output (value k arrives at tick 3k).
	puz, sol := pipeline("MOV P0, ACC\nADD ACC\nMOV ACC, P1", "[1, 2, 3, 4]", "[2, 4, 6, 8]", "")
	r := run(t, prepare(t, puz, sol), 0, engine.Options{})
	if !r.Passed || r.Metrics.Ticks != 12 || r.Metrics.Transfers != 8 {
		t.Fatalf("got %s at %d with %d transfers", r.Status, r.Metrics.Ticks, r.Metrics.Transfers)
	}
}

func TestRuntimeError(t *testing.T) {
	puz := `schema_version: 1
id: t
cells:
  - {id: in, type: input, ports: [P0], config: {stream: xs}}
  - {id: s, type: stack, program_memory: 16, config: {stack_size: 2}}
  - {id: out, type: output, ports: [P0], config: {stream: ys}}
tests:
  - {name: t, inputs: {xs: [1]}, outputs: {ys: [1]}}
`
	sol := "schema_version: 1\npuzzle: t\nconnections: []\nprograms:\n  s: |\n    PUSH 1\n    POP NIL\n    POP ACC\n"
	r := run(t, prepare(t, puz, sol), 0, engine.Options{})
	if r.Status != engine.StatusRuntimeError || r.Error == nil || r.Error.Cell != "s" || r.Error.SourceLine != 3 || !strings.Contains(r.Error.Message, "underflow") {
		t.Fatalf("got %s %+v", r.Status, r.Error)
	}
	sol = "schema_version: 1\npuzzle: t\nconnections: []\nprograms:\n  s: PUSH 1\n"
	r = run(t, prepare(t, puz, sol), 0, engine.Options{})
	if r.Status != engine.StatusRuntimeError || !strings.Contains(r.Error.Message, "overflow") || r.Metrics.Ticks != 3 {
		t.Fatalf("overflow: %s %+v at %d", r.Status, r.Error, r.Metrics.Ticks)
	}
}

type maxBytes int

func (maxBytes) ID() string { return "max-bytes" }
func (n maxBytes) Validate(ctx *engine.ValidationContext) error {
	if ctx.Result.Metrics.TotalProgramBytes > int(n) {
		return errors.New("program too large")
	}
	return nil
}

func TestCustomValidator(t *testing.T) {
	puz, sol := pipeline("MOV P0, P1", "[1]", "[1]", "")
	bp := prepare(t, puz, sol)
	if r := run(t, bp, 0, engine.Options{Validators: []engine.Validator{maxBytes(5)}}); !r.Passed {
		t.Fatalf("3-byte program should pass: %s", r.Message)
	}
	r := run(t, bp, 0, engine.Options{Validators: []engine.Validator{maxBytes(2)}})
	if r.Status != engine.StatusFailed || !strings.Contains(r.Message, "max-bytes") {
		t.Fatalf("got %s %s", r.Status, r.Message)
	}
}

func reverseBlueprint(t *testing.T) *engine.Blueprint {
	return prepare(t, `schema_version: 1
id: t
cells:
  - {id: in, type: input, ports: [P0], config: {stream: xs}}
  - {id: s, type: stack, program_memory: 32}
  - {id: out, type: output, ports: [P0], config: {stream: ys}}
tests:
  - {name: one, inputs: {xs: [1, 2, 3, 0]}, outputs: {ys: [3, 2, 1, 0]}}
  - {name: two, inputs: {xs: [4, 0, 5, 6, 0]}, outputs: {ys: [4, 0, 6, 5, 0]}}
  - {name: leftover, inputs: {xs: [9, 8]}, outputs: {ys: []}}
  - {name: after, inputs: {xs: [7, 0]}, outputs: {ys: [7, 0]}}
`, `schema_version: 1
puzzle: t
connections:
  - {from: {cell: in, port: P0}, to: {cell: s, port: P0}}
  - {from: {cell: s, port: P1}, to: {cell: out, port: P0}}
programs:
  s: |
    READ: MOV P0, ACC
    JEZ FLUSH
    PUSH ACC
    JMP READ
    FLUSH: JSE DONE
    POP P1
    JMP FLUSH
    DONE: MOV ACC, P1
`)
}

func TestIsolationAndDeterminism(t *testing.T) {
	bp := reverseBlueprint(t)
	suite, err := engine.RunAll(bp, engine.Options{CycleDetection: true})
	if err != nil {
		t.Fatal(err)
	}
	if !suite.Passed {
		t.Fatal(mustJSON(t, suite))
	}
	// Each test run alone matches the suite result (no leakage of stacks,
	// registers, cursors, outputs, LAST, or pending state).
	for i := range bp.Puzzle.Tests {
		alone := run(t, bp, i, engine.Options{CycleDetection: true})
		if mustJSON(t, alone) != mustJSON(t, suite.Tests[i]) {
			t.Errorf("test %d differs when run alone", i)
		}
	}
	// Repeated runs are identical, including the event stream.
	trace := func() string {
		var evs []engine.Event
		for i := range bp.Puzzle.Tests {
			_, _ = engine.RunTest(bp, i, engine.Options{Events: func(e engine.Event) { evs = append(evs, e) }})
		}
		return mustJSON(t, evs)
	}
	first := trace()
	for i := 0; i < 3; i++ {
		if trace() != first {
			t.Fatal("event trace is not deterministic")
		}
		again, _ := engine.RunAll(bp, engine.Options{CycleDetection: true})
		if mustJSON(t, again) != mustJSON(t, suite) {
			t.Fatal("suite result is not deterministic")
		}
	}
}

func TestFreshMachinesStartIdentical(t *testing.T) {
	bp := reverseBlueprint(t)
	dirty, _ := engine.NewMachine(bp, 3, engine.Options{})
	dirty.Run(5) // leave a machine mid-run with state everywhere
	before := mustJSON(t, dirty.Snapshot())
	a, _ := engine.NewMachine(bp, 3, engine.Options{})
	b, _ := engine.NewMachine(bp, 3, engine.Options{})
	if mustJSON(t, a.Snapshot()) != mustJSON(t, b.Snapshot()) {
		t.Fatal("fresh machines differ")
	}
	s := a.Snapshot()
	c := s.Cells[1]
	if c.PC != 0 || c.Pending != nil || c.Last != "" || len(c.State["stack"].([]int32)) != 0 || c.State["ACC"] != int32(0) ||
		s.Inputs[0].Position != 0 || len(s.Outputs[0].Received) != 0 || s.Tick != 0 {
		t.Fatalf("fresh state not reset: %s", mustJSON(t, s))
	}
	if mustJSON(t, dirty.Snapshot()) != before {
		t.Fatal("creating machines disturbed an existing one")
	}
}

func TestEventsDescribeCommittedState(t *testing.T) {
	puz, sol := pipeline("MOV P0, ACC\nADD ACC\nMOV ACC, P1", "[1, 2]", "[2, 4]", "")
	bp := prepare(t, puz, sol)
	var evs []engine.Event
	m, _ := engine.NewMachine(bp, 0, engine.Options{Events: func(e engine.Event) { evs = append(evs, e) }})
	m.Run(0)
	r := m.Result()
	counts := map[engine.EventType]int{}
	var lastSeq uint64
	var lastTick int64
	for _, e := range evs {
		counts[e.Type]++
		if e.Seq != lastSeq+1 || e.Tick < lastTick {
			t.Fatalf("event order broken at %+v", e)
		}
		lastSeq, lastTick = e.Seq, e.Tick
	}
	if counts[engine.EventPortTransfer] != int(r.Metrics.Transfers) ||
		counts[engine.EventInstructionCompleted] != int(r.Metrics.Instructions) ||
		counts[engine.EventTickCompleted] != int(r.Metrics.Ticks) ||
		counts[engine.EventOutputValidation] != 1 || counts[engine.EventSimulationCompleted] != 1 {
		t.Fatalf("counts %v vs metrics %+v", counts, r.Metrics)
	}
	// cell.state events carry the committed snapshot: the first one for cpu
	// (tick 1) already shows ACC = 1.
	for _, e := range evs {
		if e.Type == engine.EventCellStateChanged && e.Cell == "cpu" {
			if cs := e.Data.(engine.CellSnapshot); cs.State["ACC"] != int32(1) || e.Tick != 1 {
				t.Fatalf("first cpu state event: tick %d %+v", e.Tick, cs.State)
			}
			break
		}
	}
	if evs[len(evs)-1].Type != engine.EventSimulationCompleted {
		t.Error("last event should be simulation.completed")
	}
}

func TestSession(t *testing.T) {
	reg := cells.NewRegistry()
	var evs []engine.Event
	s := engine.NewSession(reg, func(e engine.Event) { evs = append(evs, e) })
	if _, err := s.Start("", engine.Options{}); err == nil {
		t.Fatal("start without puzzle should fail")
	}
	p, _ := puzzle.LoadFile("../examples/puzzles/double-input.yaml")
	if l := s.LoadPuzzle(p); l.HasErrors() {
		t.Fatal(l)
	}
	sol, _ := solution.LoadFile("../examples/solutions/double-input.yaml")
	if l := s.LoadSolution(sol); l.HasErrors() {
		t.Fatal(l)
	}
	progs, l := s.Compile()
	if l.HasErrors() || len(progs) != 1 || progs[0].Bytes != 10 {
		t.Fatalf("compile: %v %+v", l, progs)
	}
	if _, err := s.Start("negative", engine.Options{}); err != nil {
		t.Fatal(err)
	}
	if st, tick, _ := s.Step(3); st != engine.StatusRunning || tick != 3 {
		t.Fatalf("step: %s %d", st, tick)
	}
	snap, _ := s.State()
	if snap.Test != "negative" || snap.Tick != 3 {
		t.Fatalf("state: %s %d", snap.Test, snap.Tick)
	}
	if c, err := s.Cell("processor"); err != nil || c.State["ACC"] != int32(-10) {
		t.Fatalf("cell: %v %+v", err, c)
	}
	if snap, _ := s.Reset(); snap.Tick != 0 || snap.Cells[1].State["ACC"] != int32(0) {
		t.Fatal("reset did not restore a fresh machine")
	}
	// Background run with pacing, then pause.
	if err := s.RunAsync(0, 5*time.Millisecond, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	st, tick, _ := s.Pause()
	if s.Running() || st.Terminal() || tick == 0 {
		t.Fatalf("pause: running=%v %s %d", s.Running(), st, tick)
	}
	time.Sleep(10 * time.Millisecond)
	if _, t2, _ := s.Step(0); t2 != tick+1 {
		t.Fatalf("machine advanced while paused: %d -> %d", tick, t2)
	}
	res, _ := s.Run(0)
	if !res.Passed || res.Metrics.Ticks != 15 {
		t.Fatalf("run: %s %d", res.Status, res.Metrics.Ticks)
	}
	suite, err := s.RunTests(engine.Options{})
	if err != nil || !suite.Passed || len(suite.Tests) != 3 {
		t.Fatalf("runTests: %v %+v", err, suite)
	}
	types := map[engine.EventType]bool{}
	for _, e := range evs {
		types[e.Type] = true
	}
	for _, want := range []engine.EventType{engine.EventSimulationStarted, engine.EventSimulationReset, engine.EventSimulationPaused, engine.EventTestCompleted, engine.EventSimulationCompleted} {
		if !types[want] {
			t.Errorf("missing %s event", want)
		}
	}
	s.Close()
}

func TestSessionAsyncRunCompletes(t *testing.T) {
	s := engine.NewSession(cells.NewRegistry(), nil)
	p, _ := puzzle.LoadFile("../examples/puzzles/filter-positive.yaml")
	s.LoadPuzzle(p)
	sol, _ := solution.LoadFile("../examples/solutions/filter-positive.yaml")
	s.LoadSolution(sol)
	if _, err := s.Start("0", engine.Options{}); err != nil {
		t.Fatal(err)
	}
	done := make(chan engine.Status, 1)
	if err := s.RunAsync(0, 0, func(st engine.Status) { done <- st }); err != nil {
		t.Fatal(err)
	}
	select {
	case st := <-done:
		if st != engine.StatusPassed {
			t.Fatalf("status %s", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async run did not finish")
	}
}
