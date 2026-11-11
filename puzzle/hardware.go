package puzzle

import (
	"fmt"
	"strings"

	"asmgraph/cell"
	"asmgraph/diag"
	"asmgraph/vm"
)

// HardwareRules is the optional `hardware` section of a schema-v2 puzzle.
// Its presence lets players instantiate their own hardware; every field
// restricts what they may build. Plugins define what hardware can do,
// these rules define what this puzzle allows, and players design within
// both.
type HardwareRules struct {
	// MaxNodes bounds the number of player-instantiated nodes (0 = no limit).
	MaxNodes int `yaml:"max_nodes,omitempty" json:"max_nodes,omitempty"`
	// MaxCost bounds the total hardware cost (0 = no limit).
	MaxCost int `yaml:"max_cost,omitempty" json:"max_cost,omitempty"`
	// Ports bounds the port count of every player node.
	Ports *Range `yaml:"ports,omitempty" json:"ports,omitempty"`
	// Catalog lists the allowed hardware types and their restrictions. When
	// omitted, every player-placeable registered type is allowed with its
	// plugin limits, except the Prohibited ones.
	Catalog map[string]TypeRules `yaml:"catalog,omitempty" json:"catalog,omitempty"`
	// Prohibited types can never be instantiated by players.
	Prohibited []string `yaml:"prohibited,omitempty" json:"prohibited,omitempty"`
	// Required types must be instantiated at least once (shorthand for a
	// catalog entry with min: 1).
	Required []string `yaml:"required,omitempty" json:"required,omitempty"`
}

// Range is an inclusive integer range; zero bounds are open.
type Range struct {
	Min int `yaml:"min,omitempty" json:"min,omitempty"`
	Max int `yaml:"max,omitempty" json:"max,omitempty"`
	// Default overrides the plugin default (only if the plugin permits
	// default overrides).
	Default int `yaml:"default,omitempty" json:"default,omitempty"`
}

// TypeRules restricts one hardware type.
type TypeRules struct {
	// Min and Max bound the number of instances (Max 0 = no limit).
	Min int `yaml:"min,omitempty" json:"min,omitempty"`
	Max int `yaml:"max,omitempty" json:"max,omitempty"`
	// Ports restricts the port count.
	Ports *Range `yaml:"ports,omitempty" json:"ports,omitempty"`
	// ProgramMemory restricts the per-cell program memory (bytes).
	ProgramMemory *Range `yaml:"program_memory,omitempty" json:"program_memory,omitempty"`
	// PortFunctions restricts peripheral port function assignments.
	PortFunctions *FunctionRules `yaml:"port_functions,omitempty" json:"port_functions,omitempty"`
	// Config restricts plugin configuration fields.
	Config map[string]ConfigRule `yaml:"config,omitempty" json:"config,omitempty"`
	// Cost overrides the plugin cost model (only if the plugin permits it).
	Cost *CostOverride `yaml:"cost,omitempty" json:"cost,omitempty"`
}

// FunctionRules restricts port functions.
type FunctionRules struct {
	// Allowed limits the functions players may assign (empty = all).
	Allowed []string `yaml:"allowed,omitempty" json:"allowed,omitempty"`
	// Max bounds how many ports may have a function.
	Max map[string]int `yaml:"max,omitempty" json:"max,omitempty"`
}

// ConfigRule restricts one configuration field: an integer range, or a
// list of permitted values.
type ConfigRule struct {
	Min    *int64 `yaml:"min,omitempty" json:"min,omitempty"`
	Max    *int64 `yaml:"max,omitempty" json:"max,omitempty"`
	Values []any  `yaml:"values,omitempty" json:"values,omitempty"`
}

// CostOverride replaces parts of a plugin's cost model.
type CostOverride struct {
	Base       *int                        `yaml:"base,omitempty" json:"base,omitempty"`
	PerPort    *int                        `yaml:"port,omitempty" json:"port,omitempty"`
	Properties map[string]PropertyOverride `yaml:"properties,omitempty" json:"properties,omitempty"`
}

// PropertyOverride replaces (or adds) a property cost.
type PropertyOverride struct {
	Cost int `yaml:"cost" json:"cost"`
	Per  int `yaml:"per" json:"per"`
}

