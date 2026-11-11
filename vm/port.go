package vm

import (
	"strconv"
	"strings"
)

// Port identifies a communication port operand. Physical ports are
// numbered P0, P1, P2, ... up to the cell's configured port count; ANY and
// LAST are virtual selectors resolved by the engine at communication time
// over the cell's configured ports only.
type Port uint8

// The first four physical ports. Every legacy (schema v1) cell has
// exactly these.
const (
	P0 Port = iota
	P1
	P2
	P3
)

// Virtual selectors.
const (
	PortAny  Port = 0xFE
	PortLast Port = 0xFF
)

// MaxPorts is the encoding ceiling for physical ports: P0..P63. Each cell
// architecture declares its own (usually much smaller) supported range;
// this constant only bounds what the bytecode and the assembler can name.
const MaxPorts = 64

// LegacyPorts is the fixed port count of schema-v1 puzzle cells.
const LegacyPorts = 4

// PortN returns physical port i. It panics if i is outside [0, MaxPorts).
func PortN(i int) Port {
	if i < 0 || i >= MaxPorts {
		panic("vm.PortN: port index out of range")
	}
	return Port(i)
}

func (p Port) String() string {
	switch {
	case p == PortAny:
		return "ANY"
	case p == PortLast:
		return "LAST"
	case p.Physical():
		return "P" + strconv.Itoa(int(p))
	}
	return "P?"
}

// Physical reports whether p is a physical port P0..P63.
func (p Port) Physical() bool { return p < MaxPorts }

// Index returns the physical port number (meaningless for ANY and LAST).
func (p Port) Index() int { return int(p) }

// ParsePort parses a port operand name (case-insensitive): P0..P63 written
// canonically (no leading zeros), ANY, or LAST.
func ParsePort(s string) (Port, bool) {
	u := strings.ToUpper(s)
	switch u {
	case "ANY":
		return PortAny, true
	case "LAST":
		return PortLast, true
	}
	if len(u) < 2 || len(u) > 3 || u[0] != 'P' {
		return 0, false
	}
	digits := u[1:]
	if len(digits) > 1 && digits[0] == '0' {
		return 0, false
	}
	n := 0
	for _, r := range digits {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int(r-'0')
	}
	if n >= MaxPorts {
		return 0, false
	}
	return Port(n), true
}

// ParsePhysicalPort parses P0..P63 only (case-insensitive).
func ParsePhysicalPort(s string) (Port, bool) {
	p, ok := ParsePort(s)
	if !ok || !p.Physical() {
		return 0, false
	}
	return p, true
}

// PortRange formats the physical ports of a cell with n ports, e.g.
// "P0-P3", for messages.
func PortRange(n int) string {
	switch {
	case n <= 0:
		return "no ports"
	case n == 1:
		return "P0"
	}
	return "P0-P" + strconv.Itoa(n-1)
}
