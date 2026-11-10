// Package validate implements output validation outside the cell
// execution engine. V1 ships exact-sequence validation, applied
// incrementally so a test can end the moment every output is satisfied.
package validate

import "asmgraph/vm"

// State is the validation state of one output stream.
type State string

const (
	Pending State = "pending"
	Passed  State = "passed"
	Failed  State = "failed"
)

// Mismatch describes the first wrong value received on a stream.
type Mismatch struct {
	Index    int      `json:"index"`
	Expected *vm.Word `json:"expected,omitempty"` // nil: value arrived after the stream was complete
	Got      vm.Word  `json:"got"`
}

// Exact validates that a stream receives exactly Expected, in order.
//
//   - A correct value advances progress.
//   - A wrong value fails the stream immediately.
//   - Receiving the final expected value passes the stream.
//   - Any value received after the stream passed fails it ("extra output").
//   - An empty expected stream is passed from the start, and fails on the
//     first value received.
type Exact struct {
	Expected []vm.Word
	Received int
	State    State
	Mismatch *Mismatch
}

// NewExact creates a validator for an expected sequence.
func NewExact(expected []vm.Word) *Exact {
	e := &Exact{Expected: expected, State: Pending}
	if len(expected) == 0 {
		e.State = Passed
	}
	return e
}

// Observe records one received value and reports whether State changed.
func (e *Exact) Observe(v vm.Word) bool {
	if e.State == Failed {
		return false
	}
	before := e.State
	idx := e.Received
	e.Received++
	if idx >= len(e.Expected) {
		e.State = Failed
		e.Mismatch = &Mismatch{Index: idx, Got: v}
		return true
	}
	if e.Expected[idx] != v {
		exp := e.Expected[idx]
		e.State = Failed
		e.Mismatch = &Mismatch{Index: idx, Expected: &exp, Got: v}
		return true
	}
	if e.Received == len(e.Expected) {
		e.State = Passed
	}
	return e.State != before
}