// Effective is a hardware type's limits after applying a puzzle's rules
// to the plugin's own description. It is what players may actually build.
type Effective struct {
	Type          string                `json:"type"`
	Spec          cell.HardwareSpec     `json:"spec"`
	Ports         cell.PortSpec         `json:"ports"`
	ProgramMemory *cell.MemorySpec      `json:"programMemory,omitempty"`
	Functions     []string              `json:"functions,omitempty"`
	FunctionMax   map[string]int        `json:"functionMax,omitempty"`
	Config        map[string]ConfigRule `json:"config,omitempty"`
	Cost          cell.CostModel        `json:"cost"`
	Min           int                   `json:"min"`
	Max           int                   `json:"max"`

	schema []cell.ConfigField
}

// AllowsHardware reports whether players may instantiate hardware.
func (p *Puzzle) AllowsHardware() bool { return p.Hardware != nil }

// HardwareCatalog returns the effective limits of every hardware type
// players may instantiate, sorted by type. It assumes the puzzle is valid.
func (p *Puzzle) HardwareCatalog(reg *cell.Registry) []Effective {
	if p.Hardware == nil {
		return nil
	}
	var out []Effective
	for _, t := range p.allowedTypes(reg) {
		if e, ok := p.EffectiveHardware(reg, t); ok {
			out = append(out, e)
		}
	}
	return out
}

func (p *Puzzle) allowedTypes(reg *cell.Registry) []string {
	h := p.Hardware
	prohibited := map[string]bool{}
	for _, t := range h.Prohibited {
		prohibited[t] = true
	}
	var types []string
	if len(h.Catalog) > 0 {
		types = cell.SortedKeys(h.Catalog)
	} else {
		for _, t := range reg.Types() {
			a, _ := reg.Lookup(t)
			if !cell.HardwareOf(a).PuzzleOnly {
				types = append(types, t)
			}
		}
	}
	var out []string
	for _, t := range types {
		if !prohibited[t] {
			out = append(out, t)
		}
	}
	return out
}

// EffectiveHardware returns the effective limits of type t, or false if
// players may not instantiate it in this puzzle.
func (p *Puzzle) EffectiveHardware(reg *cell.Registry, t string) (Effective, bool) {
	if p.Hardware == nil {
		return Effective{}, false
	}
	allowed := false
	for _, a := range p.allowedTypes(reg) {
		if a == t {
			allowed = true
		}
	}
	arch, ok := reg.Lookup(t)
	if !allowed || !ok {
		return Effective{}, false
	}
	spec := cell.HardwareOf(arch)
	if spec.PuzzleOnly {
		return Effective{}, false
	}
	rules := p.Hardware.Catalog[t]
	e := Effective{Type: t, Spec: spec, Ports: spec.Ports, Cost: spec.Cost, Min: rules.Min, Max: rules.Max, Config: rules.Config, schema: arch.ConfigSchema()}
	for _, r := range p.Hardware.Required {
		if r == t && e.Min < 1 {
			e.Min = 1
		}
	}
	e.Ports = narrow(e.Ports, p.Hardware.Ports)
	e.Ports = narrow(e.Ports, rules.Ports)
	if spec.ProgramMemory != nil {
		m := *spec.ProgramMemory
		if r := rules.ProgramMemory; r != nil {
			n := narrow(cell.PortSpec{Min: m.Min, Max: m.Max, Default: m.Default}, r)
			m = cell.MemorySpec{Min: n.Min, Max: n.Max, Default: n.Default}
		}
		e.ProgramMemory = &m
	}
	if f := spec.PortFunctions; f != nil {
		for _, fn := range f.Functions {
			e.Functions = append(e.Functions, fn.Name)
		}
		if r := rules.PortFunctions; r != nil {
			if len(r.Allowed) > 0 {
				e.Functions = nil
				for _, fn := range f.Functions {
					for _, a := range r.Allowed {
						if strings.EqualFold(a, fn.Name) {
							e.Functions = append(e.Functions, fn.Name)
						}
					}
				}
			}
			if len(r.Max) > 0 {
				e.FunctionMax = map[string]int{}
				for k, v := range r.Max {
					if fn, ok := f.Lookup(k); ok {
						e.FunctionMax[fn.Name] = v
					}
				}
			}
		}
	}
	if c := rules.Cost; c != nil && spec.Overrides.Cost {
		m := cell.CostModel{Base: spec.Cost.Base, PerPort: spec.Cost.PerPort, Properties: append([]cell.PropertyCost(nil), spec.Cost.Properties...)}
		if c.Base != nil {
			m.Base = *c.Base
		}
		if c.PerPort != nil {
			m.PerPort = *c.PerPort
		}
		for _, name := range cell.SortedKeys(c.Properties) {
			o := c.Properties[name]
			replaced := false
			for i := range m.Properties {
				if m.Properties[i].Property == name {
					m.Properties[i] = cell.PropertyCost{Property: name, Cost: o.Cost, Per: o.Per}
					replaced = true
				}
			}
			if !replaced {
				m.Properties = append(m.Properties, cell.PropertyCost{Property: name, Cost: o.Cost, Per: o.Per})
			}
		}
		e.Cost = m
	}
	return e, true
}

