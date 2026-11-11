package scoring

import (
	"reflect"
	"testing"
	"time"

	"asmgraph/engine"
)

func TestBetterAndDominates(t *testing.T) {
	cheap := engine.Score{Cost: 20, Ticks: 40, Bytes: 12}
	fast := engine.Score{Cost: 54, Ticks: 28, Bytes: 24}
	if !Better(cheap, fast, Cost) || !Better(fast, cheap, Ticks) || !Better(cheap, fast, Bytes) {
		t.Error("each dimension compares independently")
	}
	if Dominates(cheap, fast) || Dominates(fast, cheap) {
		t.Error("a tradeoff is not dominance")
	}
	// Equal in the dimension: the remaining dimensions break the tie.
	tie := engine.Score{Cost: 20, Ticks: 39, Bytes: 12}
	if !Better(tie, cheap, Cost) || Better(cheap, cheap, Cost) {
		t.Error("tie-breaking")
	}
	worse := engine.Score{Cost: 60, Ticks: 50, Bytes: 30}
	if !Dominates(cheap, worse) {
		t.Error("dominance")
	}
	got := Frontier([]engine.Score{cheap, fast, worse, cheap})
	if !reflect.DeepEqual(got, []engine.Score{cheap, fast}) {
		t.Errorf("frontier %v", got)
	}
}

func TestStoreKeepsIndependentBests(t *testing.T) {
	st := &Store{Root: t.TempDir(), Now: func() time.Time { return time.Unix(0, 0) }}
	cheap := engine.Score{Cost: 20, Ticks: 40, Bytes: 12}
	fast := engine.Score{Cost: 54, Ticks: 28, Bytes: 24}

	rec, improved, err := st.Submit("p", cheap, []byte("cheap"))
	if err != nil || len(improved) != 3 {
		t.Fatalf("first submission improves everything: %v %v", improved, err)
	}
	rec, improved, err = st.Submit("p", fast, []byte("fast"))
	if err != nil || !reflect.DeepEqual(improved, []Dimension{Ticks}) {
		t.Fatalf("fast only improves ticks: %v %v", improved, err)
	}
	if rec.Bests[Cost].Score != cheap || rec.Bests[Ticks].Score != fast {
		t.Errorf("bests %+v", rec.Bests)
	}
	if _, improved, _ = st.Submit("p", cheap, []byte("again")); len(improved) != 0 {
		t.Errorf("an equal score is not an improvement: %v", improved)
	}
	// Bests survive a reload, and each keeps its own solution.
	rec, err = st.Load("p")
	if err != nil || rec.Bests[Bytes].Score != cheap {
		t.Fatal(rec, err)
	}
	for d, want := range map[Dimension]string{Cost: "cheap", Ticks: "fast", Bytes: "cheap"} {
		if data, err := st.Solution("p", d); err != nil || string(data) != want {
			t.Errorf("%s: %q %v", d, data, err)
		}
	}
}
