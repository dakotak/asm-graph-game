// Package vm defines the machine-level contracts shared by every cell:
// the universal integer type, port identifiers, instruction-set
// descriptions, and the versioned bytecode encoding.
package vm

import "math"

// Word is the single integer type carried by every register, stack slot,
// stream value, and port transfer. It is a signed 32-bit integer.
//
// Arithmetic on Word wraps with two's-complement semantics. Go defines
// signed overflow for fixed-width integers this way, so the helpers below
// are thin, explicit wrappers that document the contract.
type Word = int32

const (
	// MinWord is the smallest representable value (-2147483648).
	MinWord Word = math.MinInt32
	// MaxWord is the largest representable value (2147483647).
	MaxWord Word = math.MaxInt32
)

// Add returns a+b with two's-complement wraparound.
func Add(a, b Word) Word { return a + b }

// Sub returns a-b with two's-complement wraparound.
func Sub(a, b Word) Word { return a - b }

// Neg returns -a with two's-complement wraparound. Neg(MinWord) == MinWord.
func Neg(a Word) Word { return -a }

// FromInt64 converts v to a Word, reporting false when v is out of range.
// Literals and stream values are range-checked, never silently wrapped.
func FromInt64(v int64) (Word, bool) {
	if v < math.MinInt32 || v > math.MaxInt32 {
		return 0, false
	}
	return Word(v), true
}