// narrow intersects a plugin range with a puzzle range. A puzzle default
// wins only when it is inside the result; otherwise the plugin default is
// clamped into the range.
func narrow(s cell.PortSpec, r *Range) cell.PortSpec {
	if r == nil {
		return s
	}
	if r.Min > s.Min {
		s.Min = r.Min
	}
	if r.Max > 0 && r.Max < s.Max {
		s.Max = r.Max
	}
	if r.Default != 0 {
		s.Default = r.Default
	}
	if s.Default < s.Min {
		s.Default = s.Min
	}
	if s.Default > s.Max {
		s.Default = s.Max
	}
	return s
}

// validateHardware checks the hardware section against the registry:
// referenced types exist and are placeable, and no restriction exceeds a
// plugin's physical limits or overrides something the plugin does not
// permit.
func (p *Puzzle) validateHardware(l *diag.List, reg *cell.Registry) {
	h := p.Hardware
	if h == nil {
		return
	}
	if h.MaxNodes < 0 {
		l.Errorf("puzzle.hardware", "hardware.max_nodes", "max_nodes must not be negative")
	}
	if h.MaxCost < 0 {
		l.Errorf("puzzle.hardware", "hardware.max_cost", "max_cost must not be negative")
	}
	checkRange(l, "hardware.ports", h.Ports, 0, vm.MaxPorts)
	checkType := func(path, t string) (cell.Architecture, bool) {
		a, ok := reg.Lookup(t)
		if !ok {
			l.Errorf("puzzle.hardware.type", path, "unknown hardware type %q (registered: %s)", t, strings.Join(reg.Types(), ", "))
			return nil, false
		}
		if cell.HardwareOf(a).PuzzleOnly {
			l.Errorf("puzzle.hardware.type", path, "type %q can only be placed by the puzzle, not by players", t)
			return nil, false
		}
		return a, true
	}
	prohibited := map[string]bool{}
	for i, t := range h.Prohibited {
		if _, ok := reg.Lookup(t); !ok {
			l.Errorf("puzzle.hardware.type", fmt.Sprintf("hardware.prohibited[%d]", i), "unknown hardware type %q", t)
		}
		prohibited[t] = true
	}
	for i, t := range h.Required {
		path := fmt.Sprintf("hardware.required[%d]", i)
		if _, ok := checkType(path, t); !ok {
			continue
		}
		if prohibited[t] {
			l.Errorf("puzzle.hardware.type", path, "type %q is both required and prohibited", t)
		} else if _, ok := h.Catalog[t]; len(h.Catalog) > 0 && !ok {
			l.Errorf("puzzle.hardware.type", path, "required type %q is not in the catalog", t)
		}
	}
	for _, t := range cell.SortedKeys(h.Catalog) {
		path := "hardware.catalog." + t
		a, ok := checkType(path, t)
		if !ok {
			continue
		}
		if prohibited[t] {
			l.Errorf("puzzle.hardware.type", path, "type %q is both in the catalog and prohibited", t)
		}
		p.validateTypeRules(l, path, a, h.Catalog[t])
	}
	min := 0
	for _, t := range p.allowedTypes(reg) {
		if e, ok := p.EffectiveHardware(reg, t); ok {
			min += e.Min
			if e.Ports.Min > e.Ports.Max {
				l.Errorf("puzzle.hardware", "hardware", "no port count of %s satisfies both the plugin (%d-%d) and the puzzle limits", t, cell.HardwareOf(e.archOf(reg)).Ports.Min, cell.HardwareOf(e.archOf(reg)).Ports.Max)
			}
		}
	}
	if h.MaxNodes > 0 && min > h.MaxNodes {
		l.Errorf("puzzle.hardware", "hardware.max_nodes", "max_nodes %d is less than the %d node(s) the minimums require", h.MaxNodes, min)
	}
}

