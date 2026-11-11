package engine

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/puzzle"
	"asmgraph/solution"
)

// Session errors.
var (
	ErrNoPuzzle     = errors.New("no puzzle loaded")
	ErrNoSolution   = errors.New("no solution loaded")
	ErrNotStarted   = errors.New("no simulation started")
	ErrCompileFirst = errors.New("solution does not compile")
)

// Session is the stateful, transport-independent API used by frontends:
// load a puzzle and solution, compile, start a test case, step, run
// continuously, pause, reset, and inspect. The session owns all simulation
// state; callers only receive copies (snapshots and events).
//
// Continuous runs execute on a background goroutine one tick at a time
// under the session lock. Goroutines only decide *when* the next tick runs;
// the simulation itself is single-threaded and deterministic.
type Session struct {
	mu       sync.Mutex
	reg      *cell.Registry
	sink     func(Event)
	seq      uint64
	puzzle   *puzzle.Puzzle
	solution *solution.Solution
	bp       *Blueprint
	machine  *Machine
	opts     Options
	run      *runState
	bps      map[string]map[int]bool
}

// Breakpoint identifies the source line a run paused before.
type Breakpoint struct {
	Cell string `json:"cell"`
	Line int    `json:"line"`
}

type runState struct {
	stop chan struct{}
	done chan struct{}
}

// NewSession creates a session. sink receives every event (it may be nil)
// and is called with the session lock held, so it must not call back into
// the session.
func NewSession(reg *cell.Registry, sink func(Event)) *Session {
	return &Session{reg: reg, sink: sink}
}

// Registry returns the cell registry.
func (s *Session) Registry() *cell.Registry { return s.reg }

func (s *Session) emitLocked(t EventType, cellID string, data any) {
	if s.sink == nil {
		return
	}
	s.seq++
	ev := Event{Seq: s.seq, Type: t, Cell: cellID, Data: data}
	if s.machine != nil {
		ev.Tick = s.machine.tick
		ev.Test = s.machine.test.Name
	}
	s.sink(ev)
}

// stopRun stops a background run, if any, and waits for it to exit.
func (s *Session) stopRun() bool {
	s.mu.Lock()
	rs := s.run
	s.run = nil
	s.mu.Unlock()
	if rs == nil {
		return false
	}
	close(rs.stop)
	<-rs.done
	return true
}

// LoadPuzzle validates and installs a puzzle, discarding any solution and
// simulation state.
func (s *Session) LoadPuzzle(p *puzzle.Puzzle) diag.List {
	s.stopRun()
	l := p.Validate(s.reg)
	if l.HasErrors() {
		return l
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.puzzle, s.solution, s.bp, s.machine = p, nil, nil, nil
	return l
}

// Puzzle returns the loaded puzzle.
func (s *Session) Puzzle() *puzzle.Puzzle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.puzzle
}

// Solution returns the loaded solution.
func (s *Session) Solution() *solution.Solution {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.solution
}

// LoadSolution validates and installs a solution for the loaded puzzle.
// Compilation happens in Compile (or implicitly in Start / RunTests).
func (s *Session) LoadSolution(sol *solution.Solution) diag.List {
	s.stopRun()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.puzzle == nil {
		return diag.List{{Severity: diag.Error, Code: "session", Message: ErrNoPuzzle.Error()}}
	}
	l := sol.Validate(s.puzzle, s.reg)
	if l.HasErrors() {
		return l
	}
	s.solution, s.bp, s.machine = sol, nil, nil
	return l
}

// Compile prepares the blueprint and returns per-cell program reports.
func (s *Session) Compile() ([]ProgramReport, diag.List) {
	s.stopRun()
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.compileLocked()
	if l.HasErrors() {
		return nil, l
	}
	return s.bp.Programs(), l
}

func (s *Session) compileLocked() diag.List {
	if s.puzzle == nil {
		return diag.List{{Severity: diag.Error, Code: "session", Message: ErrNoPuzzle.Error()}}
	}
	if s.solution == nil {
		return diag.List{{Severity: diag.Error, Code: "session", Message: ErrNoSolution.Error()}}
	}
	bp, l := Prepare(s.reg, s.puzzle, s.solution)
	if bp != nil {
		s.bp = bp
	}
	return l
}

