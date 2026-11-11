package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"asmgraph/cells"
)

func newServer(t *testing.T, buffer int) (*httptest.Server, string) {
	t.Helper()
	sol := t.TempDir()
	s, err := New(Config{Registry: cells.NewRegistry(), PuzzlesDir: "../../examples/puzzles", SolutionsDir: sol, EventBuffer: buffer})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, sol
}

func getJSON(t *testing.T, url string, want int) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	if resp.StatusCode != want {
		t.Fatalf("GET %s: status %d, want %d: %v", url, resp.StatusCode, want, m)
	}
	return m
}

func put(t *testing.T, url, yaml string) (int, map[string]any) {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"yaml": yaml})
	req, _ := http.NewRequest(http.MethodPut, url, bytes.NewReader(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&m)
	return resp.StatusCode, m
}

func TestPuzzleLibrary(t *testing.T) {
	ts, _ := newServer(t, 0)
	list := getJSON(t, ts.URL+"/api/puzzles", 200)["puzzles"].([]any)
	if len(list) < 5 {
		t.Fatalf("puzzles: %v", list)
	}
	var found map[string]any
	for _, p := range list {
		if p.(map[string]any)["id"] == "reverse-sequences" {
			found = p.(map[string]any)
		}
	}
	if found == nil || found["valid"] != true || found["tests"] != float64(3) || found["programmableCells"] != float64(2) {
		t.Fatalf("summary: %v", found)
	}
	p := getJSON(t, ts.URL+"/api/puzzles/double-input", 200)
	if !strings.Contains(p["yaml"].(string), "tests:") || p["puzzle"].(map[string]any)["id"] != "double-input" {
		t.Fatalf("puzzle: %v", p)
	}
	getJSON(t, ts.URL+"/api/puzzles/nope", 404)
	getJSON(t, ts.URL+"/api/nothing", 404)
}

func TestSolutionStorage(t *testing.T) {
	ts, dir := newServer(t, 0)
	data, _ := os.ReadFile("../../examples/solutions/double-input.yaml")
	if code, m := put(t, ts.URL+"/api/puzzles/double-input/solutions/main", string(data)); code != 200 {
		t.Fatalf("put: %d %v", code, m)
	}
	if _, err := os.Stat(filepath.Join(dir, "double-input", "main.yaml")); err != nil {
		t.Fatal(err)
	}
	list := getJSON(t, ts.URL+"/api/puzzles/double-input/solutions", 200)["solutions"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name"] != "main" {
		t.Fatalf("list: %v", list)
	}
	got := getJSON(t, ts.URL+"/api/puzzles/double-input/solutions/main", 200)
	if got["yaml"] != string(data) {
		t.Fatal("round trip mismatch")
	}
	// Wrong puzzle, invalid YAML, unknown puzzle.
	if code, _ := put(t, ts.URL+"/api/puzzles/filter-positive/solutions/x", string(data)); code != 422 {
		t.Fatalf("wrong puzzle accepted: %d", code)
	}
	if code, m := put(t, ts.URL+"/api/puzzles/double-input/solutions/x", "cells: []\n"); code != 422 || m["diagnostics"] == nil {
		t.Fatalf("invalid yaml: %d %v", code, m)
	}
	if code, _ := put(t, ts.URL+"/api/puzzles/ghost/solutions/x", string(data)); code != 404 {
		t.Fatalf("unknown puzzle: %d", code)
	}
}

func TestPathTraversalRejected(t *testing.T) {
	ts, _ := newServer(t, 0)
	for _, u := range []string{
		"/api/puzzles/double-input/solutions/..%2F..%2Fetc%2Fpasswd",
		"/api/puzzles/..%2F..%2Fetc/solutions/x",
		"/api/puzzles/double-input/solutions/.hidden",
		"/api/puzzles/double-input/solutions/a..b",
	} {
		resp, err := http.Get(ts.URL + u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 && resp.StatusCode != 404 {
			t.Errorf("%s: status %d", u, resp.StatusCode)
		}
	}
	if code, _ := put(t, ts.URL+"/api/puzzles/double-input/solutions/..%2Fescape", "schema_version: 1\npuzzle: double-input\n"); code != 400 {
		t.Errorf("traversal put: %d", code)
	}
}

func TestHostCheck(t *testing.T) {
	ts, _ := newServer(t, 0)
	req, _ := http.NewRequest("GET", ts.URL+"/api/health", nil)
	req.Host = "evil.example.com"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("foreign host accepted: %d", resp.StatusCode)
	}
	if h := getJSON(t, ts.URL+"/api/health", 200); h["protocolVersion"] != "1" {
		t.Fatal(h)
	}
}

