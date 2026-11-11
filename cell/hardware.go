package cell

import (
	"fmt"
	"sort"
	"strings"

	"asmgraph/vm"
)

// Category classifies hardware.
type Category string

const (
	// CategoryProcessor executes player-written assembly (it has an ISA).
	CategoryProcessor Category = "processor"
	// CategoryPeripheral is passive hardware: it runs no player program and
	// exposes its behaviour through port functions (for example a stack).
	CategoryPeripheral Category = "peripheral"
	// CategoryIO is a puzzle-supplied stream endpoint (input or output).
	CategoryIO Category = "io"
)

// PortSpec is the port-count range an architecture supports.
type PortSpec struct {
	Min     int `json:"min"`
	Max     int `json:"max"`
	Default int `json:"default"`
}

// Configurable reports whether the port count can be chosen.
func (p PortSpec) Configurable() bool { return p.Min < p.Max }

// Direction is the data direction of a peripheral port function, seen from
// the neighbouring cell.
type Direction string

const (
	// DirectionIn: neighbours write values into the peripheral.
	DirectionIn Direction = "in"
	// DirectionOut: neighbours read values from the peripheral.
	DirectionOut Direction = "out"
)

// PortFunction is one behaviour a peripheral port can be assigned.
type PortFunction struct {
	Name      string    `json:"name"`
	Direction Direction `json:"direction"`
	Summary   string    `json:"summary"`
	// Min and Max bound how many ports may have this function (Max 0 =
	// unlimited).
	Min int `json:"min,omitempty"`
	Max int `json:"max,omitempty"`
}

// PortFunctions declares the functions a peripheral's ports can perform.
type PortFunctions struct {
	Functions []PortFunction `json:"functions"`
	// Configurable is true when players assign a function to each port.
	// Otherwise every port uses Default.
	Configurable bool `json:"configurable"`
	// Default is the assignment used when none is given: port i gets
	// Default[i % len(Default)].
	Default []string `json:"default"`
	// Rule documents any further combination rule the plugin enforces in
	// HardwareValidator (shown to players).
	Rule string `json:"rule,omitempty"`
}

// Lookup finds a function by name (case-insensitive).
func (f *PortFunctions) Lookup(name string) (PortFunction, bool) {
	for _, fn := range f.Functions {
		if strings.EqualFold(fn.Name, name) {
			return fn, true
		}
	}
	return PortFunction{}, false
}

// DefaultAssignment returns the default function of each of n ports.
func (f *PortFunctions) DefaultAssignment(n int) []string {
	out := make([]string, n)
	for i := range out {
		if len(f.Default) > 0 {
			out[i] = f.Default[i%len(f.Default)]
		}
	}
	return out
}

// PropertyCost charges for a configurable property: Cost for every Per
// units of the property's value, rounded up. Property is "program_memory"
// or the name of an integer config field.
type PropertyCost struct {
	Property string `json:"property"`
	Cost     int    `json:"cost"`
	Per      int    `json:"per"`
}

// CostModel is the hardware cost of one instance:
//
//	Base + PerPort × ports + Σ properties ⌈value / Per⌉ × Cost
type CostModel struct {
	Base       int            `json:"base"`
	PerPort    int            `json:"perPort"`
	Properties []PropertyCost `json:"properties,omitempty"`
}

// MemorySpec is the program-memory range (bytes) of a processor.
type MemorySpec struct {
	Min     int `json:"min"`
	Max     int `json:"max"`
	Default int `json:"default"`
}

// Overrides lists what puzzle authors may change about an architecture.
// Restricting ranges and choices is always allowed; overriding the cost
// model or the defaults only where the plugin permits it.
type Overrides struct {
	Cost     bool `json:"cost"`
	Defaults bool `json:"defaults"`
}

// HardwareSpec describes an architecture as hardware: its category, the
// physical limits players and puzzles must stay within, and its cost.
type HardwareSpec struct {
	Category      Category       `json:"category"`
	Ports         PortSpec       `json:"ports"`
	PortFunctions *PortFunctions `json:"portFunctions,omitempty"`
	ProgramMemory *MemorySpec    `json:"programMemory,omitempty"`
	Cost          CostModel      `json:"cost"`
	// PuzzleOnly hardware can only be placed by puzzle authors (stream I/O).
	PuzzleOnly bool      `json:"puzzleOnly,omitempty"`
	Overrides  Overrides `json:"overrides"`
}

// Hardware is optionally implemented by architectures to describe
// themselves as hardware. See HardwareOf for the defaults used otherwise.
type Hardware interface {
	Hardware() HardwareSpec
}