// Start creates a fresh machine for a test case (by name or index).
func (s *Session) Start(test string, opts Options) (*Snapshot, error) {
	s.stopRun()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bp == nil {
		if l := s.compileLocked(); l.HasErrors() {
			return nil, l
		}
	}
	idx, err := s.bp.TestIndex(test)
	if err != nil {
		return nil, err
	}
	return s.startLocked(idx, opts)
}

func (s *Session) startLocked(idx int, opts Options) (*Snapshot, error) {
	o := opts
	o.Events, o.Sequence = nil, nil
	if s.sink != nil {
		o.Events, o.Sequence = s.sink, &s.seq
	}
	m, err := NewMachine(s.bp, idx, o)
	if err != nil {
		return nil, err
	}
	s.machine, s.opts = m, opts
	snap := m.Snapshot()
	s.emitLocked(EventSimulationStarted, "", snap)
	return snap, nil
}

// Step executes up to n ticks (n <= 0 means 1) and returns the status.
func (s *Session) Step(n int) (Status, int64, error) {
	s.stopRun()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.machine == nil {
		return "", 0, ErrNotStarted
	}
	if n <= 0 {
		n = 1
	}
	for i := 0; i < n && !s.machine.status.Terminal(); i++ {
		if bp, hit := s.stepLocked(); hit && i < n-1 {
			s.emitLocked(EventSimulationPaused, bp.Cell, pausedAt(s.machine.status, bp))
			break
		}
	}
	return s.machine.status, s.machine.tick, nil
}

// SetBreakpoints replaces the breakpoint set: cell id -> 1-based source
// lines. Runs (RunAsync, Run, and multi-tick Step) pause after any tick
// that moves a cell onto a breakpoint line, before that line executes.
// Breakpoints only decide when a run stops; they never change results.
func (s *Session) SetBreakpoints(bps map[string][]int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bps = map[string]map[int]bool{}
	for cell, lines := range bps {
		if len(lines) == 0 {
			continue
		}
		set := map[int]bool{}
		for _, l := range lines {
			set[l] = true
		}
		s.bps[cell] = set
	}
}

// stepLocked advances one tick and reports a breakpoint hit, if any.
func (s *Session) stepLocked() (Breakpoint, bool) {
	s.machine.Step()
	if len(s.bps) == 0 || s.machine.status.Terminal() {
		return Breakpoint{}, false
	}
	cell, line, ok := s.machine.BreakpointHit(s.bps)
	return Breakpoint{cell, line}, ok
}

func pausedAt(st Status, bp Breakpoint) map[string]any {
	return map[string]any{"status": st, "reason": "breakpoint", "cell": bp.Cell, "line": bp.Line}
}

// Run executes synchronously until terminal or maxTicks more ticks
// (maxTicks <= 0: until terminal).
func (s *Session) Run(maxTicks int64) (*TestResult, error) {
	s.stopRun()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.machine == nil {
		return nil, ErrNotStarted
	}
	for n := int64(0); !s.machine.status.Terminal() && (maxTicks <= 0 || n < maxTicks); n++ {
		if bp, hit := s.stepLocked(); hit {
			s.emitLocked(EventSimulationPaused, bp.Cell, pausedAt(s.machine.status, bp))
			break
		}
	}
	return s.machine.Result(), nil
}

