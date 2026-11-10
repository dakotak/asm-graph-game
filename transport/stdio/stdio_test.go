package stdio_test

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"asmgraph/cells"
	"asmgraph/transport/stdio"
)

type client struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *bufio.Scanner
	done chan error
	// notifications received while waiting for responses
	events []map[string]any
}

func start(t *testing.T) *client {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	srv := stdio.New(inR, outW, nil, cells.NewRegistry())
	c := &client{t: t, in: inW, out: bufio.NewScanner(outR), done: make(chan error, 1)}
	c.out.Buffer(make([]byte, 1<<20), 16<<20)
	go func() {
		err := srv.Serve()
		outW.Close()
		c.done <- err
	}()
	return c
}

func (c *client) send(line string) {
	c.t.Helper()
	if _, err := io.WriteString(c.in, line+"\n"); err != nil {
		c.t.Fatal(err)
	}
}

// next returns the next non-notification message.
func (c *client) next() map[string]any {
	c.t.Helper()
	for c.out.Scan() {
		var m map[string]any
		if err := json.Unmarshal(c.out.Bytes(), &m); err != nil {
			c.t.Fatalf("server wrote invalid JSON: %s", c.out.Text())
		}
		if m["method"] == "event" {
			c.events = append(c.events, m)
			continue
		}
		return m
	}
	c.t.Fatal("server closed output")
	return nil
}

func (c *client) call(id int, method, params string) map[string]any {
	c.t.Helper()
	req := `{"jsonrpc":"2.0","id":` + itoa(id) + `,"method":"` + method + `"`
	if params != "" {
		req += `,"params":` + params
	}
	c.send(req + "}")
	resp := c.next()
	if resp["id"] != float64(id) {
		c.t.Fatalf("response id %v, want %d: %v", resp["id"], id, resp)
	}
	return resp
}

func itoa(i int) string { b, _ := json.Marshal(i); return string(b) }

func result(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if resp["error"] != nil {
		t.Fatalf("unexpected error: %v", resp["error"])
	}
	if _, has := resp["error"]; has {
		t.Fatal("success response must not contain error")
	}
	r, _ := resp["result"].(map[string]any)
	return r
}

func errCode(t *testing.T, resp map[string]any) float64 {
	t.Helper()
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error response, got %v", resp)
	}
	if _, has := resp["result"]; has {
		t.Fatal("error response must not contain result")
	}
	return e["code"].(float64)
}

func yamlParam(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]string{"yaml": string(b)})
	return string(p)
}