// Default hardware descriptions for architectures that do not implement
// Hardware.
var (
	DefaultProcessorCost  = CostModel{Base: 10, PerPort: 2}
	DefaultPeripheralCost = CostModel{Base: 5, PerPort: 1}
	DefaultProgramMemory  = MemorySpec{Min: 1, Max: vm.MaxProgramBytes, Default: 32}
)

// HardwareOf returns a's hardware description. Architectures that do not
// implement Hardware get four fixed ports, the default cost for their
// category, and are puzzle-only if they bind test streams.
func HardwareOf(a Architecture) HardwareSpec {
	if h, ok := a.(Hardware); ok {
		return h.Hardware()
	}
	hs := HardwareSpec{Ports: PortSpec{Min: vm.LegacyPorts, Max: vm.LegacyPorts, Default: vm.LegacyPorts}}
	_, io := a.(StreamDeclarer)
	switch {
	case a.ISA() != nil:
		hs.Category, hs.Cost = CategoryProcessor, DefaultProcessorCost
		mem := DefaultProgramMemory
		hs.ProgramMemory = &mem
	case io:
		hs.Category, hs.PuzzleOnly = CategoryIO, true
	default:
		hs.Category, hs.Cost = CategoryPeripheral, DefaultPeripheralCost
	}
	return hs
}

// CheckHardwareSpec validates an architecture's own description. The
// registry calls it so a plugin cannot declare impossible limits.
func CheckHardwareSpec(a Architecture) error {
	h := HardwareOf(a)
	t := a.Type()
	switch h.Category {
	case CategoryProcessor:
		if a.ISA() == nil {
			return fmt.Errorf("type %q: category processor requires an ISA", t)
		}
		if h.ProgramMemory == nil {
			return fmt.Errorf("type %q: a processor must declare its program memory range", t)
		}
		m := h.ProgramMemory
		if m.Min < 1 || m.Max > vm.MaxProgramBytes || m.Min > m.Max || m.Default < m.Min || m.Default > m.Max {
			return fmt.Errorf("type %q: invalid program memory range %d..%d (default %d)", t, m.Min, m.Max, m.Default)
		}
	case CategoryPeripheral, CategoryIO:
		if a.ISA() != nil {
			return fmt.Errorf("type %q: category %s cannot have an ISA", t, h.Category)
		}
		if h.ProgramMemory != nil {
			return fmt.Errorf("type %q: only processors have program memory", t)
		}
	default:
		return fmt.Errorf("type %q: unknown hardware category %q", t, h.Category)
	}
	p := h.Ports
	if p.Min < 0 || p.Max > vm.MaxPorts || p.Min > p.Max || p.Default < p.Min || p.Default > p.Max || p.Max == 0 {
		return fmt.Errorf("type %q: invalid port range %d..%d (default %d; at most %d ports)", t, p.Min, p.Max, p.Default, vm.MaxPorts)
	}
	if h.Cost.Base < 0 || h.Cost.PerPort < 0 {
		return fmt.Errorf("type %q: costs must be nonnegative", t)
	}
	for _, pc := range h.Cost.Properties {
		if pc.Cost < 0 || pc.Per < 1 {
			return fmt.Errorf("type %q: property cost %q needs cost >= 0 and per >= 1", t, pc.Property)
		}
	}
	if f := h.PortFunctions; f != nil {
		if len(f.Functions) == 0 || len(f.Default) == 0 {
			return fmt.Errorf("type %q: port functions need at least one function and a default assignment", t)
		}
		seen := map[string]bool{}
		for _, fn := range f.Functions {
			k := strings.ToUpper(fn.Name)
			if fn.Name == "" || seen[k] || (fn.Direction != DirectionIn && fn.Direction != DirectionOut) || fn.Min < 0 || fn.Max < 0 || (fn.Max > 0 && fn.Min > fn.Max) {
				return fmt.Errorf("type %q: invalid port function %q", t, fn.Name)
			}
			seen[k] = true
		}
		for _, d := range f.Default {
			if _, ok := f.Lookup(d); !ok {
				return fmt.Errorf("type %q: default port function %q is not declared", t, d)
			}
		}
	}
	return nil
}

// HardwareConfig is one configured hardware instance: the choices made by
// a player (or a puzzle author for fixed cells).
type HardwareConfig struct {
	Ports         int
	PortFunctions []string // peripherals with port functions; nil = default
	ProgramMemory int      // processors
	Config        map[string]any
}

// HardwareValidator is optionally implemented by architectures that have
// rules beyond what HardwareSpec expresses (for example "at least one
// input port and one output port"). It is called after the generic checks
// pass.
type HardwareValidator interface {
	ValidateHardware(hc HardwareConfig) []ConfigError
}

