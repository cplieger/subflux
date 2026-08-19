package synchandlers

import (
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

// FuzzShiftAndFilterCues verifies that shifting never produces a negative
// start or a non-positive end, and never returns more cues than it was given.
//
// Bug class: integer underflow in time.Duration arithmetic could produce
// negative timestamps that violate VTT/SRT constraints.
func FuzzShiftAndFilterCues(f *testing.F) {
	f.Add(int64(0), int64(1000), int64(2000), int64(3000), int64(0))
	f.Add(int64(1000), int64(2000), int64(3000), int64(4000), int64(-1500))
	f.Add(int64(0), int64(100), int64(50), int64(200), int64(-99))
	f.Add(int64(5000), int64(6000), int64(7000), int64(8000), int64(-10000))
	f.Add(int64(0), int64(1), int64(0), int64(1), int64(1))

	f.Fuzz(func(t *testing.T, s1, e1, s2, e2, shiftMs int64) {
		// Clamp starts to [0, 1h] and ends to [1, 1h]: real cues always have a
		// positive end, so any non-positive output end must come from the shift
		// logic, not from a degenerate input. This also keeps the zero-shift
		// passthrough path valid.
		clampStart := func(v int64) int64 {
			if v < 0 {
				return 0
			}
			if v > 3_600_000 {
				return 3_600_000
			}
			return v
		}
		clampEnd := func(v int64) int64 {
			if v < 1 {
				return 1
			}
			if v > 3_600_000 {
				return 3_600_000
			}
			return v
		}
		s1, s2 = clampStart(s1), clampStart(s2)
		e1, e2 = clampEnd(e1), clampEnd(e2)
		if shiftMs < -3_600_000 {
			shiftMs = -3_600_000
		}
		if shiftMs > 3_600_000 {
			shiftMs = 3_600_000
		}

		cues := []subflux.SubtitleCue{
			{Start: time.Duration(s1) * time.Millisecond, End: time.Duration(e1) * time.Millisecond, Text: "a"},
			{Start: time.Duration(s2) * time.Millisecond, End: time.Duration(e2) * time.Millisecond, Text: "b"},
		}
		shift := time.Duration(shiftMs) * time.Millisecond
		result := ShiftAndFilterCues(cues, shift)
		if len(result) > len(cues) {
			t.Fatalf("ShiftAndFilterCues returned %d cues, want <= %d", len(result), len(cues))
		}
		for i, c := range result {
			if c.Start < 0 {
				t.Fatalf("result[%d].Start = %v < 0 (shift=%v)", i, c.Start, shift)
			}
			if c.End <= 0 {
				t.Fatalf("result[%d].End = %v <= 0 (shift=%v)", i, c.End, shift)
			}
		}
	})
}
