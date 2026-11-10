// Package graph models the machine topology: puzzle-defined cells with
// enabled port slots, joined by player-defined bidirectional connections.
// Layout and positions are not part of the topology.
package graph

import (
	"fmt"

	"asmgraph/vm"
)

// Endpoint names one physical port of one cell, as written in a solution.
type Endpoint struct {
	Cell string `json:"cell" yaml:"cell"`
	Port string `json:"port" yaml:"port"`
}

func (e Endpoint) String() string { return e.Cell + "." + e.Port }

// Connection joins two endpoints. Direction is irrelevant: connections are
// bidirectional; "from"/"to" exist only for readability.
type Connection struct {
	From Endpoint `json:"from" yaml:"from"`
	To   Endpoint `json:"to" yaml:"to"`
}

// CellPorts describes a cell's identity and which port slots the puzzle
// author enabled.
type CellPorts struct {
	ID      string
	Enabled [vm.NumPorts]bool
}

// Link is a resolved port reference.
type Link struct {
	Cell int
	Port vm.Port
}

// Error is a topology validation error for connection Index.
type Error struct {
	Index int    // connection index
	Field string // "from.cell", "to.port", or "" for the whole connection
	Msg   string
}

func (e Error) Error() string {
	if e.Field != "" {
		return fmt.Sprintf("connections[%d].%s: %s", e.Index, e.Field, e.Msg)
	}
	return fmt.Sprintf("connections[%d]: %s", e.Index, e.Msg)
}

// Path returns the YAML path of the error within a solution file.
func (e Error) Path() string {
	if e.Field != "" {
		return fmt.Sprintf("connections[%d].%s", e.Index, e.Field)
	}
	return fmt.Sprintf("connections[%d]", e.Index)
}

// Topology is a validated, immutable cell graph.
type Topology struct {
	cells []CellPorts
	index map[string]int
	peers [][vm.NumPorts]Link // Cell == -1 means unconnected
	links []resolved          // in solution order
}

type resolved struct{ A, B Link }

// Build validates connections against the cells and returns the topology.
// Every error is reported; the topology is nil if there are any.
//
// Rules: endpoints must name existing cells and physical ports P0..P3 that
// the puzzle enabled; a port joins at most one connection; a cell may not
// connect to itself; parallel connections between the same two cells are
// fine when they use distinct ports.
func Build(cells []CellPorts, conns []Connection) (*Topology, []Error) {
	t := &Topology{
		cells: append([]CellPorts(nil), cells...),
		index: make(map[string]int, len(cells)),
		peers: make([][vm.NumPorts]Link, len(cells)),
	}
	for i, c := range cells {
		t.index[c.ID] = i
		for p := range t.peers[i] {
			t.peers[i][p] = Link{Cell: -1}
		}
	}
	var errs []Error
	used := map[Link]int{}
	for i, c := range conns {
		a, okA := t.resolve(i, "from", c.From, &errs)
		b, okB := t.resolve(i, "to", c.To, &errs)
		if !okA || !okB {
			continue
		}
		if a.Cell == b.Cell {
			errs = append(errs, Error{i, "", fmt.Sprintf("cell %q cannot connect to itself", c.From.Cell)})
			continue
		}
		dup := false
		for _, e := range []struct {
			l     Link
			field string
			ep    Endpoint
		}{{a, "from", c.From}, {b, "to", c.To}} {
			if prev, ok := used[e.l]; ok {
				errs = append(errs, Error{i, e.field, fmt.Sprintf("port %s is already used by connections[%d]", e.ep, prev)})
				dup = true
			}
		}
		if dup {
			continue
		}
		used[a], used[b] = i, i
		t.peers[a.Cell][a.Port] = b
		t.peers[b.Cell][b.Port] = a
		t.links = append(t.links, resolved{a, b})
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return t, nil
}

func (t *Topology) resolve(i int, side string, e Endpoint, errs *[]Error) (Link, bool) {
	ci, ok := t.index[e.Cell]
	if !ok {
		*errs = append(*errs, Error{i, side + ".cell", fmt.Sprintf("unknown cell %q", e.Cell)})
		return Link{}, false
	}
	p, ok := vm.ParsePhysicalPort(e.Port)
	if !ok {
		*errs = append(*errs, Error{i, side + ".port", fmt.Sprintf("invalid port %q (expected P0, P1, P2, or P3)", e.Port)})
		return Link{}, false
	}
	if !t.cells[ci].Enabled[p] {
		*errs = append(*errs, Error{i, side + ".port", fmt.Sprintf("port %s is disabled on cell %q by the puzzle", p, e.Cell)})
		return Link{}, false
	}
	return Link{Cell: ci, Port: p}, true
}

// NumCells returns the number of cells.
func (t *Topology) NumCells() int { return len(t.cells) }

// CellID returns the identifier of cell i.
func (t *Topology) CellID(i int) string { return t.cells[i].ID }

// CellIndex returns the index of the named cell.
func (t *Topology) CellIndex(id string) (int, bool) {
	i, ok := t.index[id]
	return i, ok
}

// Enabled reports whether port p of cell i is enabled by the puzzle.
func (t *Topology) Enabled(i int, p vm.Port) bool {
	return p.Physical() && t.cells[i].Enabled[p]
}

// Peer returns the port connected to port p of cell i.
func (t *Topology) Peer(i int, p vm.Port) (Link, bool) {
	if !p.Physical() {
		return Link{}, false
	}
	l := t.peers[i][p]
	return l, l.Cell >= 0
}

// NumConnections returns the number of connections.
func (t *Topology) NumConnections() int { return len(t.links) }

// Link returns connection k as two resolved endpoints, in solution order.
func (t *Topology) Link(k int) (a, b Link) { return t.links[k].A, t.links[k].B }

// Connections returns the connections in canonical (solution) order.
func (t *Topology) Connections() []Connection {
	out := make([]Connection, len(t.links))
	for k, l := range t.links {
		out[k] = Connection{
			From: Endpoint{t.cells[l.A.Cell].ID, l.A.Port.String()},
			To:   Endpoint{t.cells[l.B.Cell].ID, l.B.Port.String()},
		}
	}
	return out
}
