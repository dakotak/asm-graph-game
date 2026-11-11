package solution

import (
	"fmt"
	"regexp"
	"strings"

	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/puzzle"
)

var nodeIDRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

// NodeReport is the resolved configuration and cost of one player node.
type NodeReport struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	Category      string   `json:"category"`
	Ports         int      `json:"ports"`
	ProgramMemory int      `json:"programMemory,omitempty"`
	PortFunctions []string `json:"portFunctions,omitempty"`
	Cost          int      `json:"cost"`
}

// HardwareReport summarises the player's hardware against the puzzle's
// budget. Costs are computed by the backend only; frontends display them.
type HardwareReport struct {
	Allowed   bool           `json:"allowed"`
	Nodes     []NodeReport   `json:"nodes"`
	TotalCost int            `json:"totalCost"`
	MaxCost   int            `json:"maxCost,omitempty"`
	NodeCount int            `json:"nodeCount"`
	MaxNodes  int            `json:"maxNodes,omitempty"`
	Counts    map[string]int `json:"counts"`
}

// Report resolves every player node (with defaults) and its cost. Nodes of
// unknown or disallowed types are listed with cost 0.
func (s *Solution) Report(p *puzzle.Puzzle, reg *cell.Registry) HardwareReport {
	r := HardwareReport{Allowed: p.AllowsHardware(), Nodes: []NodeReport{}, Counts: map[string]int{}}
	if h := p.Hardware; h != nil {
		r.MaxCost, r.MaxNodes = h.MaxCost, h.MaxNodes
	}
	for i := range s.Hardware {
		n := &s.Hardware[i]
		hc := s.nodeConfig(p, reg, i)
		nr := NodeReport{ID: n.ID, Type: n.Type, Ports: hc.Ports, ProgramMemory: hc.ProgramMemory, PortFunctions: hc.PortFunctions}
		if a, ok := reg.Lookup(n.Type); ok {
			spec := cell.HardwareOf(a)
			nr.Category = string(spec.Category)
			if nr.PortFunctions == nil && spec.PortFunctions != nil {
				nr.PortFunctions = spec.PortFunctions.DefaultAssignment(hc.Ports)
			}
		}
		if e, ok := p.EffectiveHardware(reg, n.Type); ok {
			nr.Cost = cell.Cost(e.Cost, hc)
		}
		r.Nodes = append(r.Nodes, nr)
		r.TotalCost += nr.Cost
		r.Counts[n.Type]++
	}
	r.NodeCount = len(s.Hardware)
	return r
}

// validateHardware checks the player's nodes against the plugins' physical
// limits and the puzzle's rules: allowed types, instance counts, port
// counts, program memory, port functions, configuration, total node count
// and total cost.
func (s *Solution) validateHardware(p *puzzle.Puzzle, reg *cell.Registry) diag.List {
	var l diag.List
	if !p.AllowsHardware() {
		if len(s.Hardware) > 0 {
			l.Errorf("solution.hardware", "hardware", "puzzle %q has a fixed topology and does not allow player hardware", p.ID)
		}
		return l
	}
	ids := map[string]int{}
	for i := range s.Hardware {
		n := &s.Hardware[i]
		path := fmt.Sprintf("hardware[%d]", i)
		switch {
		case n.ID == "":
			l.Errorf("solution.hardware.id", path+".id", "node id is required")
		case !nodeIDRe.MatchString(n.ID):
			l.Errorf("solution.hardware.id", path+".id", "invalid node id %q (letters, digits, '.', '_' and '-')", n.ID)
		default:
			if _, ok := p.CellIndex(n.ID); ok {
				l.Errorf("solution.hardware.id", path+".id", "id %q is already used by a puzzle cell", n.ID)
			} else if prev, dup := ids[n.ID]; dup {
				l.Errorf("solution.hardware.id", path+".id", "duplicate node id %q (also hardware[%d])", n.ID, prev)
			} else {
				ids[n.ID] = i
			}
		}
		arch, ok := reg.Lookup(n.Type)
		if !ok {
			l.Errorf("solution.hardware.type", path+".type", "unknown hardware type %q", n.Type)
			continue
		}
		e, ok := p.EffectiveHardware(reg, n.Type)
		if !ok {
			allowed := []string{}
			for _, c := range p.HardwareCatalog(reg) {
				allowed = append(allowed, c.Type)
			}
			l.Errorf("solution.hardware.type", path+".type", "this puzzle does not allow %s hardware (allowed: %s)", n.Type, strings.Join(allowed, ", "))
			continue
		}
		hc := s.nodeConfig(p, reg, i)
		// Report the plugin's physical limits and the puzzle's rules
		// together, but only one problem per field.
		errs := cell.ValidateHardware(arch, hc)
		seen := map[string]bool{}
		for _, ce := range errs {
			seen[ce.Field] = true
		}
		for _, ce := range e.CheckNode(hc) {
			if !seen[ce.Field] {
				errs = append(errs, ce)
			}
		}
		for _, ce := range errs {
			l.Errorf("solution.hardware.config", path+"."+nodeField(ce.Field), "%s: %s", n.ID, ce.Message)
		}
	}
	rep := s.Report(p, reg)
	for _, e := range p.HardwareCatalog(reg) {
		n := rep.Counts[e.Type]
		if n < e.Min {
			l.Errorf("solution.hardware.count", "hardware", "this puzzle requires at least %d %s node(s), the solution has %d", e.Min, e.Type, n)
		}
		if e.Max > 0 && n > e.Max {
			l.Errorf("solution.hardware.count", "hardware", "this puzzle allows at most %d %s node(s), the solution has %d", e.Max, e.Type, n)
		}
	}
	if rep.MaxNodes > 0 && rep.NodeCount > rep.MaxNodes {
		l.Errorf("solution.hardware.count", "hardware", "this puzzle allows at most %d node(s), the solution has %d", rep.MaxNodes, rep.NodeCount)
	}
	if rep.MaxCost > 0 && rep.TotalCost > rep.MaxCost {
		l.Errorf("solution.hardware.cost", "hardware", "hardware cost %d exceeds this puzzle's budget of %d", rep.TotalCost, rep.MaxCost)
	}
	return l
}

// nodeField maps cell.ConfigError field names to solution YAML keys.
func nodeField(f string) string {
	switch {
	case f == "ports":
		return "port_count"
	case f == "":
		return "config"
	}
	return f
}
