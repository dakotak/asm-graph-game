package protocol

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"asmgraph/cells"
	"asmgraph/engine"
)

func call(t *testing.T, s *Service, method string, params any) (any, *Error) {
	t.Helper()
	b, _ := json.Marshal(params)
	return s.Call(method, b)
}

// The stateless methods the in-browser (WebAssembly) engine relies on to
// build its puzzle library and score solutions without a server.
func TestStatelessLibraryMethods(t *testing.T) {
	s := NewService(cells.NewRegistry(), nil)
	puz, _ := os.ReadFile("../examples/puzzles/polynomial-design.yaml")
	sol, _ := os.ReadFile("../examples/solutions/polynomial-design-fast.yaml")

	r, e := call(t, s, "summarizePuzzle", map[string]string{"yaml": string(puz), "file": "polynomial-design.yaml"})
	if e != nil || !strings.Contains(mustJSON(t, r), `"id":"polynomial-design"`) {
		t.Fatalf("summarizePuzzle: %v %v", e, r)
	}
	r, e = call(t, s, "evaluateSolution", map[string]string{"puzzle": string(puz), "solution": string(sol)})
	if e != nil {
		t.Fatal(e)
	}
	res := r.(*engine.SuiteResult)
	if !res.Passed || res.Score == nil || *res.Score != (engine.Score{Cost: 54, Ticks: 28, Bytes: 24}) {
		t.Errorf("evaluateSolution: %+v", res)
	}
	// It does not touch the interactive session.
	if s.Session().Puzzle() != nil {
		t.Error("evaluateSolution loaded a puzzle into the session")
	}
	bad := strings.Replace(string(sol), "port_count: 2", "port_count: 3", 1)
	if _, e = call(t, s, "evaluateSolution", map[string]string{"puzzle": string(puz), "solution": bad}); e == nil || e.Code != CodeValidationFailed {
		t.Errorf("invalid solution: %v", e)
	}
}

func mustJSON(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
