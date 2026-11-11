// Command asmgraph is the headless CLI for the asmgraph puzzle engine.
//
//	asmgraph validate PUZZLE.yaml [SOLUTION.yaml]
//	asmgraph compile  PUZZLE.yaml SOLUTION.yaml [--disasm]
//	asmgraph run      PUZZLE.yaml SOLUTION.yaml [--test NAME] [--trace]
//	asmgraph test     PUZZLE.yaml SOLUTION.yaml
//	asmgraph types
//	asmgraph serve --stdio
//
// Every command accepts --json for machine-readable output and --plugin
// PATH (repeatable) to load native cell plugins.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"asmgraph/cell"
	"asmgraph/cells"
	"asmgraph/diag"
	"asmgraph/engine"
	"asmgraph/plugins"
	"asmgraph/protocol"
	"asmgraph/puzzle"
	"asmgraph/solution"
	"asmgraph/transport/stdio"
	webtransport "asmgraph/transport/web"
)

// Exit codes.
const (
	exitOK      = 0
	exitFailure = 1 // invalid input, compile errors, failing tests
	exitUsage   = 2 // bad command line or I/O error
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

type cli struct {
	stdin       io.Reader
	stdout      io.Writer
	stderr      io.Writer
	jsonOut     bool
	pluginPaths multiFlag
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	c := &cli{stdin: stdin, stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		c.usage()
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "validate":
		return c.validate(rest)
	case "compile":
		return c.compile(rest)
	case "run":
		return c.runOne(rest)
	case "test":
		return c.test(rest)
	case "types":
		return c.types(rest)
	case "migrate":
		return c.migrate(rest)
	case "serve":
		return c.serve(rest)
	case "web":
		return c.web(rest)
	case "version", "--version", "-v":
		fmt.Fprintf(stdout, "%s %s (protocol %s)\n", protocol.ServerName, protocol.ServerVersion, protocol.Version)
		return exitOK
	case "help", "-h", "--help":
		c.usage()
		return exitOK
	}
	fmt.Fprintf(stderr, "asmgraph: unknown command %q\n\n", cmd)
	c.usage()
	return exitUsage
}

func (c *cli) usage() {
	fmt.Fprint(c.stderr, `usage: asmgraph <command> [flags] [args]

commands:
  validate PUZZLE [SOLUTION]   validate a puzzle (and optionally a solution)
  compile  PUZZLE SOLUTION     compile programs and report memory usage
  run      PUZZLE SOLUTION     run one test case (--test, --trace)
  test     PUZZLE SOLUTION     run every test case
  types                        list registered cell types, hardware limits and instructions
  migrate  FILE...             rewrite schema-v1 puzzles/solutions as schema v2 (--write)
  serve --stdio                serve JSON-RPC 2.0 on stdin/stdout
  web                          serve the browser IDE and HTTP/WebSocket API
  version                      print version information

common flags:
  --json          machine-readable output
  --plugin PATH   load a native cell plugin (repeatable)
`)
}

// flags creates a flag set with the common flags. Flags may appear before
// or after positional arguments.
func (c *cli) flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(c.stderr)
	fs.BoolVar(&c.jsonOut, "json", false, "machine-readable JSON output")
	fs.Var(&c.pluginPaths, "plugin", "load a native cell plugin (repeatable)")
	return fs
}

func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

func (c *cli) registry() (*cell.Registry, error) {
	reg := cells.NewRegistry()
	var loaders []plugins.Loader
	for _, p := range c.pluginPaths {
		loaders = append(loaders, plugins.Native{Path: p})
	}
	if env := os.Getenv("ASMGRAPH_PLUGINS"); env != "" {
		for _, p := range strings.Split(env, string(os.PathListSeparator)) {
			if p != "" {
				loaders = append(loaders, plugins.Native{Path: p})
			}
		}
	}
	return reg, plugins.LoadAll(reg, loaders...)
}

