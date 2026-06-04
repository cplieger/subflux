package subsync

import (
	"math"
	"testing"
	"time"
)

// FuzzShiftCuesIdentity verifies that ShiftCues with a zero offset
// returns cues with unchanged timing (identity invariant).
func FuzzShiftCuesIdentity(f *testing.F) {
	f.Add(int64(1000), int64(2000), "hello")
	f.Add(int64(0), int64(500), "test")
	f.Fuzz(func(t *testing.T, startMs, endMs int64, text string) {
		if startMs < 0 || endMs < 0 || startMs > 1<<40 || endMs > 1<<40 {
			return
		}
		cues := []Cue{{
			Start: time.Duration(startMs) * time.Millisecond,
			End:   time.Duration(endMs) * time.Millisecond,
			Text:  text,
		}}
		shifted := ShiftCues(cues, 0)
		if len(shifted) != 1 {
			t.Fatal("length mismatch")
		}
		if shifted[0].Start != cues[0].Start || shifted[0].End != cues[0].End || shifted[0].Text != cues[0].Text {
			t.Fatal("zero shift changed cue")
		}
	})
}

// FuzzScaleCuesInverse verifies that scaling by r then by 1/r
// recovers the original timings within floating-point tolerance (inverse property).
func FuzzScaleCuesInverse(f *testing.F) {
	f.Add(int64(5000), int64(10000), "sub", uint32(120))
	f.Add(int64(0), int64(1000), "x", uint32(100))
	f.Fuzz(func(t *testing.T, startMs, endMs int64, text string, ratioRaw uint32) {
		if startMs < 0 || endMs < 0 || startMs > 1<<40 || endMs > 1<<40 {
			return
		}
		// ratio in range (0.5, 2.0) to avoid degenerate values
		ratio := 0.5 + float64(ratioRaw%1500)/1000.0
		cues := []Cue{{
			Start: time.Duration(startMs) * time.Millisecond,
			End:   time.Duration(endMs) * time.Millisecond,
			Text:  text,
		}}
		scaled := scaleCues(cues, ratio)
		recovered := scaleCues(scaled, 1.0/ratio)
		if len(recovered) != 1 {
			t.Fatal("length mismatch")
		}
		origStart := float64(cues[0].Start)
		origEnd := float64(cues[0].End)
		tolerance := 2.0 // 2 nanoseconds tolerance for float rounding
		if math.Abs(float64(recovered[0].Start)-origStart) > tolerance {
			t.Fatalf("start mismatch: got %v want %v", recovered[0].Start, cues[0].Start)
		}
		if math.Abs(float64(recovered[0].End)-origEnd) > tolerance {
			t.Fatalf("end mismatch: got %v want %v", recovered[0].End, cues[0].End)
		}
	})
}
