package previewhandlers

import (
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

// The three targets below moved here from internal/server/synchandlers, which
// carried exported duplicates of msToVTT/srtToWebVTT/findDialogueDenseStart
// until the preview handlers stopped delegating to them. The live copies are
// the unexported ones in this package, so the fuzz coverage belongs here.
// Target names are unchanged so no committed corpus is orphaned.

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
		got := msToVTT(ms)
		n := len(got)
		if n < 12 {
			t.Fatalf("msToVTT(%d) = %q, length %d, want >= 12", ms, got, n)
		}
		if got[n-4] != '.' || got[n-7] != ':' || got[n-10] != ':' {
			t.Fatalf("msToVTT(%d) = %q, separators not at expected positions", ms, got)
		}
		if ms < 0 && got != "00:00:00.000" {
			t.Fatalf("msToVTT(%d) = %q, negative input must clamp to 00:00:00.000", ms, got)
		}
	})
}

// FuzzSrtToWebVTT exercises WebVTT conversion with arbitrary cue text and
// timing. Output must always carry the WEBVTT header and emit each cue's
// timing line as msToVTT(start) --> msToVTT(end), regardless of text content.
func FuzzSrtToWebVTT(f *testing.F) {
	f.Add("hello", int64(0), int64(1000), "world", int64(1000), int64(2000))
	f.Add("arrow --> in text", int64(0), int64(500), "", int64(500), int64(900))

	f.Fuzz(func(t *testing.T, text1 string, s1, e1 int64, text2 string, s2, e2 int64) {
		cues := []subflux.SubtitleCue{
			{Text: text1, Start: time.Duration(s1) * time.Millisecond, End: time.Duration(e1) * time.Millisecond},
			{Text: text2, Start: time.Duration(s2) * time.Millisecond, End: time.Duration(e2) * time.Millisecond},
		}
		result := srtToWebVTT(cues)
		if !strings.HasPrefix(result, "WEBVTT\n\n") {
			t.Fatalf("output must start with WEBVTT header, got %q", result)
		}
		for i, c := range cues {
			timing := msToVTT(c.Start.Milliseconds()) + " --> " + msToVTT(c.End.Milliseconds())
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
		cues := []subflux.SubtitleCue{
			{Text: "a", Start: time.Duration(s1) * time.Millisecond, End: time.Duration(s1+500) * time.Millisecond},
			{Text: "bb", Start: time.Duration(s2) * time.Millisecond, End: time.Duration(s2+500) * time.Millisecond},
			{Text: "ccc", Start: time.Duration(s3) * time.Millisecond, End: time.Duration(s3+500) * time.Millisecond},
			{Text: "dddd", Start: time.Duration(s4) * time.Millisecond, End: time.Duration(s4+500) * time.Millisecond},
		}
		result := findDialogueDenseStart(cues)
		if result < 0 {
			t.Fatalf("result must be non-negative, got %d", result)
		}
		maxStart := max(max(s1, s2), max(s3, s4))
		if result > maxStart {
			t.Fatalf("result %d exceeds latest cue start %d", result, maxStart)
		}
	})
}