func TestStatelessRPC(t *testing.T) {
	ts, _ := newServer(t, 0)
	call := func(body string) map[string]any {
		resp, err := http.Post(ts.URL+"/api/rpc", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&m)
		return m
	}
	r := call(`{"jsonrpc":"2.0","id":1,"method":"assemble","params":{"type":"standard","source":"MOV P0, ACC\nADD 64","memoryLimit":32}}`)
	res := r["result"].(map[string]any)
	if res["ok"] != true || res["bytes"] != float64(7) {
		t.Fatalf("assemble: %v", r)
	}
	r = call(`{"jsonrpc":"2.0","id":2,"method":"assemble","params":{"type":"standard","source":"MOV P0, ACC\nADD 64","memoryLimit":5}}`)
	res = r["result"].(map[string]any)
	d := res["diagnostics"].([]any)[0].(map[string]any)
	if res["ok"] != false || res["bytes"] != float64(7) || d["sourceLine"] != float64(2) {
		t.Fatalf("over limit: %v", r)
	}
	r = call(`{"jsonrpc":"2.0","id":3,"method":"assemble","params":{"type":"stack","source":"SAV"}}`)
	if r["result"].(map[string]any)["ok"] != false {
		t.Fatalf("SAV on stack: %v", r)
	}
	// Stateful methods are not available over HTTP.
	r = call(`{"jsonrpc":"2.0","id":4,"method":"start"}`)
	if r["error"].(map[string]any)["code"] != float64(-32601) {
		t.Fatalf("start over http: %v", r)
	}
	r = call(`{"jsonrpc":"2.0","id":5,"method":"listCellTypes"}`)
	lt := r["result"].(map[string]any)
	if lt["operandKinds"] == nil || len(lt["ports"].([]any)) != 6 || len(lt["types"].([]any)) != 4 {
		t.Fatalf("listCellTypes: %v", lt)
	}
}

type wsClient struct {
	t      *testing.T
	c      *websocket.Conn
	ctx    context.Context
	events []map[string]any
}

func dial(t *testing.T, ts *httptest.Server) *wsClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/ws", nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(16 << 20)
	t.Cleanup(func() { c.CloseNow() })
	return &wsClient{t: t, c: c, ctx: ctx}
}

var nextID = 0

func (w *wsClient) call(method string, params any) map[string]any {
	w.t.Helper()
	nextID++
	id := nextID
	msg := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		msg["params"] = params
	}
	b, _ := json.Marshal(msg)
	if err := w.c.Write(w.ctx, websocket.MessageText, b); err != nil {
		w.t.Fatal(err)
	}
	for {
		_, data, err := w.c.Read(w.ctx)
		if err != nil {
			w.t.Fatal(err)
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		if m["method"] == "event" {
			w.events = append(w.events, m["params"].(map[string]any))
			continue
		}
		if m["id"] == float64(id) {
			return m
		}
	}
}

func (w *wsClient) result(method string, params any) map[string]any {
	w.t.Helper()
	m := w.call(method, params)
	if m["error"] != nil {
		w.t.Fatalf("%s: %v", method, m["error"])
	}
	r, _ := m["result"].(map[string]any)
	return r
}

func load(t *testing.T, w *wsClient, puzzle, sol string) {
	t.Helper()
	p, _ := os.ReadFile("../../examples/puzzles/" + puzzle)
	s, _ := os.ReadFile("../../examples/solutions/" + sol)
	w.result("loadPuzzle", map[string]string{"yaml": string(p)})
	w.result("loadSolution", map[string]string{"yaml": string(s)})
}

