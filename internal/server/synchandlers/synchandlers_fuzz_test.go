package synchandlers

import (
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzMsToVTT exercises the millisecond-to-VTT formatter across the full int64
// range (negative, MaxInt64) and asserts the output is always a well-formed
// HH:MM:SS.mmm timestamp with separators in fixed positions from the end, and
// that negatives clamp to zero.
func FuzzMsToVTT(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(-1))
	f.Add(int64(3661001))
	f.Add(int64(1<<63 - 1))
	f.Add(int64(86400000))

	f.Fuzz(func(t *testing.T, ms int64) {
		got := MsToVTT(ms)
		n := len(got)
		if n < 12 {
			t.Fatalf("MsToVTT(%d) = %q, length %d, want >= 12", ms, got, n)
		}
		if got[n-4] != '.' || got[n-7] != ':' || got[n-10] != ':' {
			t.Fatalf("MsToVTT(%d) = %q, separators not at expected positions", ms, got)
		}
		if ms < 0 && got != "00:00:00.000" {
			t.Fatalf("MsToVTT(%d) = %q, negative input must clamp to 00:00:00.000", ms, got)
		}
	})
}

// FuzzSrtToWebVTT exercises WebVTT conversion with arbitrary cue text and
// timing. Output must always carry the WEBVTT header and emit each cue's
// timing line as MsToVTT(start) --> MsToVTT(end), regardless of text content.
func FuzzSrtToWebVTT(f *testing.F) {
	f.Add("hello", int64(0), int64(1000), "world", int64(1000), int64(2000))
	f.Add("arrow --> in text", int64(0), int64(500), "", int64(500), int64(900))

	f.Fuzz(func(t *testing.T, text1 string, s1, e1 int64, text2 string, s2, e2 int64) {
		cues := []api.SubtitleCue{
			{Text: text1, Start: time.Duration(s1) * time.Millisecond, End: time.Duration(e1) * time.Millisecond},
			{Text: text2, Start: time.Duration(s2) * time.Millisecond, End: time.Duration(e2) * time.Millisecond},
		}
		result := SrtToWebVTT(cues)
		if !strings.HasPrefix(result, "WEBVTT\n\n") {
			t.Fatalf("output must start with WEBVTT header, got %q", result)
		}
		for i, c := range cues {
			timing := MsToVTT(c.Start.Milliseconds()) + " --> " + MsToVTT(c.End.Milliseconds())
			if !strings.Contains(result, timing) {
				t.Fatalf("cue %d timing line %q missing from output %q", i, timing, result)
			}
		}
	})
}

// FuzzFindDialogueDenseStart exercises the sliding-window dialogue density
// search with arbitrary cue timing. The chosen start is always non-negative
// and never later than the latest cue's start (it is an anchor minus a
// non-negative lead-in, clamped at zero).
func FuzzFindDialogueDenseStart(f *testing.F) {
	f.Add(int64(0), int64(1000), int64(5000), int64(10000))
	f.Add(int64(0), int64(0), int64(0), int64(0))

	f.Fuzz(func(t *testing.T, s1, s2, s3, s4 int64) {
		// Clamp to a reasonable range to avoid pathological O(n^2) cost.
		clamp := func(v int64) int64 {
			if v < 0 {
				return 0
			}
			if v > 600_000 {
				return 600_000
			}
			return v
		}
		s1, s2, s3, s4 = clamp(s1), clamp(s2), clamp(s3), clamp(s4)
		cues := []api.SubtitleCue{
			{Text: "a", Start: time.Duration(s1) * time.Millisecond, End: time.Duration(s1+500) * time.Millisecond},
			{Text: "bb", Start: time.Duration(s2) * time.Millisecond, End: time.Duration(s2+500) * time.Millisecond},
			{Text: "ccc", Start: time.Duration(s3) * time.Millisecond, End: time.Duration(s3+500) * time.Millisecond},
			{Text: "dddd", Start: time.Duration(s4) * time.Millisecond, End: time.Duration(s4+500) * time.Millisecond},
		}
		result := FindDialogueDenseStart(cues)
		if result < 0 {
			t.Fatalf("result must be non-negative, got %d", result)
		}
		maxStart := max(max(s1, s2), max(s3, s4))
		if result > maxStart {
			t.Fatalf("result %d exceeds latest cue start %d", result, maxStart)
		}
	})
}

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

		cues := []api.SubtitleCue{
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
