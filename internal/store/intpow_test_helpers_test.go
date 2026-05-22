package store

// maxDurationFloat is the largest float64 that fits in a time.Duration
// without overflow. Used as an early-exit threshold in intPow to avoid
// unnecessary iterations when the result will be capped by the caller.
const maxDurationFloat = float64(1<<63 - 1) // math.MaxInt64 as float64

// intPow computes base^exp for non-negative integer exponents.
// Uses iterative multiplication instead of math.Pow to avoid
// floating-point precision loss on integer exponents.
// Only valid for exp >= 0; negative exponents return 1.0.
// Exits early when the result exceeds maxDurationFloat, since callers
// always cap the result to a maximum delay.
func intPow(base float64, exp int) float64 {
	result := 1.0
	for range exp {
		result *= base
		if result > maxDurationFloat {
			return result
		}
	}
	return result
}