func TestWebSocketSession(t *testing.T) {
	ts, _ := newServer(t, 0)
	w := dial(t, ts)
	if r := w.result("initialize", nil); r["protocolVersion"] != "1" {
		t.Fatal(r)
	}
	// File paths are not accepted from browsers.
	if m := w.call("loadPuzzle", map[string]string{"path": "../../examples/puzzles/double-input.yaml"}); m["error"] == nil {
		t.Fatal("path loading should be disabled over WebSocket")
	}
	load(t, w, "double-input.yaml", "double-input.yaml")
	if r := w.result("compile", nil); r["totalBytes"] != float64(10) {
		t.Fatal(r)
	}
	w.result("start", map[string]any{"test": "positive"})
	if r := w.result("step", map[string]any{"ticks": 2}); r["tick"] != float64(2) {
		t.Fatal(r)
	}
	if r := w.result("runTest", map[string]any{"test": "boundaries"}); r["status"] != "passed" || r["test"] != "boundaries" {
		t.Fatalf("runTest: %v", r)
	}
	// runTest did not disturb the interactive machine.
	if s := w.result("getState", nil); s["tick"] != float64(2) || s["test"] != "positive" {
		t.Fatalf("interactive machine disturbed: %v", s["tick"])
	}
	if len(w.events) == 0 {
		t.Fatal("no events")
	}
}

func TestBreakpoints(t *testing.T) {
	ts, _ := newServer(t, 0)
	w := dial(t, ts)
	load(t, w, "double-input.yaml", "double-input.yaml")
	w.result("setEventLevel", map[string]string{"level": "lifecycle"})
	w.result("setBreakpoints", map[string]any{"breakpoints": map[string][]int{"processor": {3}}})
	w.result("start", nil)
	r := w.result("run", map[string]any{"wait": true})
	if r["status"] != "running" || r["tick"] != float64(2) {
		t.Fatalf("should pause before line 3 at tick 2: %v", r)
	}
	cell := w.result("getCell", map[string]string{"cell": "processor"})
	if cell["sourceLine"] != float64(3) {
		t.Fatalf("paused at line %v", cell["sourceLine"])
	}
	var paused map[string]any
	for _, e := range w.events {
		if e["type"] == "simulation.paused" {
			paused = e["data"].(map[string]any)
		}
	}
	if paused == nil || paused["reason"] != "breakpoint" || paused["line"] != float64(3) || paused["cell"] != "processor" {
		t.Fatalf("paused event: %v", paused)
	}
	// Continuing runs to the next hit, and clearing breakpoints runs to the end.
	if r := w.result("run", map[string]any{"wait": true}); r["tick"] != float64(6) {
		t.Fatalf("second hit at %v", r["tick"])
	}
	w.result("setBreakpoints", map[string]any{"breakpoints": map[string][]int{}})
	if r := w.result("run", map[string]any{"wait": true}); r["status"] != "passed" {
		t.Fatal(r)
	}
	if m := w.call("setBreakpoints", map[string]any{"breakpoints": map[string][]int{"x": {0}}}); m["error"] == nil {
		t.Fatal("line 0 accepted")
	}
}

// With a tiny outbound buffer and a client that does not read during a
// fast run, events are dropped instead of blocking the simulation, and the
// gap is visible through seq.
func TestBackpressureDropsEventsWithVisibleGap(t *testing.T) {
	ts, _ := newServer(t, 2)
	w := dial(t, ts)
	load(t, w, "reverse-sequences.yaml", "reverse-sequences.yaml")
	w.result("start", map[string]any{"test": "full-stack"})
	r := w.result("run", map[string]any{"wait": true}) // emits hundreds of events at level "all"
	if r["status"] != "passed" {
		t.Fatal(r)
	}
	w.result("getState", nil)
	var last float64
	gap := false
	for _, e := range w.events {
		if s := e["seq"].(float64); s != last+1 {
			gap = true
		}
		last = e["seq"].(float64)
	}
	if !gap {
		t.Fatalf("expected dropped events (got %d contiguous events)", len(w.events))
	}
}

func TestStaticSPA(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>app</html>"), 0o644)
	os.MkdirAll(filepath.Join(dir, "assets"), 0o755)
	os.WriteFile(filepath.Join(dir, "assets", "a.js"), []byte("js"), 0o644)
	s, _ := New(Config{Registry: cells.NewRegistry(), PuzzlesDir: "../../examples/puzzles", SolutionsDir: t.TempDir(), StaticDir: dir})
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for path, want := range map[string]string{"/": "app", "/puzzle/x": "app", "/assets/a.js": "js", "/../../etc/passwd": "app"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		b.ReadFrom(resp.Body)
		resp.Body.Close()
		if !strings.Contains(b.String(), want) {
			t.Errorf("%s: got %q", path, b.String())
		}
	}
}
