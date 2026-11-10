package vm

import "strings"

// Port identifies a communication port operand. P0..P3 are the four
// physical port slots every cell has; ANY and LAST are virtual selectors
// resolved by the engine at communication time.
type Port uint8

const (
	P0 Port = iota
	P1
	P2
	P3
	PortAny
	PortLast
)

// NumPorts is the number of physical port slots per cell in V1.
const NumPorts = 4

var portNames = [...]string{"P0", "P1", "P2", "P3", "ANY", "LAST"}

func (p Port) String() string {
	if int(p) < len(portNames) {
		return portNames[p]
	}
	return "P?"
}

// Physical reports whether p is one of P0..P3.
func (p Port) Physical() bool { return p < NumPorts }

// ParsePort parses a port operand name (case-insensitive), including the
// virtual selectors ANY and LAST.
func ParsePort(s string) (Port, bool) {
	u := strings.ToUpper(s)
	for i, n := range portNames {
		if n == u {
			return Port(i), true
		}
	}
	return 0, false
}

// ParsePhysicalPort parses P0..P3 only (case-insensitive).
func ParsePhysicalPort(s string) (Port, bool) {
	p, ok := ParsePort(s)
	if !ok || !p.Physical() {
		return 0, false
	}
	return p, true
}
