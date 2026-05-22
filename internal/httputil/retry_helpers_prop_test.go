package httputil

import (
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestJitteredBackoff_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		d := time.Duration(rapid.Int64Range(1, math.MaxInt64/2).Draw(t, "duration"))
		result := JitteredBackoff(d)
		half := d / 2
		if result < half {
			t.Fatalf("JitteredBackoff(%v) = %v, want >= %v", d, result, half)
		}
		// Implementation: half + rand.Int64N(half+1), so max is half + half = d
		// (for even d). The upper bound is inclusive of d.
		if result > d {
			t.Fatalf("JitteredBackoff(%v) = %v, want <= %v", d, result, d)
		}
	})
}

func TestSafeDouble_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		d := time.Duration(rapid.Int64Range(1, math.MaxInt64).Draw(t, "duration"))
		result := SafeDouble(d)

		// Result must be >= d (never shrinks).
		if result < d {
			t.Fatalf("SafeDouble(%v) = %v, want >= %v", d, result, d)
		}

		// Result is either exactly 2*d or MaxInt64 (overflow case).
		doubled := d * 2
		if doubled >= d {
			// No overflow.
			if result != doubled {
				t.Fatalf("SafeDouble(%v) = %v, want %v (no overflow)", d, result, doubled)
			}
		} else {
			// Overflow: expect MaxInt64.
			if result != time.Duration(math.MaxInt64) {
				t.Fatalf("SafeDouble(%v) = %v, want MaxInt64 on overflow", d, result)
			}
		}
	})
}
