package graph

import (
	"strings"
	"testing"

	"asmgraph/vm"
)

var all = [vm.NumPorts]bool{true, true, true, true}

func cellsFixture() []CellPorts {
	return []CellPorts{
		{ID: "a", Enabled: all},
		{ID: "b", Enabled: all},
		{ID: "in", Enabled: [vm.NumPorts]bool{true, false, false, false}},
	}
}

func conn(c1, p1, c2, p2 string) Connection {
	return Connection{From: Endpoint{c1, p1}, To: Endpoint{c2, p2}}
}

func TestValidConnections(t *testing.T) {
	topo, errs := Build(cellsFixture(), []Connection{
		conn("in", "P0", "a", "P0"),
		conn("a", "P1", "b", "P3"),
	})
	if errs != nil {
		t.Fatal(errs)
	}
	l, ok := topo.Peer(0, vm.P1)
	if !ok || l.Cell != 1 || l.Port != vm.P3 {
		t.Errorf("a.P1 peer = %+v", l)
	}
	// Bidirectional.
	l, ok = topo.Peer(1, vm.P3)
	if !ok || l.Cell != 0 || l.Port != vm.P1 {
		t.Errorf("b.P3 peer = %+v", l)
	}
	if _, ok := topo.Peer(0, vm.P2); ok {
		t.Error("a.P2 should be unconnected")
	}
	if topo.NumConnections() != 2 || topo.Connections()[1].To.Port != "P3" {
		t.Errorf("connections = %+v", topo.Connections())
	}
}

func TestMultipleEdgesBetweenSameCells(t *testing.T) {
	_, errs := Build(cellsFixture(), []Connection{
		conn("a", "P0", "b", "P0"),
		conn("a", "P1", "b", "P1"),
		conn("b", "P2", "a", "P2"),
	})
	if errs != nil {
		t.Fatalf("parallel edges on distinct ports should be allowed: %v", errs)
	}
}

func TestCyclesAndDisconnectedComponentsAllowed(t *testing.T) {
	cells := []CellPorts{{ID: "x", Enabled: all}, {ID: "y", Enabled: all}, {ID: "z", Enabled: all}, {ID: "lonely", Enabled: all}}
	_, errs := Build(cells, []Connection{conn("x", "P0", "y", "P0"), conn("y", "P1", "z", "P1"), conn("z", "P2", "x", "P2")})
	if errs != nil {
		t.Fatal(errs)
	}
}

func TestInvalidConnections(t *testing.T) {
	cases := []struct {
		name string
		c    []Connection
		want string
		path string
	}{
		{"disabled port", []Connection{conn("in", "P1", "a", "P0")}, "disabled", "connections[0].from.port"},
		{"invalid port id", []Connection{conn("a", "P4", "b", "P0")}, "invalid port", "connections[0].from.port"},
		{"virtual port", []Connection{conn("a", "ANY", "b", "P0")}, "invalid port", "connections[0].from.port"},
		{"unknown cell", []Connection{conn("a", "P0", "nope", "P0")}, "unknown cell", "connections[0].to.cell"},
		{"self connection", []Connection{conn("a", "P0", "a", "P1")}, "itself", "connections[0]"},
		{"duplicate connection", []Connection{conn("a", "P0", "b", "P0"), conn("a", "P0", "b", "P0")}, "already used", "connections[1].from"},
		{"port reuse", []Connection{conn("a", "P0", "b", "P0"), conn("in", "P0", "b", "P0")}, "already used", "connections[1].to"},
	}
	for _, c := range cases {
		_, errs := Build(cellsFixture(), c.c)
		if len(errs) == 0 {
			t.Errorf("%s: no error", c.name)
			continue
		}
		found := false
		for _, e := range errs {
			if strings.Contains(e.Msg, c.want) && e.Path() == c.path {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: want %q at %s, got %v", c.name, c.want, c.path, errs)
		}
	}
}

func TestAllErrorsReported(t *testing.T) {
	_, errs := Build(cellsFixture(), []Connection{
		conn("a", "P9", "b", "P0"),
		conn("ghost", "P0", "b", "P1"),
		conn("b", "P2", "b", "P3"),
	})
	if len(errs) != 3 {
		t.Errorf("want 3 errors, got %d: %v", len(errs), errs)
	}
}
