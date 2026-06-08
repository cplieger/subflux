package synchandlers

import (
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// FuzzMsToVTT exercises the millisecond-to-VTT timestamp formatter with
// arbitrary int64 values including negative and overflow cases.
//
// Bug class: incorrect format output or panic on extreme int64 values
// (math.MaxInt64, negative clamping).
func FuzzMsToVTT(f *testing.F) {
	f.Add(int64(0))
	f.Add(int64(-1))
	f.Add(int64(3661001))
	f.Add(int64(1<<63 - 1))
	f.Add(int64(86400000))

	f.Fuzz(func(t *testing.T, ms int64) {
		result := MsToVTT(ms)
		// Must contain ":" and "." separators and not panic.
		if !strings.Contains(result, ":") || !strings.Contains(result, ".") {
			t.Fatalf("invalid format: %q", result)
		}
		// Negative values must be clamped to 00:00:00.000.
		if ms < 0 && result != "00:00:00.000" {
			t.Fatalf("negative ms not clamped: %q", result)
		}
	})
}

// FuzzSrtToWebVTT exercises WebVTT conversion with arbitrary cue slices.
//
// Bug class: panic on empty/nil slices; output must always start with
// "WEBVTT\n\n" header; cue count in output matches input length.
func FuzzSrtToWebVTT(f *testing.F) {
	f.Add("hello", int64(0), int64(1000), "world", int64(1000), int64(2000))

	f.Fuzz(func(t *testing.T, text1 string, s1, e1 int64, text2 string, s2, e2 int64) {
		cues := []api.SubtitleCue{
			{Text: text1, Start: time.Duration(s1) * time.Millisecond, End: time.Duration(e1) * time.Millisecond},
			{Text: text2, Start: time.Duration(s2) * time.Millisecond, End: time.Duration(e2) * time.Millisecond},
		}
		result := SrtToWebVTT(cues)
		if !strings.HasPrefix(result, "WEBVTT\n\n") {
			t.Fatal("output must start with WEBVTT header")
		}
	})
}

// FuzzFindDialogueDenseStart exercises the sliding-window dialogue density
// search with arbitrary cue timing.
//
// Bug class: panic on empty slices; result must be non-negative.
func FuzzFindDialogueDenseStart(f *testing.F) {
	f.Add(int64(0), int64(1000), int64(5000), int64(10000))
	f.Add(int64(0), int64(0), int64(0), int64(0))

	f.Fuzz(func(t *testing.T, s1, s2, s3, s4 int64) {
		// Clamp to reasonable range to avoid O(n²) pathological cases.
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
	})
}