func (e Effective) archOf(reg *cell.Registry) cell.Architecture {
	a, _ := reg.Lookup(e.Type)
	return a
}

func (p *Puzzle) validateTypeRules(l *diag.List, path string, a cell.Architecture, r TypeRules) {
	spec := cell.HardwareOf(a)
	if r.Min < 0 || r.Max < 0 || (r.Max > 0 && r.Min > r.Max) {
		l.Errorf("puzzle.hardware.count", path, "invalid instance range min %d, max %d", r.Min, r.Max)
	}
	if r.Ports != nil {
		checkRange(l, path+".ports", r.Ports, spec.Ports.Min, spec.Ports.Max)
		if r.Ports.Default != 0 && !spec.Overrides.Defaults {
			l.Errorf("puzzle.hardware.override", path+".ports.default", "%s does not allow puzzles to override its defaults", a.Type())
		}
	}
	if r.ProgramMemory != nil {
		if spec.ProgramMemory == nil {
			l.Errorf("puzzle.hardware.memory", path+".program_memory", "%s is not programmable", a.Type())
		} else {
			checkRange(l, path+".program_memory", r.ProgramMemory, spec.ProgramMemory.Min, spec.ProgramMemory.Max)
			if r.ProgramMemory.Default != 0 && !spec.Overrides.Defaults {
				l.Errorf("puzzle.hardware.override", path+".program_memory.default", "%s does not allow puzzles to override its defaults", a.Type())
			}
		}
	}
	if f := r.PortFunctions; f != nil {
		if spec.PortFunctions == nil {
			l.Errorf("puzzle.hardware.functions", path+".port_functions", "%s has no port functions", a.Type())
		} else {
			for i, name := range f.Allowed {
				if _, ok := spec.PortFunctions.Lookup(name); !ok {
					l.Errorf("puzzle.hardware.functions", fmt.Sprintf("%s.port_functions.allowed[%d]", path, i), "unknown port function %q (supported: %s)", name, cell.FunctionNames(spec.PortFunctions))
				}
			}
			for _, name := range cell.SortedKeys(f.Max) {
				fn, ok := spec.PortFunctions.Lookup(name)
				switch {
				case !ok:
					l.Errorf("puzzle.hardware.functions", path+".port_functions.max."+name, "unknown port function %q", name)
				case f.Max[name] < fn.Min:
					l.Errorf("puzzle.hardware.functions", path+".port_functions.max."+name, "%s requires at least %d %s port(s); the puzzle cannot allow fewer", a.Type(), fn.Min, fn.Name)
				}
			}
			if len(f.Allowed) > 0 {
				for _, fn := range spec.PortFunctions.Functions {
					if fn.Min == 0 {
						continue
					}
					found := false
					for _, al := range f.Allowed {
						found = found || strings.EqualFold(al, fn.Name)
					}
					if !found {
						l.Errorf("puzzle.hardware.functions", path+".port_functions.allowed", "%s requires a %s port, so it must stay allowed", a.Type(), fn.Name)
					}
				}
			}
		}
	}
	schema := map[string]cell.ConfigField{}
	for _, f := range a.ConfigSchema() {
		schema[f.Name] = f
	}
	for _, name := range cell.SortedKeys(r.Config) {
		cr := r.Config[name]
		fp := path + ".config." + name
		f, ok := schema[name]
		if !ok {
			l.Errorf("puzzle.hardware.config", fp, "%s has no configuration field %q", a.Type(), name)
			continue
		}
		if cr.Min != nil || cr.Max != nil {
			if f.Type != cell.FieldInt {
				l.Errorf("puzzle.hardware.config", fp, "min/max only apply to integer fields")
				continue
			}
			if (cr.Min != nil && f.Min != nil && *cr.Min < *f.Min) || (cr.Max != nil && f.Max != nil && *cr.Max > *f.Max) ||
				(cr.Min != nil && cr.Max != nil && *cr.Min > *cr.Max) {
				l.Errorf("puzzle.hardware.config", fp, "range exceeds what %s supports (%s)", a.Type(), fieldRange(f))
			}
		}
		for i, v := range cr.Values {
			if errs := a.ValidateConfig(map[string]any{name: v}); hasFieldErr(errs, name) {
				l.Errorf("puzzle.hardware.config", fmt.Sprintf("%s.values[%d]", fp, i), "%v is not a valid %s for %s", v, name, a.Type())
			}
		}
	}
	if c := r.Cost; c != nil {
		if !spec.Overrides.Cost {
			l.Errorf("puzzle.hardware.override", path+".cost", "%s does not allow puzzles to override its cost", a.Type())
		}
		if (c.Base != nil && *c.Base < 0) || (c.PerPort != nil && *c.PerPort < 0) {
			l.Errorf("puzzle.hardware.cost", path+".cost", "costs must be nonnegative")
		}
		for _, name := range cell.SortedKeys(c.Properties) {
			o := c.Properties[name]
			if o.Cost < 0 || o.Per < 1 {
				l.Errorf("puzzle.hardware.cost", path+".cost.properties."+name, "property cost needs cost >= 0 and per >= 1")
			}
			if name != "program_memory" {
				if f, ok := schema[name]; !ok || f.Type != cell.FieldInt {
					l.Errorf("puzzle.hardware.cost", path+".cost.properties."+name, "%q is not an integer property of %s", name, a.Type())
				}
			} else if spec.ProgramMemory == nil {
				l.Errorf("puzzle.hardware.cost", path+".cost.properties."+name, "%s has no program memory", a.Type())
			}
		}
	}
}

