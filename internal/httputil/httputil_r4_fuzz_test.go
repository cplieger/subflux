package httputil

import (
	"testing"
	"time"
)

// FuzzSafeDouble tests that SafeDouble never overflows to a negative value
// and always returns a result >= the input for non-negative inputs.
// Bug class: integer overflow when doubling time.Duration (int64 nanoseconds)
// can wrap around to negative if the guard condition has an off-by-one.
func FuzzSafeDouble(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(time.Second))
	f.Add(int64(time.Hour))
	f.Add(int64(time.Duration(1<<62 - 1)))
	f.Add(int64(time.Duration(1<<63 - 1)))
	f.Add(int64(-time.Second))

	f.Fuzz(func(t *testing.T, ns int64) {
		d := time.Duration(ns)
		result := SafeDouble(d)

		// SafeDouble should never produce a negative from a non-negative input.
		if d >= 0 && result < 0 {
			t.Errorf("SafeDouble(%v) = %v, want non-negative", d, result)
		}
		// For non-negative inputs, result must be >= input (doubling or capping).
		if d >= 0 && result < d {
			t.Errorf("SafeDouble(%v) = %v, want >= input", d, result)
		}
	})
}

// FuzzJitteredBackoff tests that JitteredBackoff returns a value within the
// documented range [backoff/2, backoff] for non-negative inputs.
// Bug class: jitter calculation using random number generation can produce
// out-of-range values if the modulus arithmetic has off-by-one errors or
// if negative durations cause unsigned underflow in the half-open range.
func FuzzJitteredBackoff(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(time.Millisecond))
	f.Add(int64(time.Second))
	f.Add(int64(30 * time.Second))
	f.Add(int64(time.Hour))
	f.Add(int64(1))

	f.Fuzz(func(t *testing.T, ns int64) {
		d := time.Duration(ns)
		if d <= 0 {
			return // only test non-negative backoffs
		}
		result := JitteredBackoff(d)

		half := d / 2
		if result < half {
			t.Errorf("JitteredBackoff(%v) = %v, want >= %v (half)", d, result, half)
		}
		if result > d {
			t.Errorf("JitteredBackoff(%v) = %v, want <= %v", d, result, d)
		}
	})
}
