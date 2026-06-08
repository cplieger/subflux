package synchandlers

import (
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzShiftAndFilterCuesNonNegative verifies that all output cue times are
// non-negative after shifting. The invariant is: for every output cue,
// Start >= 0 and End > 0.
//
// Bug class: integer underflow in time.Duration arithmetic could produce
// negative timestamps that violate VTT/SRT format constraints, causing
// subtitle renderers to display cues at wrong times or crash.
func FuzzShiftAndFilterCuesNonNegative(f *testing.F) {
	f.Add(int64(0), int64(1000), int64(2000), int64(3000), int64(0))
	f.Add(int64(1000), int64(2000), int64(3000), int64(4000), int64(-1500))
	f.Add(int64(0), int64(100), int64(50), int64(200), int64(-99))
	f.Add(int64(5000), int64(6000), int64(7000), int64(8000), int64(-10000))
	f.Add(int64(0), int64(1), int64(0), int64(1), int64(1))

	f.Fuzz(func(t *testing.T, s1, e1, s2, e2, shiftMs int64) {
		// Clamp to reasonable range to avoid time.Duration overflow
		clamp := func(v int64) int64 {
			if v < 0 {
				return 0
			}
			if v > 3600000 {
				return 3600000
			}
			return v
		}
		s1, e1, s2, e2 = clamp(s1), clamp(e1), clamp(s2), clamp(e2)
		if shiftMs < -3600000 {
			shiftMs = -3600000
		}
		if shiftMs > 3600000 {
			shiftMs = 3600000
		}
		// Ensure shift != 0 so the function actually processes cues
		if shiftMs == 0 {
			shiftMs = 1
		}

		cues := []api.SubtitleCue{
			{Start: time.Duration(s1) * time.Millisecond, End: time.Duration(e1) * time.Millisecond, Text: "a"},
			{Start: time.Duration(s2) * time.Millisecond, End: time.Duration(e2) * time.Millisecond, Text: "b"},
		}
		shift := time.Duration(shiftMs) * time.Millisecond
		result := ShiftAndFilterCues(cues, shift)
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
