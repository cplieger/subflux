package subsync

import (
	"math"
	"testing"
	"time"
)

func FuzzScaleCues(f *testing.F) {
	f.Add(1.0)
	f.Add(0.5)
	f.Add(2.0)
	f.Add(23.976 / 25.0)
	f.Add(25.0 / 23.976)
	f.Add(0.0)
	f.Add(-1.0)

	f.Fuzz(func(t *testing.T, ratio float64) {
		if math.IsNaN(ratio) || math.IsInf(ratio, 0) {
			return
		}

		cues := []Cue{
			{Start: time.Second, End: 2 * time.Second, Text: "one"},
			{Start: 5 * time.Second, End: 7 * time.Second, Text: "two"},
		}

		result := scaleCues(cues, ratio)

		if ratio <= 0 {
			// Should return unmodified cues.
			if len(result) != len(cues) {
				t.Fatalf("scaleCues(ratio=%f) changed length", ratio)
			}
			for i := range result {
				if result[i].Start != cues[i].Start || result[i].End != cues[i].End {
					t.Fatalf("scaleCues(ratio=%f) modified timing", ratio)
				}
			}
			return
		}

		if len(result) != len(cues) {
			t.Fatalf("scaleCues changed length: got %d, want %d", len(result), len(cues))
		}

		for i, c := range result {
			if c.Text != cues[i].Text {
				t.Fatalf("scaleCues modified text at index %d", i)
			}
			if c.Start < 0 && cues[i].Start > 0 {
				t.Fatalf("scaleCues produced negative start from positive input")
			}
		}
	})
}
