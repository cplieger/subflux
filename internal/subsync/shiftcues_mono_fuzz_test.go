package subsync

import (
	"testing"
	"time"
)

func FuzzShiftCuesMonotonic(f *testing.F) {
	f.Add(int64(0), int64(1000), int64(2000), int64(3000), int64(500))
	f.Add(int64(100), int64(200), int64(300), int64(400), int64(-50))
	f.Add(int64(0), int64(0), int64(1000), int64(1000), int64(-2000))

	f.Fuzz(func(t *testing.T, s1, e1, s2, e2, offsetMs int64) {
		// Clamp to reasonable range to avoid overflow.
		clamp := func(v int64) time.Duration {
			if v < 0 {
				v = 0
			}
			if v > 86_400_000 {
				v = 86_400_000
			}
			return time.Duration(v) * time.Millisecond
		}
		cues := []Cue{
			{Start: clamp(s1), End: clamp(e1), Text: "a"},
			{Start: clamp(s2), End: clamp(e2), Text: "b"},
		}
		// Clamp offset to +/- 24h.
		if offsetMs < -86_400_000 {
			offsetMs = -86_400_000
		}
		if offsetMs > 86_400_000 {
			offsetMs = 86_400_000
		}
		offset := time.Duration(offsetMs) * time.Millisecond
		shifted := ShiftCues(cues, offset)
		if len(shifted) != 2 {
			t.Fatal("shifted length changed")
		}
		// Monotonicity: if original Start order holds, shifted must preserve it.
		if cues[0].Start <= cues[1].Start && shifted[0].Start > shifted[1].Start {
			t.Errorf("monotonicity violated: cues[0].Start=%v cues[1].Start=%v shifted[0].Start=%v shifted[1].Start=%v",
				cues[0].Start, cues[1].Start, shifted[0].Start, shifted[1].Start)
		}
		// Non-negative invariant.
		for i, c := range shifted {
			if c.Start < 0 || c.End < 0 {
				t.Errorf("shifted[%d] has negative time: Start=%v End=%v", i, c.Start, c.End)
			}
		}
	})
}
