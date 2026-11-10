package engine

// RunTest executes one test case on a fresh machine until it terminates.
func RunTest(bp *Blueprint, testIndex int, opts Options) (*TestResult, error) {
	m, err := NewMachine(bp, testIndex, opts)
	if err != nil {
		return nil, err
	}
	m.Run(0)
	return m.Result(), nil
}

// RunAll executes every test case, each on its own fresh machine, and
// aggregates the results. The solution passes only if every test passes.
func RunAll(bp *Blueprint, opts Options) (*SuiteResult, error) {
	res := &SuiteResult{Puzzle: bp.Puzzle.ID, Passed: true}
	used := map[string]bool{}
	for i := range bp.Puzzle.Tests {
		m, err := NewMachine(bp, i, opts)
		if err != nil {
			return nil, err
		}
		m.Run(0)
		tr := m.Result()
		for _, c := range m.cells {
			if c.used {
				used[c.bp.ID] = true
			}
		}
		res.Tests = append(res.Tests, tr)
		if !tr.Passed {
			res.Passed = false
		}
		s := &res.Summary
		s.TotalTests++
		if tr.Passed {
			s.PassedTests++
		}
		s.TotalTicks += tr.Metrics.Ticks
		if tr.Metrics.Ticks > s.MaxTicks {
			s.MaxTicks = tr.Metrics.Ticks
		}
		s.TotalInstructions += tr.Metrics.Instructions
		s.TotalTransfers += tr.Metrics.Transfers
		s.ProgramBytes = tr.Metrics.ProgramBytes
		s.TotalProgramBytes = tr.Metrics.TotalProgramBytes
		s.CellsSupplied = tr.Metrics.CellsSupplied
		s.Connections = tr.Metrics.Connections
	}
	res.Summary.CellsUsed = len(used)
	return res, nil
}