func (c *cli) emitJSON(v any) {
	enc := json.NewEncoder(c.stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (c *cli) printDiags(l diag.List) {
	for _, d := range l {
		fmt.Fprintln(c.stderr, d.String())
	}
}

func (c *cli) fail(err error) int {
	if c.jsonOut {
		c.emitJSON(map[string]any{"ok": false, "error": err.Error()})
	} else {
		fmt.Fprintf(c.stderr, "asmgraph: %v\n", err)
	}
	return exitUsage
}

func (c *cli) diagFail(stage string, l diag.List) int {
	if c.jsonOut {
		c.emitJSON(map[string]any{"ok": false, "stage": stage, "diagnostics": l})
	} else {
		c.printDiags(l)
		n := 0
		for _, d := range l {
			if d.Severity == diag.Error {
				n++
			}
		}
		fmt.Fprintf(c.stderr, "%s failed: %d error(s)\n", stage, n)
	}
	return exitFailure
}

// load parses and validates a puzzle and (optionally) a solution, then
// prepares the blueprint when a solution is given.
func (c *cli) load(reg *cell.Registry, puzzlePath, solutionPath string) (*puzzle.Puzzle, *engine.Blueprint, diag.List, string) {
	p, l := puzzle.LoadFile(puzzlePath)
	if l.HasErrors() {
		return nil, nil, l, "puzzle validation"
	}
	if l = p.Validate(reg); l.HasErrors() {
		return nil, nil, l, "puzzle validation"
	}
	if solutionPath == "" {
		return p, nil, l, ""
	}
	s, sl := solution.LoadFile(solutionPath)
	if sl.HasErrors() {
		return nil, nil, sl, "solution validation"
	}
	bp, bl := engine.Prepare(reg, p, s)
	if bl.HasErrors() {
		return nil, nil, bl, "solution validation/compilation"
	}
	return p, bp, append(l, bl...), ""
}

func (c *cli) validate(args []string) int {
	fs := c.flags("validate")
	pos, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) < 1 || len(pos) > 2 {
		fmt.Fprintln(c.stderr, "usage: asmgraph validate PUZZLE.yaml [SOLUTION.yaml] [--json]")
		return exitUsage
	}
	reg, err := c.registry()
	if err != nil {
		return c.fail(err)
	}
	sol := ""
	if len(pos) == 2 {
		sol = pos[1]
	}
	p, bp, l, stage := c.load(reg, pos[0], sol)
	if stage != "" {
		if c.jsonOut {
			c.emitJSON(map[string]any{"valid": false, "stage": stage, "diagnostics": l})
			return exitFailure
		}
		return c.diagFail(stage, l)
	}
	if c.jsonOut {
		out := map[string]any{"valid": true, "puzzle": p.ID, "tests": len(p.Tests), "cells": len(p.Cells), "diagnostics": nonNil(l)}
		if bp != nil {
			out["programBytes"] = bp.TotalProgramBytes()
			out["hardware"] = bp.Hardware
		}
		c.emitJSON(out)
		return exitOK
	}
	c.printDiags(l)
	fmt.Fprintf(c.stdout, "puzzle %s (%s): valid — %d cells, %d test cases\n", p.ID, p.Name, len(p.Cells), len(p.Tests))
	if bp != nil {
		fmt.Fprintf(c.stdout, "solution: valid — %d connections, %d program bytes\n", bp.Topology.NumConnections(), bp.TotalProgramBytes())
		printHardware(c.stdout, bp.Hardware)
	}
	return exitOK
}

func (c *cli) compile(args []string) int {
	fs := c.flags("compile")
	disasm := fs.Bool("disasm", false, "print disassembly")
	pos, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 2 {
		fmt.Fprintln(c.stderr, "usage: asmgraph compile PUZZLE.yaml SOLUTION.yaml [--disasm] [--json]")
		return exitUsage
	}
	reg, err := c.registry()
	if err != nil {
		return c.fail(err)
	}
	_, bp, l, stage := c.load(reg, pos[0], pos[1])
	if stage != "" {
		return c.diagFail(stage, l)
	}
	progs := bp.Programs()
	if c.jsonOut {
		c.emitJSON(map[string]any{"ok": true, "programs": progs, "totalBytes": bp.TotalProgramBytes(), "diagnostics": nonNil(l)})
		return exitOK
	}
	for _, p := range progs {
		fmt.Fprintf(c.stdout, "%-16s %-10s %4d / %-4d bytes  %3d instructions  %s\n", p.Cell, p.Type, p.Bytes, p.Limit, p.Instructions, p.Hex)
		if *disasm {
			for _, d := range p.Disassembly {
				fmt.Fprintf(c.stdout, "    %3d  @%-4d %-14s %-20s ; line %d\n", d.Index, d.Address, d.Bytes, d.Text, d.SourceLine)
			}
		}
	}
	fmt.Fprintf(c.stdout, "total: %d program bytes\n", bp.TotalProgramBytes())
	return exitOK
}

func (c *cli) runOne(args []string) int {
	fs := c.flags("run")
	testSel := fs.String("test", "", "test case name or index (default: first)")
	trace := fs.Bool("trace", false, "print committed events tick by tick")
	maxTicks := fs.Int64("max-ticks", 0, "override the puzzle tick limit")
	cycles := fs.Bool("cycles", false, "enable exact repeated-state (cycle) detection")
	snapshot := fs.Bool("snapshot", false, "print the final machine snapshot")
	pos, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 2 {
		fmt.Fprintln(c.stderr, "usage: asmgraph run PUZZLE.yaml SOLUTION.yaml [--test NAME] [--trace] [--max-ticks N] [--cycles] [--json]")
		return exitUsage
	}
	reg, err := c.registry()
	if err != nil {
		return c.fail(err)
	}
	_, bp, l, stage := c.load(reg, pos[0], pos[1])
	if stage != "" {
		return c.diagFail(stage, l)
	}
	idx, err := bp.TestIndex(*testSel)
	if err != nil {
		return c.fail(err)
	}
	opts := engine.Options{MaxTicks: *maxTicks, CycleDetection: *cycles}
	var events []engine.Event
	if *trace {
		opts.Events = func(e engine.Event) {
			if c.jsonOut {
				events = append(events, e)
			} else {
				printEvent(c.stdout, e)
			}
		}
	}
	m, err := engine.NewMachine(bp, idx, opts)
	if err != nil {
		return c.fail(err)
	}
	m.Run(0)
	res := m.Result()
	if c.jsonOut {
		out := map[string]any{"ok": res.Passed, "result": res}
		if *trace {
			out["events"] = events
		}
		if *snapshot {
			out["snapshot"] = m.Snapshot()
		}
		c.emitJSON(out)
	} else {
		printResult(c.stdout, res, true)
		if *snapshot {
			enc := json.NewEncoder(c.stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(m.Snapshot())
		}
	}
	if !res.Passed {
		return exitFailure
	}
	return exitOK
}

func (c *cli) test(args []string) int {
	fs := c.flags("test")
	maxTicks := fs.Int64("max-ticks", 0, "override the puzzle tick limit")
	cycles := fs.Bool("cycles", false, "enable exact repeated-state (cycle) detection")
	pos, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) != 2 {
		fmt.Fprintln(c.stderr, "usage: asmgraph test PUZZLE.yaml SOLUTION.yaml [--max-ticks N] [--cycles] [--json]")
		return exitUsage
	}
	reg, err := c.registry()
	if err != nil {
		return c.fail(err)
	}
	p, bp, l, stage := c.load(reg, pos[0], pos[1])
	if stage != "" {
		return c.diagFail(stage, l)
	}
	res, err := engine.RunAll(bp, engine.Options{MaxTicks: *maxTicks, CycleDetection: *cycles})
	if err != nil {
		return c.fail(err)
	}
	if c.jsonOut {
		c.emitJSON(res)
	} else {
		fmt.Fprintf(c.stdout, "puzzle %s — %s\n", p.ID, p.Name)
		for _, t := range res.Tests {
			printResult(c.stdout, t, false)
		}
		s := res.Summary
		verdict := "PASSED"
		if !res.Passed {
			verdict = "FAILED"
		}
		fmt.Fprintf(c.stdout, "\n%s: %d/%d test cases passed\n", verdict, s.PassedTests, s.TotalTests)
		fmt.Fprintf(c.stdout, "ticks: total %d, max %d   instructions: %d   transfers: %d\n", s.TotalTicks, s.MaxTicks, s.TotalInstructions, s.TotalTransfers)
		fmt.Fprintf(c.stdout, "program bytes: %d (%s)\n", s.TotalProgramBytes, formatBytes(s.ProgramBytes, bp))
		fmt.Fprintf(c.stdout, "cells used: %d/%d   connections: %d\n", s.CellsUsed, s.CellsSupplied, s.Connections)
		printHardware(c.stdout, bp.Hardware)
		if res.Score != nil {
			fmt.Fprintf(c.stdout, "score: cost %d   ticks %d (worst case)   bytes %d\n", res.Score.Cost, res.Score.Ticks, res.Score.Bytes)
		} else {
			fmt.Fprintln(c.stdout, "score: none (a solution must pass every test case to be scored)")
		}
	}
	if !res.Passed {
		return exitFailure
	}
	return exitOK
}

func printHardware(w io.Writer, h solution.HardwareReport) {
	if !h.Allowed {
		return
	}
	budget := ""
	if h.MaxCost > 0 {
		budget = fmt.Sprintf(" of %d", h.MaxCost)
	}
	var parts []string
	for _, n := range h.Nodes {
		parts = append(parts, fmt.Sprintf("%s %s %d", n.ID, n.Type, n.Cost))
	}
	fmt.Fprintf(w, "hardware cost: %d%s (%s)\n", h.TotalCost, budget, strings.Join(parts, ", "))
}

func formatBytes(m map[string]int, bp *engine.Blueprint) string {
	var parts []string
	for _, c := range bp.Cells {
		if n, ok := m[c.ID]; ok {
			parts = append(parts, fmt.Sprintf("%s %d/%d", c.ID, n, c.Spec.ProgramMemory))
		}
	}
	return strings.Join(parts, ", ")
}

func printResult(w io.Writer, r *engine.TestResult, verbose bool) {
	mark := "PASS"
	if !r.Passed {
		mark = "FAIL"
	}
	fmt.Fprintf(w, "  %s  %-20s %-20s ticks=%-6d instructions=%-6d transfers=%d\n",
		mark, r.Test, r.Status, r.Metrics.Ticks, r.Metrics.Instructions, r.Metrics.Transfers)
	if !r.Passed || verbose {
		if r.Message != "" {
			fmt.Fprintf(w, "        %s\n", r.Message)
		}
	}
	if r.Error != nil {
		fmt.Fprintf(w, "        at cell %s, instruction %q (line %d)\n", r.Error.Cell, r.Error.Instruction, r.Error.SourceLine)
	}
	if r.Cycle != nil {
		fmt.Fprintf(w, "        cycle: period %d ticks (first seen at tick %d)\n", r.Cycle.Period, r.Cycle.FirstTick)
	}
	for _, b := range r.Blocked {
		fmt.Fprintf(w, "        blocked: %s at %q (line %d): %s\n", b.Cell, b.Instruction, b.SourceLine, b.Reason)
	}
	if !r.Passed || verbose {
		for _, o := range r.Outputs {
			fmt.Fprintf(w, "        output %-10s %-8s received %v of expected %v\n", o.Stream, o.State, o.Received, o.Expected)
		}
	}
	for _, n := range r.Notes {
		fmt.Fprintf(w, "        note: %s\n", n)
	}
}

func printEvent(w io.Writer, e engine.Event) {
	switch d := e.Data.(type) {
	case engine.TransferEvent:
		fmt.Fprintf(w, "[%5d] transfer   %s -> %s : %d\n", e.Tick, d.From, d.To, d.Value)
	case engine.InstructionEvent:
		fmt.Fprintf(w, "[%5d] %-10s %-20s (line %d) -> pc %d\n", e.Tick, e.Cell, d.Instruction, d.SourceLine, d.NextPC)
	case engine.BlockEvent:
		fmt.Fprintf(w, "[%5d] %-10s blocked: %s\n", e.Tick, e.Cell, d.Reason)
	case engine.OutputSnapshot:
		fmt.Fprintf(w, "[%5d] output     %s is %s (%d/%d)\n", e.Tick, d.Stream, d.State, len(d.Received), len(d.Expected))
	default:
		switch e.Type {
		case engine.EventDeadlockDetected, engine.EventCycleDetected, engine.EventRuntimeError:
			fmt.Fprintf(w, "[%5d] %s\n", e.Tick, e.Type)
		}
	}
}

func (c *cli) types(args []string) int {
	fs := c.flags("types")
	if _, err := parse(fs, args); err != nil {
		return exitUsage
	}
	reg, err := c.registry()
	if err != nil {
		return c.fail(err)
	}
	info := protocol.DescribeTypes(reg)
	if c.jsonOut {
		c.emitJSON(map[string]any{"types": info})
		return exitOK
	}
	for _, t := range info {
		fmt.Fprintf(c.stdout, "%s — %s\n", t.Type, t.Description)
		h := t.Hardware
		fmt.Fprintf(c.stdout, "  hardware: %s, ports %d-%d (default %d)", h.Category, h.Ports.Min, h.Ports.Max, h.Ports.Default)
		if h.PuzzleOnly {
			fmt.Fprint(c.stdout, ", puzzle-only")
		} else {
			fmt.Fprintf(c.stdout, ", cost %d + %d/port", h.Cost.Base, h.Cost.PerPort)
			for _, pc := range h.Cost.Properties {
				fmt.Fprintf(c.stdout, " + %d per %d %s", pc.Cost, pc.Per, pc.Property)
			}
		}
		if m := h.ProgramMemory; m != nil {
			fmt.Fprintf(c.stdout, ", program memory %d-%d bytes (default %d)", m.Min, m.Max, m.Default)
		}
		fmt.Fprintln(c.stdout)
		if f := h.PortFunctions; f != nil {
			for _, fn := range f.Functions {
				fmt.Fprintf(c.stdout, "  port function %-5s %-3s %s\n", fn.Name, fn.Direction, fn.Summary)
			}
		}
		if t.Programmable {
			var regs []string
			for _, r := range t.Registers {
				regs = append(regs, r.Name)
			}
			fmt.Fprintf(c.stdout, "  registers: %s\n", strings.Join(regs, ", "))
			for _, in := range t.Instructions {
				fmt.Fprintf(c.stdout, "  %s %-6s %-22s %s\n", in.Opcode, in.Mnemonic, strings.Join(in.Operands, ", "), in.Summary)
			}
		}
		for _, f := range t.Config {
			req := ""
			if f.Required {
				req = " (required)"
			}
			fmt.Fprintf(c.stdout, "  config %s: %s%s — %s\n", f.Name, f.Type, req, f.Description)
		}
	}
	return exitOK
}

// migrate rewrites schema-v1 files as schema v2. Without --write it prints
// the result; with --write it replaces each file in place.
func (c *cli) migrate(args []string) int {
	fs := c.flags("migrate")
	write := fs.Bool("write", false, "rewrite the files in place")
	pos, err := parse(fs, args)
	if err != nil {
		return exitUsage
	}
	if len(pos) == 0 {
		fmt.Fprintln(c.stderr, "usage: asmgraph migrate FILE.yaml... [--write]")
		return exitUsage
	}
	reg, err := c.registry()
	if err != nil {
		return c.fail(err)
	}
	for _, path := range pos {
		data, err := os.ReadFile(path)
		if err != nil {
			return c.fail(err)
		}
		var out []byte
		var notes []string
		if puzzle.IsPuzzleYAML(data) {
			out, notes, err = puzzle.MigrateYAML(data, reg)
		} else {
			out, notes, err = puzzle.MigrateSolutionYAML(data)
		}
		if err != nil {
			return c.fail(fmt.Errorf("%s: %w", path, err))
		}
		if len(notes) == 0 {
			fmt.Fprintf(c.stderr, "%s: already schema v2\n", path)
			continue
		}
		for _, n := range notes {
			fmt.Fprintf(c.stderr, "%s: %s\n", path, n)
		}
		if *write {
			if err := os.WriteFile(path, out, 0o644); err != nil {
				return c.fail(err)
			}
		} else {
			c.stdout.Write(out)
		}
	}
	return exitOK
}

func (c *cli) serve(args []string) int {
	fs := c.flags("serve")
	useStdio := fs.Bool("stdio", false, "serve JSON-RPC 2.0 over stdin/stdout")
	noFiles := fs.Bool("no-files", false, "reject path-based loading (inline yaml only)")
	if _, err := parse(fs, args); err != nil {
		return exitUsage
	}
	if !*useStdio {
		fmt.Fprintln(c.stderr, "usage: asmgraph serve --stdio")
		return exitUsage
	}
	reg, err := c.registry()
	if err != nil {
		fmt.Fprintf(c.stderr, "asmgraph: %v\n", err)
		return exitUsage
	}
	srv := stdio.New(c.stdin, c.stdout, c.stderr, reg)
	srv.Service().AllowFiles = !*noFiles
	if err := srv.Serve(); err != nil && !errors.Is(err, io.EOF) {
		fmt.Fprintf(c.stderr, "asmgraph: %v\n", err)
		return exitUsage
	}
	return exitOK
}

func (c *cli) web(args []string) int {
	fs := c.flags("web")
	addr := fs.String("addr", "127.0.0.1:7357", "listen address (keep it on localhost unless you add --allow-host)")
	puzzles := fs.String("puzzles", "examples/puzzles", "directory of puzzle YAML files")
	solutions := fs.String("solutions", "solutions", "directory where solutions are saved")
	static := fs.String("static", "web/dist", "built frontend directory (empty to disable)")
	var allowHosts multiFlag
	fs.Var(&allowHosts, "allow-host", "extra Host/Origin name to accept (repeatable)")
	if _, err := parse(fs, args); err != nil {
		return exitUsage
	}
	reg, err := c.registry()
	if err != nil {
		fmt.Fprintf(c.stderr, "asmgraph: %v\n", err)
		return exitUsage
	}
	staticDir := *static
	if staticDir != "" {
		if st, err := os.Stat(filepath.Join(staticDir, "index.html")); err != nil || st.IsDir() {
			fmt.Fprintf(c.stderr, "asmgraph: no built frontend in %s (run `npm run build` in web/); serving the API only\n", staticDir)
			staticDir = ""
		}
	}
	var origins []string
	for _, h := range allowHosts {
		origins = append(origins, h, h+":*")
	}
	srv, err := webtransport.New(webtransport.Config{
		Registry: reg, PuzzlesDir: *puzzles, SolutionsDir: *solutions, StaticDir: staticDir,
		Logger:       log.New(c.stderr, "asmgraph: ", log.LstdFlags),
		AllowedHosts: allowHosts, OriginPatterns: origins,
	})
	if err != nil {
		fmt.Fprintf(c.stderr, "asmgraph: %v\n", err)
		return exitUsage
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		fmt.Fprintf(c.stderr, "asmgraph: %v\n", err)
		return exitUsage
	}
	fmt.Fprintf(c.stderr, "asmgraph: serving on http://%s (puzzles: %s, solutions: %s)\n", ln.Addr(), *puzzles, *solutions)
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = hs.Shutdown(ctx)
	}()
	if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(c.stderr, "asmgraph: %v\n", err)
		return exitUsage
	}
	return exitOK
}

func nonNil(l diag.List) diag.List {
	if l == nil {
		return diag.List{}
	}
	sort.SliceStable(l, func(i, j int) bool { return l[i].Line < l[j].Line })
	return l
}
