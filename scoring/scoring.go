// Package scoring compares passing solutions on the three independent
// optimisation dimensions (hardware cost, worst-case ticks, program bytes)
// and keeps a personal best per dimension.
//
// Dimensions are never combined into one score. Each personal best is the
// best solution for its own dimension, so a player can hold a cheap slow
// solution and a fast expensive one at the same time. The comparison
// helpers here (Better, Dominates, Frontier) are what leaderboards and
// Pareto views can build on.
package scoring

import (
	"asmgraph/engine"
)

// Dimension names one optimisation dimension.
type Dimension string

const (
	Cost  Dimension = "cost"
	Ticks Dimension = "ticks"
	Bytes Dimension = "bytes"
)

// Dimensions lists every dimension in display order.
var Dimensions = []Dimension{Cost, Ticks, Bytes}

// Value returns the score's value in dimension d.
func Value(s engine.Score, d Dimension) int64 {
	switch d {
	case Cost:
		return int64(s.Cost)
	case Ticks:
		return s.Ticks
	case Bytes:
		return int64(s.Bytes)
	}
	return 0
}

// Better reports whether a beats b in dimension d. A tie in d is broken by
// the other dimensions in the fixed order cost, ticks, bytes, so an equal
// result that is better elsewhere still replaces a personal best; fully
// equal scores are not better.
func Better(a, b engine.Score, d Dimension) bool {
	if va, vb := Value(a, d), Value(b, d); va != vb {
		return va < vb
	}
	for _, o := range Dimensions {
		if o == d {
			continue
		}
		if va, vb := Value(a, o), Value(b, o); va != vb {
			return va < vb
		}
	}
	return false
}

// Dominates reports whether a is at least as good as b in every dimension
// and strictly better in at least one (Pareto dominance).
func Dominates(a, b engine.Score) bool {
	strict := false
	for _, d := range Dimensions {
		va, vb := Value(a, d), Value(b, d)
		if va > vb {
			return false
		}
		if va < vb {
			strict = true
		}
	}
	return strict
}

// Frontier returns the scores that no other score dominates (the Pareto
// frontier), in input order. Duplicates are kept once.
func Frontier(scores []engine.Score) []engine.Score {
	var out []engine.Score
	for i, s := range scores {
		dominated := false
		for j, o := range scores {
			if i != j && (Dominates(o, s) || (o == s && j < i)) {
				dominated = true
				break
			}
		}
		if !dominated {
			out = append(out, s)
		}
	}
	return out
}
