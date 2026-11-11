package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"asmgraph/cells"
	"asmgraph/engine"
	"asmgraph/examples/plugins/multiplier"
	"asmgraph/plugins"
	"asmgraph/puzzle"
	"asmgraph/solution"
)

func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(""), &out, &errb)
	return code, out.String(), errb.String()
}

const ex = "../../examples/"

// Every example puzzle has at least one matching solution (NAME.yaml or
// NAME-*.yaml) that passes every test, through the CLI exactly as a user
// would run it.
func TestExamplesPassThroughCLI(t *testing.T) {
	puzzles, _ := filepath.Glob(ex + "puzzles/*.yaml")
	if len(puzzles) < 7 {
		t.Fatalf("expected at least 7 example puzzles, found %d", len(puzzles))
	}
	for _, p := range puzzles {
		name := strings.TrimSuffix(filepath.Base(p), ".yaml")
		sols, _ := filepath.Glob(ex + "solutions/" + name + "-*.yaml")
		if _, err := os.Stat(ex + "solutions/" + name + ".yaml"); err == nil {
			sols = append(sols, ex+"solutions/"+name+".yaml")
		}
		if len(sols) == 0 {
			t.Errorf("%s has no example solution", name)
		}
		for _, sol := range sols {
			t.Run(filepath.Base(sol), func(t *testing.T) { examplePasses(t, p, sol) })
		}
	}
}

func examplePasses(t *testing.T, p, sol string) {
	t.Helper()
	{
		{
			if code, out, errs := runCLI(t, "validate", p, sol); code != 0 {
				t.Fatalf("validate: %d\n%s%s", code, out, errs)
			}
			if code, out, errs := runCLI(t, "compile", p, sol, "--disasm"); code != 0 {
				t.Fatalf("compile: %d\n%s%s", code, out, errs)
			}
			code, out, errs := runCLI(t, "test", p, sol)
			if code != 0 || !strings.Contains(out, "PASSED") {
				t.Fatalf("test: %d\n%s%s", code, out, errs)
			}
			code, out, _ = runCLI(t, "test", p, sol, "--json", "--cycles")
			var res engine.SuiteResult
			if err := json.Unmarshal([]byte(out), &res); err != nil || code != 0 || !res.Passed {
				t.Fatalf("test --json: %d %v\n%s", code, err, out)
			}
			// Every test case of the single puzzle file was executed.
			pz, _ := puzzle.LoadFile(p)
			if len(res.Tests) != len(pz.Tests) || len(res.Tests) < 3 {
				t.Fatalf("ran %d of %d tests", len(res.Tests), len(pz.Tests))
			}
			if code, out, _ := runCLI(t, "run", p, sol, "--trace"); code != 0 || !strings.Contains(out, "transfer") {
				t.Fatalf("run --trace: %d\n%s", code, out)
			}
		}
	}
}

func TestDistributedPuzzleNeedsSplitting(t *testing.T) {
	code, _, errs := runCLI(t, "compile", ex+"puzzles/distributed-polynomial.yaml", ex+"solutions/invalid/distributed-too-big.yaml")
	if code != exitFailure || !strings.Contains(errs, "program is 12 bytes but this cell has 8 bytes") {
		t.Fatalf("%d\n%s", code, errs)
	}
}

