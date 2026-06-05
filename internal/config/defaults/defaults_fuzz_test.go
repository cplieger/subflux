package defaults

import (
	"testing"
	"time"
)

// FuzzFormatDuration exercises FormatDuration with arbitrary durations
// checking that it never panics and always produces non-empty output for
// non-negative inputs.
func FuzzFormatDuration(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(time.Second))
	f.Add(int64(5 * time.Minute))
	f.Add(int64(time.Hour))
	f.Add(int64(24 * time.Hour))
	f.Add(int64(730 * time.Hour))
	f.Add(int64(time.Millisecond)) // sub-second
	f.Add(int64(-time.Second))     // negative

	f.Fuzz(func(t *testing.T, ns int64) {
		d := time.Duration(ns)
		if d < 0 {
			return // negative durations are outside the domain
		}
		result := FormatDuration(d)

		// Invariant 1: never panics (implicit).

		// Invariant 2: result is never empty for non-negative duration.
		if result == "" {
			t.Fatalf("FormatDuration(%v) returned empty string", d)
		}

		// Invariant 3: result always ends with a unit suffix.
		last := result[len(result)-1]
		validSuffix := last == 's' || last == 'm' || last == 'h' || last == 'D' || last == 'M'
		if !validSuffix {
			t.Fatalf("FormatDuration(%v) = %q has no valid unit suffix", d, result)
		}
	})
}
