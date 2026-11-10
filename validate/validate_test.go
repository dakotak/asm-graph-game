package validate

import "testing"

func TestExact(t *testing.T) {
	e := NewExact([]int32{1, 2})
	if e.State != Pending || e.Observe(1) || e.State != Pending {
		t.Fatal("first correct value should keep the stream pending")
	}
	if !e.Observe(2) || e.State != Passed {
		t.Fatal("final value should pass the stream")
	}
	if !e.Observe(3) || e.State != Failed || e.Mismatch.Expected != nil || e.Mismatch.Index != 2 {
		t.Fatalf("extra value should fail: %+v", e.Mismatch)
	}
	if e.Observe(4) {
		t.Fatal("failed stays failed")
	}
	w := NewExact([]int32{5})
	if !w.Observe(6) || w.State != Failed || *w.Mismatch.Expected != 5 || w.Mismatch.Got != 6 {
		t.Fatalf("wrong value: %+v", w.Mismatch)
	}
	empty := NewExact(nil)
	if empty.State != Passed || !empty.Observe(0) || empty.State != Failed {
		t.Fatal("empty expectation: passed until any value arrives")
	}
}
