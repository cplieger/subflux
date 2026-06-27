package subsync

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func FuzzParseSRT(f *testing.F) {
	f.Add("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")
	f.Add("")
	f.Add("1\n99:99:99,999 --> 00:00:00,000\nBad\n\n")
	f.Add("not a subtitle file at all")
	f.Fuzz(func(t *testing.T, input string) {
		_, _ = ParseSRT(strings.NewReader(input))
	})
}

// FuzzSRTRoundtrip checks that parsing SRT, writing it back, and re-parsing
// yields identical cues (timing and text). Round-trip is the highest-leverage
// property for a parser/serializer pair.
func FuzzSRTRoundtrip(f *testing.F) {
	f.Add("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n")
	f.Add("1\n00:00:01,000 --> 00:00:02,000\nLine 1\n\n2\n00:00:03,000 --> 00:00:04,000\nLine 2\n\n")
	f.Add("1\n00:01:30,500 --> 00:01:33,200\nMulti\nline\n\n")

	f.Fuzz(func(t *testing.T, input string) {
		cues1, err := ParseSRT(strings.NewReader(input))
		if err != nil || len(cues1) == 0 {
			return
		}
		var buf bytes.Buffer
		if err := WriteSRT(&buf, cues1); err != nil {
			t.Fatalf("WriteSRT failed: %v", err)
		}
		cues2, err := ParseSRT(strings.NewReader(buf.String()))
		if err != nil {
			t.Fatalf("re-parse failed: %v", err)
		}
		if len(cues1) != len(cues2) {
			t.Fatalf("roundtrip length mismatch: %d vs %d", len(cues1), len(cues2))
		}
		for i := range cues1 {
			if cues1[i].Start != cues2[i].Start || cues1[i].End != cues2[i].End || cues1[i].Text != cues2[i].Text {
				t.Errorf("roundtrip mismatch at cue %d: %+v vs %+v", i, cues1[i], cues2[i])
			}
		}
	})
}

// FuzzShiftCuesMonotonic checks that shifting cues by a constant offset
// preserves cue count and start-time ordering, and never produces negative
// timestamps (ShiftCues clamps at zero).
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