// RunAsync starts a background run that executes ticks until the machine
// terminates, maxTicks more ticks have run (<= 0: unlimited), or Pause is
// called. delay paces ticks for animation; it never affects results.
func (s *Session) RunAsync(maxTicks int64, delay time.Duration, onDone func(Status)) error {
	s.stopRun()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.machine == nil {
		return ErrNotStarted
	}
	rs := &runState{stop: make(chan struct{}), done: make(chan struct{})}
	s.run = rs
	m := s.machine
	go func() {
		defer close(rs.done)
		var n int64
		for {
			select {
			case <-rs.stop:
				return
			default:
			}
			s.mu.Lock()
			if s.machine != m || m.status.Terminal() || (maxTicks > 0 && n >= maxTicks) {
				st := m.status
				if s.run == rs {
					s.run = nil
					if !st.Terminal() {
						s.emitLocked(EventSimulationPaused, "", map[string]any{"status": st, "reason": "tick budget reached"})
					}
				}
				s.mu.Unlock()
				if onDone != nil {
					onDone(st)
				}
				return
			}
			bp, hit := s.stepLocked()
			n++
			if hit {
				s.run = nil
				st := m.status
				s.emitLocked(EventSimulationPaused, bp.Cell, pausedAt(st, bp))
				s.mu.Unlock()
				if onDone != nil {
					onDone(st)
				}
				return
			}
			s.mu.Unlock()
			if delay > 0 {
				select {
				case <-rs.stop:
					return
				case <-time.After(delay):
				}
			}
		}
	}()
	return nil
}

// Running reports whether a background run is active.
func (s *Session) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run != nil
}

// Pause stops a background run. It is a no-op if nothing is running.
func (s *Session) Pause() (Status, int64, error) {
	stopped := s.stopRun()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.machine == nil {
		return "", 0, ErrNotStarted
	}
	if stopped {
		s.emitLocked(EventSimulationPaused, "", map[string]any{"status": s.machine.status, "reason": "pause requested"})
	}
	return s.machine.status, s.machine.tick, nil
}

// Reset replaces the machine with a fresh one for the same test case.
func (s *Session) Reset() (*Snapshot, error) {
	s.stopRun()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.machine == nil {
		return nil, ErrNotStarted
	}
	idx := s.machine.testIndex
	s.emitLocked(EventSimulationReset, "", nil)
	return s.startLocked(idx, s.opts)
}

// State returns a complete snapshot of the current machine.
func (s *Session) State() (*Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.machine == nil {
		return nil, ErrNotStarted
	}
	return s.machine.Snapshot(), nil
}

// Cell returns the snapshot of one cell.
func (s *Session) Cell(id string) (*CellSnapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.machine == nil {
		return nil, ErrNotStarted
	}
	c, ok := s.machine.CellSnapshot(id)
	if !ok {
		return nil, fmt.Errorf("no cell %q", id)
	}
	return c, nil
}

// Result returns the current machine's (possibly partial) result.
func (s *Session) Result() (*TestResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.machine == nil {
		return nil, ErrNotStarted
	}
	return s.machine.Result(), nil
}

// RunTest runs one test case (by name or index) on a fresh machine. It
// does not disturb the interactive machine.
func (s *Session) RunTest(test string, opts Options) (*TestResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bp == nil {
		if l := s.compileLocked(); l.HasErrors() {
			return nil, l
		}
	}
	idx, err := s.bp.TestIndex(test)
	if err != nil {
		return nil, err
	}
	o := opts
	o.Events, o.Sequence = nil, nil
	res, err := RunTest(s.bp, idx, o)
	if err != nil {
		return nil, err
	}
	if s.sink != nil {
		s.seq++
		s.sink(Event{Seq: s.seq, Tick: res.Metrics.Ticks, Type: EventTestCompleted, Test: res.Test, Data: res})
	}
	return res, nil
}

// RunTests runs every test case on fresh machines. It does not disturb the
// interactive machine. A test.completed event is emitted per test.
func (s *Session) RunTests(opts Options) (*SuiteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bp == nil {
		if l := s.compileLocked(); l.HasErrors() {
			return nil, l
		}
	}
	o := opts
	o.Events, o.Sequence = nil, nil
	res, err := RunAll(s.bp, o)
	if err != nil {
		return nil, err
	}
	if s.sink != nil {
		for _, t := range res.Tests {
			s.seq++
			s.sink(Event{Seq: s.seq, Tick: t.Metrics.Ticks, Type: EventTestCompleted, Test: t.Test, Data: t})
		}
	}
	return res, nil
}

// Close stops any background run.
func (s *Session) Close() { s.stopRun() }