func hasFieldErr(errs []cell.ConfigError, name string) bool {
	for _, e := range errs {
		if e.Field == name {
			return true
		}
	}
	return false
}

func fieldRange(f cell.ConfigField) string {
	lo, hi := "-inf", "+inf"
	if f.Min != nil {
		lo = fmt.Sprint(*f.Min)
	}
	if f.Max != nil {
		hi = fmt.Sprint(*f.Max)
	}
	return lo + ".." + hi
}

func checkRange(l *diag.List, path string, r *Range, min, max int) {
	if r == nil {
		return
	}
	if r.Min < 0 || r.Max < 0 || (r.Max > 0 && r.Min > r.Max) {
		l.Errorf("puzzle.hardware.range", path, "invalid range min %d, max %d", r.Min, r.Max)
		return
	}
	if r.Min > max || (r.Max > 0 && r.Max < min) {
		l.Errorf("puzzle.hardware.range", path, "range %d-%d does not overlap the supported range %d-%d", r.Min, r.Max, min, max)
	}
	if r.Default != 0 && (r.Default < r.Min || (r.Max > 0 && r.Default > r.Max) || r.Default < min || r.Default > max) {
		l.Errorf("puzzle.hardware.range", path+".default", "default %d is outside the allowed range", r.Default)
	}
}

// CheckNode checks a player node's configuration against the puzzle's
// rules for its type (the plugin's own checks are separate; see
// cell.ValidateHardware). Errors use the same field names.
func (e Effective) CheckNode(hc cell.HardwareConfig) []cell.ConfigError {
	var errs []cell.ConfigError
	if hc.Ports < e.Ports.Min || hc.Ports > e.Ports.Max {
		errs = append(errs, cell.ConfigError{Field: "ports", Message: fmt.Sprintf("this puzzle allows %s with %s ports, not %d", e.Type, span(e.Ports.Min, e.Ports.Max), hc.Ports)})
	}
	if m := e.ProgramMemory; m != nil && (hc.ProgramMemory < m.Min || hc.ProgramMemory > m.Max) {
		errs = append(errs, cell.ConfigError{Field: "program_memory", Message: fmt.Sprintf("this puzzle allows %s with %s bytes of program memory, not %d", e.Type, span(m.Min, m.Max), hc.ProgramMemory)})
	}
	if e.Spec.PortFunctions != nil {
		fns := hc.PortFunctions
		if fns == nil {
			fns = e.Spec.PortFunctions.DefaultAssignment(hc.Ports)
		}
		counts := map[string]int{}
		for i, name := range fns {
			fn, ok := e.Spec.PortFunctions.Lookup(name)
			if !ok {
				continue // reported by the plugin check
			}
			counts[fn.Name]++
			allowed := false
			for _, a := range e.Functions {
				allowed = allowed || a == fn.Name
			}
			if !allowed {
				errs = append(errs, cell.ConfigError{Field: fmt.Sprintf("port_functions[%d]", i), Message: fmt.Sprintf("this puzzle does not allow %s ports (allowed: %s)", fn.Name, strings.Join(e.Functions, ", "))})
			}
		}
		for _, name := range cell.SortedKeys(e.FunctionMax) {
			if counts[name] > e.FunctionMax[name] {
				errs = append(errs, cell.ConfigError{Field: "port_functions", Message: fmt.Sprintf("this puzzle allows at most %d %s port(s), has %d", e.FunctionMax[name], name, counts[name])})
			}
		}
	}
	for _, name := range cell.SortedKeys(e.Config) {
		cr := e.Config[name]
		v, set := hc.Config[name]
		if !set {
			v = defaultOf(e.archSchema(), name)
		}
		if n, ok := cell.ToInt64(v); ok {
			if (cr.Min != nil && n < *cr.Min) || (cr.Max != nil && n > *cr.Max) {
				errs = append(errs, cell.ConfigError{Field: "config." + name, Message: fmt.Sprintf("this puzzle allows %s %s, not %d", name, crRange(cr), n)})
				continue
			}
		}
		if len(cr.Values) > 0 {
			ok := false
			for _, a := range cr.Values {
				ok = ok || fmt.Sprint(a) == fmt.Sprint(v)
			}
			if !ok {
				errs = append(errs, cell.ConfigError{Field: "config." + name, Message: fmt.Sprintf("this puzzle allows %s to be one of %v, not %v", name, cr.Values, v)})
			}
		}
	}
	return errs
}