func TestInvalidSolutionDiagnostics(t *testing.T) {
	code, _, errs := runCLI(t, "validate", ex+"puzzles/double-input.yaml", ex+"solutions/invalid/double-input-broken.yaml")
	if code != exitFailure {
		t.Fatalf("exit %d", code)
	}
	for _, want := range []string{
		"double-input-broken.yaml:8:31: connections[1].from.port: port P2 is disabled",
		"connections[2].from: port processor.P0 is already used",
		`cell "processor" has no port P5`,
		"double-input-broken.yaml:18: programs.processor: cell processor, line 2:1: unknown instruction MUL",
		`port P9 does not exist on this cell`,
		`undefined label "LOOP"`,
	} {
		if !strings.Contains(errs, want) {
			t.Errorf("missing %q in:\n%s", want, errs)
		}
	}
	code, out, _ := runCLI(t, "validate", ex+"puzzles/double-input.yaml", ex+"solutions/invalid/double-input-broken.yaml", "--json")
	var v struct {
		Valid       bool              `json:"valid"`
		Diagnostics []json.RawMessage `json:"diagnostics"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Valid || len(v.Diagnostics) != 6 || code != exitFailure {
		t.Fatalf("json: %v %s", err, out)
	}
}

func TestDeadlockReportedByCLI(t *testing.T) {
	code, out, _ := runCLI(t, "run", ex+"puzzles/double-input.yaml", ex+"solutions/invalid/double-input-deadlock.yaml")
	if code != exitFailure || !strings.Contains(out, "deadlocked") || !strings.Contains(out, "cannot write to P2: port is not connected") {
		t.Fatalf("%d\n%s", code, out)
	}
	code, out, _ = runCLI(t, "run", ex+"puzzles/double-input.yaml", ex+"solutions/invalid/double-input-deadlock.yaml", "--json", "--snapshot")
	var v struct {
		Result   engine.TestResult `json:"result"`
		Snapshot *engine.Snapshot  `json:"snapshot"`
	}
	if err := json.Unmarshal([]byte(out), &v); err != nil || v.Result.Status != engine.StatusDeadlocked || v.Snapshot == nil {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestCLIMisc(t *testing.T) {
	if code, out, _ := runCLI(t, "types"); code != 0 || !strings.Contains(out, "PUSH") || !strings.Contains(out, "stack_size") {
		t.Fatalf("types: %d\n%s", code, out)
	}
	if code, _, _ := runCLI(t, "version"); code != 0 {
		t.Fatal("version failed")
	}
	if code, _, _ := runCLI(t, "bogus"); code != exitUsage {
		t.Fatal("unknown command should be a usage error")
	}
	if code, _, _ := runCLI(t, "test", "only-one.yaml"); code != exitUsage {
		t.Fatal("missing args should be a usage error")
	}
	if code, _, errs := runCLI(t, "validate", "does-not-exist.yaml"); code != exitFailure || !strings.Contains(errs, "no such file") {
		t.Fatalf("missing file: %d %s", code, errs)
	}
	if code, _, errs := runCLI(t, "test", ex+"puzzles/double-input.yaml", ex+"solutions/double-input.yaml", "--plugin", "/nonexistent.so"); code != exitUsage || !strings.Contains(errs, "plugin") {
		t.Fatalf("bad plugin: %d %s", code, errs)
	}
	code, out, _ := runCLI(t, "run", ex+"puzzles/filter-positive.yaml", ex+"solutions/filter-positive.yaml", "--test", "extremes")
	if code != 0 || !strings.Contains(out, "extremes") {
		t.Fatalf("run --test: %d %s", code, out)
	}
}

func TestServeStdio(t *testing.T) {
	var out, errb bytes.Buffer
	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"shutdown"}` + "\n")
	if code := run([]string{"serve", "--stdio"}, in, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"protocolVersion":"2"`) {
		t.Fatalf("stdout must carry only protocol messages:\n%s", out.String())
	}
	if !strings.Contains(errb.String(), "serving JSON-RPC") {
		t.Fatal("logs should go to stderr")
	}
}

// The example plugin also works when linked statically, on every platform.
func TestStaticPluginExample(t *testing.T) {
	reg := cells.NewRegistry()
	if err := plugins.LoadAll(reg, plugins.Static{ID: "multiplier", Register: multiplier.Register}); err != nil {
		t.Fatal(err)
	}
	p, l := puzzle.LoadFile(ex + "plugins/multiplier/puzzle.yaml")
	if l.HasErrors() {
		t.Fatal(l)
	}
	s, _ := solution.LoadFile(ex + "plugins/multiplier/solution.yaml")
	bp, l := engine.Prepare(reg, p, s)
	if l.HasErrors() {
		t.Fatal(l)
	}
	res, err := engine.RunAll(bp, engine.Options{})
	if err != nil || !res.Passed {
		t.Fatalf("%v %+v", err, res)
	}
	// Without the plugin, the puzzle references an unknown type.
	if l := p.Validate(cells.NewRegistry()); !l.HasErrors() {
		t.Fatal("expected unknown type error")
	}
}

func TestMain(m *testing.M) {
	os.Unsetenv("ASMGRAPH_PLUGINS")
	os.Exit(m.Run())
}

func TestMigrateAndScoreOutput(t *testing.T) {
	code, out, errs := runCLI(t, "migrate", ex+"puzzles/reverse-sequences.yaml")
	if code != 0 || !strings.Contains(out, "schema_version: 2") || !strings.Contains(out, "type: stack_processor") ||
		!strings.Contains(errs, "stack → stack_processor") {
		t.Fatalf("migrate: %d\n%s%s", code, out, errs)
	}
	if code, _, errs := runCLI(t, "migrate", ex+"puzzles/stack-reverse.yaml"); code != 0 || !strings.Contains(errs, "already schema v2") {
		t.Fatalf("migrate v2: %d %s", code, errs)
	}
	code, out, _ = runCLI(t, "test", ex+"puzzles/stack-reverse.yaml", ex+"solutions/stack-reverse.yaml")
	if code != 0 || !strings.Contains(out, "hardware cost: 32 of 50") || !strings.Contains(out, "score: cost 32") {
		t.Fatalf("test: %d\n%s", code, out)
	}
	code, out, _ = runCLI(t, "types")
	if code != 0 || !strings.Contains(out, "port function PUSH") || !strings.Contains(out, "hardware: peripheral, ports 2-8") {
		t.Fatalf("types: %d\n%s", code, out)
	}
	code, out, _ = runCLI(t, "validate", ex+"puzzles/stack-reverse.yaml", ex+"solutions/invalid/stack-reverse-over-budget.yaml")
	if code != 1 {
		t.Fatalf("invalid hardware should fail validation: %d", code)
	}
}
