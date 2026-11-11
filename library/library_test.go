package library

import (
	"os"
	"strings"
	"testing"

	"asmgraph/cells"
)

func TestSummarize(t *testing.T) {
	reg := cells.NewRegistry()
	data, err := os.ReadFile("../examples/puzzles/stack-reverse.yaml")
	if err != nil {
		t.Fatal(err)
	}
	s, p := Summarize(reg, "stack-reverse.yaml", data)
	if p == nil || !s.Valid || s.ID != "stack-reverse" || !s.Hardware || s.Tests != 3 || strings.Join(s.CellTypes, ",") != "input,output" {
		t.Errorf("summary %+v", s)
	}
	s, p = Summarize(reg, "dir/broken.yaml", []byte("schema_version: 2\nid: [\n"))
	if p != nil || s.Valid || s.ID != "broken" || len(s.Diagnostics) == 0 {
		t.Errorf("broken file: %+v", s)
	}
	s, _ = Summarize(reg, "bad.yaml", []byte("schema_version: 2\nid: bad\ncells: []\ntests: []\n"))
	if s.Valid || s.ID != "bad" {
		t.Errorf("invalid puzzle should parse but not validate: %+v", s)
	}
}