func (e Effective) archSchema() []cell.ConfigField { return e.schema }

func defaultOf(schema []cell.ConfigField, name string) any {
	for _, f := range schema {
		if f.Name == name {
			return f.Default
		}
	}
	return nil
}

func crRange(cr ConfigRule) string {
	switch {
	case cr.Min != nil && cr.Max != nil:
		return fmt.Sprintf("%d-%d", *cr.Min, *cr.Max)
	case cr.Max != nil:
		return fmt.Sprintf("at most %d", *cr.Max)
	case cr.Min != nil:
		return fmt.Sprintf("at least %d", *cr.Min)
	}
	return "any value"
}

func span(min, max int) string {
	if min == max {
		return fmt.Sprint(min)
	}
	return fmt.Sprintf("%d-%d", min, max)
}

// NodeDefaults fills unset fields of a player node with the effective
// defaults: port count, program memory, and (for configurable port
// functions) a default assignment that respects the puzzle's rules.
func (e Effective) NodeDefaults(hc cell.HardwareConfig) cell.HardwareConfig {
	if hc.Ports == 0 {
		hc.Ports = e.Ports.Default
	}
	if hc.ProgramMemory == 0 && e.ProgramMemory != nil {
		hc.ProgramMemory = e.ProgramMemory.Default
	}
	if hc.PortFunctions == nil {
		hc.PortFunctions = e.DefaultFunctions(hc.Ports)
	}
	hc.Config = cell.WithDefaults(e.schema, hc.Config)
	return hc
}

// DefaultFunctions is the default port-function assignment for n ports:
// the plugin's default, with any function the puzzle disallows (or allows
// fewer times) replaced by the first allowed function that still has room.
// It returns nil when the plugin default already complies, or when port
// functions are not configurable.
func (e Effective) DefaultFunctions(n int) []string {
	f := e.Spec.PortFunctions
	if f == nil || !f.Configurable {
		return nil
	}
	plugin := f.DefaultAssignment(n)
	out := append([]string(nil), plugin...)
	allowed := map[string]bool{}
	for _, a := range e.Functions {
		allowed[a] = true
	}
	room := func(name string, counts map[string]int) bool {
		m := e.FunctionMax[name]
		return allowed[name] && (m == 0 || counts[name] < m)
	}
	counts := map[string]int{}
	changed := false
	for i, name := range out {
		if room(name, counts) {
			counts[name]++
			continue
		}
		out[i], changed = "", true
	}
	for i := range out {
		if out[i] != "" {
			continue
		}
		out[i] = plugin[i] // nothing fits: leave it for validation to report
		for _, cand := range e.Functions {
			if room(cand, counts) {
				out[i] = cand
				counts[cand]++
				break
			}
		}
	}
	if !changed {
		return nil
	}
	return out
}