// ValidateHardware checks hc against the architecture's own physical
// limits (never a puzzle's restrictions): port range, port functions,
// program memory, configuration fields, and the plugin's own rules. Field
// names in errors are "ports", "port_functions", "port_functions[i]",
// "program_memory", and "config.<field>".
func ValidateHardware(a Architecture, hc HardwareConfig) []ConfigError {
	h := HardwareOf(a)
	var errs []ConfigError
	if hc.Ports < h.Ports.Min || hc.Ports > h.Ports.Max {
		errs = append(errs, ConfigError{"ports", fmt.Sprintf("%s supports %s ports, not %d", a.Type(), rangeText(h.Ports.Min, h.Ports.Max), hc.Ports)})
	}
	if h.ProgramMemory != nil {
		m := h.ProgramMemory
		if hc.ProgramMemory < m.Min || hc.ProgramMemory > m.Max {
			errs = append(errs, ConfigError{"program_memory", fmt.Sprintf("%s supports %s bytes of program memory, not %d", a.Type(), rangeText(m.Min, m.Max), hc.ProgramMemory)})
		}
	} else if hc.ProgramMemory != 0 {
		errs = append(errs, ConfigError{"program_memory", fmt.Sprintf("%s is not programmable and has no program memory", a.Type())})
	}
	errs = append(errs, checkFunctions(a.Type(), h.PortFunctions, hc)...)
	for _, e := range a.ValidateConfig(hc.Config) {
		f := "config"
		if e.Field != "" {
			f += "." + e.Field
		}
		errs = append(errs, ConfigError{f, e.Message})
	}
	if len(errs) == 0 {
		if v, ok := a.(HardwareValidator); ok {
			errs = append(errs, v.ValidateHardware(hc)...)
		}
	}
	return errs
}

func checkFunctions(t string, f *PortFunctions, hc HardwareConfig) []ConfigError {
	if f == nil {
		if hc.PortFunctions != nil {
			return []ConfigError{{"port_functions", fmt.Sprintf("%s has no configurable port functions", t)}}
		}
		return nil
	}
	if hc.PortFunctions == nil {
		return nil // the default assignment is always valid for its count
	}
	var errs []ConfigError
	if !f.Configurable {
		return []ConfigError{{"port_functions", fmt.Sprintf("%s port functions are fixed and cannot be assigned", t)}}
	}
	if len(hc.PortFunctions) != hc.Ports {
		return []ConfigError{{"port_functions", fmt.Sprintf("assign exactly one function per port: %d port(s), %d function(s)", hc.Ports, len(hc.PortFunctions))}}
	}
	counts := map[string]int{}
	for i, name := range hc.PortFunctions {
		fn, ok := f.Lookup(name)
		if !ok {
			errs = append(errs, ConfigError{fmt.Sprintf("port_functions[%d]", i), fmt.Sprintf("unknown port function %q (supported: %s)", name, FunctionNames(f))})
			continue
		}
		counts[fn.Name]++
	}
	if len(errs) > 0 {
		return errs
	}
	for _, fn := range f.Functions {
		n := counts[fn.Name]
		if n < fn.Min {
			errs = append(errs, ConfigError{"port_functions", fmt.Sprintf("%s needs at least %d %s port(s), has %d", t, fn.Min, fn.Name, n)})
		}
		if fn.Max > 0 && n > fn.Max {
			errs = append(errs, ConfigError{"port_functions", fmt.Sprintf("%s allows at most %d %s port(s), has %d", t, fn.Max, fn.Name, n)})
		}
	}
	return errs
}

// FunctionNames lists the declared function names.
func FunctionNames(f *PortFunctions) string {
	names := make([]string, len(f.Functions))
	for i, fn := range f.Functions {
		names[i] = fn.Name
	}
	return strings.Join(names, ", ")
}

// Cost computes the cost of hc under model m. Properties that are not set
// count as zero; negative values cannot occur after validation.
func Cost(m CostModel, hc HardwareConfig) int {
	c := m.Base + m.PerPort*hc.Ports
	for _, pc := range m.Properties {
		var v int64
		if pc.Property == "program_memory" {
			v = int64(hc.ProgramMemory)
		} else {
			v = ConfigInt(hc.Config, pc.Property, 0)
		}
		if v > 0 && pc.Per > 0 {
			c += int((v+int64(pc.Per)-1)/int64(pc.Per)) * pc.Cost
		}
	}
	return c
}

func rangeText(min, max int) string {
	if min == max {
		return fmt.Sprint(min)
	}
	return fmt.Sprintf("%d-%d", min, max)
}

// SortedKeys returns the keys of m in order.
func SortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