func TestJSONRPCSession(t *testing.T) {
	c := start(t)
	init := result(t, c.call(1, "initialize", ""))
	if init["protocolVersion"] != "1" {
		t.Fatalf("initialize: %v", init)
	}
	if r := result(t, c.call(2, "loadPuzzle", yamlParam(t, "../../examples/puzzles/double-input.yaml"))); r["puzzle"] == nil {
		t.Fatal("loadPuzzle returned no puzzle")
	}
	result(t, c.call(3, "loadSolution", `{"path":"../../examples/solutions/double-input.yaml"}`))
	comp := result(t, c.call(4, "compile", ""))
	if comp["totalBytes"] != float64(10) {
		t.Fatalf("compile: %v", comp)
	}
	snap := result(t, c.call(5, "start", `{"test":"negative"}`))
	if snap["test"] != "negative" || snap["tick"] != float64(0) {
		t.Fatalf("start: %v", snap)
	}
	step := result(t, c.call(6, "step", `{"ticks":3,"snapshot":true}`))
	if step["tick"] != float64(3) || step["snapshot"] == nil {
		t.Fatalf("step: %v", step)
	}
	cell := result(t, c.call(7, "getCell", `{"cell":"processor"}`))
	if cell["state"].(map[string]any)["ACC"] != float64(-10) {
		t.Fatalf("getCell: %v", cell)
	}
	run := result(t, c.call(8, "run", `{"wait":true}`))
	if run["status"] != "passed" {
		t.Fatalf("run: %v", run)
	}
	tests := result(t, c.call(9, "runTests", ""))
	if tests["passed"] != true {
		t.Fatalf("runTests: %v", tests)
	}
	// Incremental events arrived as notifications with gap-free sequence numbers.
	if len(c.events) == 0 {
		t.Fatal("no events received")
	}
	types := map[string]bool{}
	for i, e := range c.events {
		p := e["params"].(map[string]any)
		types[p["type"].(string)] = true
		if p["seq"] != float64(i+1) {
			t.Fatalf("event %d has seq %v", i, p["seq"])
		}
		if e["id"] != nil {
			t.Fatal("notifications must not have an id")
		}
	}
	for _, want := range []string{"simulation.started", "tick.completed", "port.transfer", "instruction.completed", "cell.state", "output.validation", "simulation.completed", "test.completed"} {
		if !types[want] {
			t.Errorf("missing %s event", want)
		}
	}
	result(t, c.call(10, "shutdown", ""))
	select {
	case err := <-c.done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestEventLevels(t *testing.T) {
	c := start(t)
	c.call(1, "loadPuzzle", `{"path":"../../examples/puzzles/double-input.yaml"}`)
	c.call(2, "loadSolution", `{"path":"../../examples/solutions/double-input.yaml"}`)
	result(t, c.call(3, "setEventLevel", `{"level":"lifecycle"}`))
	c.call(4, "start", "")
	c.call(5, "run", `{"wait":true}`)
	for _, e := range c.events {
		typ := e["params"].(map[string]any)["type"]
		if typ == "tick.completed" || typ == "cell.state" || typ == "port.transfer" {
			t.Fatalf("lifecycle level forwarded %s", typ)
		}
	}
	if len(c.events) != 2 { // simulation.started + simulation.completed
		t.Fatalf("got %d events", len(c.events))
	}
	if code := errCode(t, c.call(6, "setEventLevel", `{"level":"loud"}`)); code != -32602 {
		t.Fatalf("code %v", code)
	}
	c.in.Close()
	<-c.done
}

func TestAsyncRunAndPause(t *testing.T) {
	c := start(t)
	c.call(1, "loadPuzzle", `{"path":"../../examples/puzzles/double-input.yaml"}`)
	c.call(2, "loadSolution", `{"path":"../../examples/solutions/double-input.yaml"}`)
	c.call(3, "setEventLevel", `{"level":"none"}`)
	c.call(4, "start", "")
	if r := result(t, c.call(5, "run", `{"delayMs":20}`)); r["running"] != true {
		t.Fatalf("run: %v", r)
	}
	time.Sleep(50 * time.Millisecond)
	p := result(t, c.call(6, "pause", ""))
	if p["status"] != "running" || p["tick"].(float64) < 1 {
		t.Fatalf("pause: %v", p)
	}
	tick := p["tick"]
	time.Sleep(50 * time.Millisecond)
	if s := result(t, c.call(7, "getState", "")); s["tick"] != tick {
		t.Fatalf("advanced while paused: %v -> %v", tick, s["tick"])
	}
	c.in.Close()
	<-c.done
}

func TestInvalidRequests(t *testing.T) {
	c := start(t)
	c.send(`{not json`)
	if code := errCode(t, c.next()); code != -32700 {
		t.Fatalf("parse error code %v", code)
	}
	c.send(`[]`)
	if code := errCode(t, c.next()); code != -32600 {
		t.Fatalf("empty batch code %v", code)
	}
	c.send(`{"id":1,"method":"initialize"}`)
	resp := c.next()
	if code := errCode(t, resp); code != -32600 || resp["id"] != float64(1) {
		t.Fatalf("missing jsonrpc: %v", resp)
	}
	c.send(`{"jsonrpc":"2.0","id":{"x":1},"method":"initialize"}`)
	if code := errCode(t, c.next()); code != -32600 {
		t.Fatal("object id accepted")
	}
	c.send(`"just a string"`)
	if code := errCode(t, c.next()); code != -32600 {
		t.Fatal("non-object request accepted")
	}
	if code := errCode(t, c.call(2, "frobnicate", "")); code != -32601 {
		t.Fatalf("method not found code %v", code)
	}
	if code := errCode(t, c.call(3, "step", `{"tickz":1}`)); code != -32602 {
		t.Fatalf("unknown param code %v", code)
	}
	if code := errCode(t, c.call(4, "step", "")); code != -32003 {
		t.Fatalf("step before start code %v", code)
	}
	resp = c.call(5, "loadPuzzle", `{"yaml":"schema_version: 9\nid: x\ncells: []\ntests: []\n"}`)
	if code := errCode(t, resp); code != -32001 {
		t.Fatalf("invalid puzzle code %v", code)
	}
	diags := resp["error"].(map[string]any)["data"].(map[string]any)["diagnostics"].([]any)
	if len(diags) < 3 {
		t.Fatalf("diagnostics: %v", diags)
	}
	// Notifications (no id) get no response, even when they fail.
	c.send(`{"jsonrpc":"2.0","method":"frobnicate"}`)
	c.send(`{"jsonrpc":"2.0","method":"initialize"}`)
	// Batches return an array of responses for the requests only.
	c.send(`[{"jsonrpc":"2.0","id":"a","method":"initialize"},{"jsonrpc":"2.0","method":"initialize"},{"jsonrpc":"2.0","id":"b","method":"nope"}]`)
	if !c.out.Scan() {
		t.Fatal("no batch response")
	}
	var batch []map[string]any
	if err := json.Unmarshal(c.out.Bytes(), &batch); err != nil || len(batch) != 2 || batch[0]["id"] != "a" || batch[1]["id"] != "b" {
		t.Fatalf("batch response: %s", c.out.Text())
	}
	c.in.Close()
	if err := <-c.done; err != nil {
		t.Fatal(err)
	}
}

func TestCompileFailureDiagnostics(t *testing.T) {
	c := start(t)
	c.call(1, "loadPuzzle", `{"path":"../../examples/puzzles/distributed-polynomial.yaml"}`)
	c.call(2, "loadSolution", `{"path":"../../examples/solutions/invalid/distributed-too-big.yaml"}`)
	resp := c.call(3, "compile", "")
	if code := errCode(t, resp); code != -32002 {
		t.Fatalf("code %v", code)
	}
	d := resp["error"].(map[string]any)["data"].(map[string]any)["diagnostics"].([]any)[0].(map[string]any)
	if d["cell"] != "alpha" || !strings.Contains(d["message"].(string), "program is 12 bytes") || d["sourceLine"] != float64(4) {
		t.Fatalf("diagnostic: %v", d)
	}
	c.in.Close()
	<-c.done
}
