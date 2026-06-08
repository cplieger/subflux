package server

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

// --- msToVTT ---

func TestMsToVTT(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want string
		ms   int64
	}{
		{name: "zero", ms: 0, want: "00:00:00.000"},
		{name: "one millisecond", ms: 1, want: "00:00:00.001"},
		{name: "one second", ms: 1000, want: "00:00:01.000"},
		{name: "one minute", ms: 60_000, want: "00:01:00.000"},
		{name: "one hour", ms: 3_600_000, want: "01:00:00.000"},
		{name: "mixed", ms: 3_723_456, want: "01:02:03.456"},
		{name: "negative clamped to zero", ms: -500, want: "00:00:00.000"},
		{name: "large value", ms: 86_399_999, want: "23:59:59.999"},
		{name: "999 ms", ms: 999, want: "00:00:00.999"},
		{name: "exactly 10 hours", ms: 36_000_000, want: "10:00:00.000"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := msToVTT(tt.ms)
			if got != tt.want {
				t.Errorf("msToVTT(%d) = %q, want %q", tt.ms, got, tt.want)
			}
		})
	}
}

// --- srtToWebVTT ---

func TestSrtToWebVTT(t *testing.T) {
	t.Parallel()

	t.Run("empty cues", func(t *testing.T) {
		t.Parallel()
		got := srtToWebVTT(nil)
		if got != "WEBVTT\n\n" {
			t.Errorf("srtToWebVTT(nil) = %q, want %q", got, "WEBVTT\n\n")
		}
	})

	t.Run("single cue", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 1500 * time.Millisecond, End: 4200 * time.Millisecond, Text: "Hello world"},
		}
		got := srtToWebVTT(cues)
		want := "WEBVTT\n\n1\n00:00:01.500 --> 00:00:04.200\nHello world\n\n"
		if got != want {
			t.Errorf("srtToWebVTT(single) = %q, want %q", got, want)
		}
	})

	t.Run("multiple cues numbered sequentially", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 0, End: 2 * time.Second, Text: "First"},
			{Start: 3 * time.Second, End: 5 * time.Second, Text: "Second"},
		}
		got := srtToWebVTT(cues)
		if !strings.Contains(got, "1\n00:00:00.000 --> 00:00:02.000\nFirst") {
			t.Errorf("srtToWebVTT() missing first cue in %q", got)
		}
		if !strings.Contains(got, "2\n00:00:03.000 --> 00:00:05.000\nSecond") {
			t.Errorf("srtToWebVTT() missing second cue in %q", got)
		}
	})

	t.Run("starts with WEBVTT header", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 0, End: time.Second, Text: "Test"},
		}
		got := srtToWebVTT(cues)
		if !strings.HasPrefix(got, "WEBVTT\n\n") {
			t.Errorf("srtToWebVTT() should start with WEBVTT header, got %q", got[:20])
		}
	})
}

// --- findDialogueDenseStart ---

func TestFindDialogueDenseStart(t *testing.T) {
	t.Parallel()

	t.Run("empty cues returns zero", func(t *testing.T) {
		t.Parallel()
		got := findDialogueDenseStart(nil)
		if got != 0 {
			t.Errorf("findDialogueDenseStart(nil) = %d, want 0", got)
		}
	})

	t.Run("single cue returns start minus lead-in", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 30 * time.Second, End: 32 * time.Second, Text: "Hello world"},
		}
		got := findDialogueDenseStart(cues)
		// Start is 30s, lead-in is 10s, so result should be 20s = 20000ms.
		if got != 20_000 {
			t.Errorf("findDialogueDenseStart(single@30s) = %d, want 20000", got)
		}
	})

	t.Run("lead-in clamped to zero", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 5 * time.Second, End: 7 * time.Second, Text: "Early dialogue"},
		}
		got := findDialogueDenseStart(cues)
		// Start is 5s, lead-in is 10s, clamped to 0.
		if got != 0 {
			t.Errorf("findDialogueDenseStart(early cue) = %d, want 0", got)
		}
	})

	t.Run("picks densest window", func(t *testing.T) {
		t.Parallel()
		// Sparse section at 0-60s (1 short cue), dense section at 120-180s (many long cues).
		cues := []api.SubtitleCue{
			{Start: 10 * time.Second, End: 12 * time.Second, Text: "Hi"},
			{Start: 120 * time.Second, End: 125 * time.Second, Text: "This is a much longer dialogue line with many characters"},
			{Start: 130 * time.Second, End: 135 * time.Second, Text: "Another long line of dialogue that adds character count"},
			{Start: 140 * time.Second, End: 145 * time.Second, Text: "And yet another substantial piece of dialogue text here"},
			{Start: 150 * time.Second, End: 155 * time.Second, Text: "Final long dialogue line in the dense window section here"},
		}
		got := findDialogueDenseStart(cues)
		// Dense window starts at 120s, lead-in 10s = 110s = 110000ms.
		if got != 110_000 {
			t.Errorf("findDialogueDenseStart(dense@120s) = %d, want 110000", got)
		}
	})

	t.Run("whitespace-only cues ignored in density", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 60 * time.Second, End: 62 * time.Second, Text: "   \t  \n  "},
			{Start: 120 * time.Second, End: 122 * time.Second, Text: "Real dialogue"},
		}
		got := findDialogueDenseStart(cues)
		// Whitespace cue at 60s has 0 trimmed chars.
		// Real cue at 120s has 13 chars, so it wins. 120s - 10s lead-in = 110s.
		if got != 110_000 {
			t.Errorf("findDialogueDenseStart(whitespace+real) = %d, want 110000", got)
		}
	})
}

// --- shiftAndFilterCues ---

func TestShiftAndFilterCues_zero_shift_returns_original(t *testing.T) {
	t.Parallel()
	cues := []api.SubtitleCue{
		{Start: time.Second, End: 2 * time.Second, Text: "Hello"},
	}
	got := shiftAndFilterCues(cues, 0)
	if len(got) != 1 {
		t.Fatalf("shiftAndFilterCues(1 cue, 0) returned %d cues, want 1", len(got))
	}
	if got[0].Text != "Hello" {
		t.Errorf("shiftAndFilterCues(1 cue, 0)[0].Text = %q, want %q", got[0].Text, "Hello")
	}
}

func TestShiftAndFilterCues_positive_shift(t *testing.T) {
	t.Parallel()
	cues := []api.SubtitleCue{
		{Start: time.Second, End: 3 * time.Second, Text: "A"},
	}
	got := shiftAndFilterCues(cues, 500*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("shiftAndFilterCues(shift +500ms) returned %d cues, want 1", len(got))
	}
	if got[0].Start != 1500*time.Millisecond {
		t.Errorf("shiftAndFilterCues(shift +500ms)[0].Start = %v, want 1.5s", got[0].Start)
	}
	if got[0].End != 3500*time.Millisecond {
		t.Errorf("shiftAndFilterCues(shift +500ms)[0].End = %v, want 3.5s", got[0].End)
	}
}

func TestShiftAndFilterCues_negative_shift_filters_ended_cues(t *testing.T) {
	t.Parallel()
	cues := []api.SubtitleCue{
		{Start: time.Second, End: 2 * time.Second, Text: "Early"},
		{Start: 5 * time.Second, End: 7 * time.Second, Text: "Late"},
	}
	got := shiftAndFilterCues(cues, -3*time.Second)
	if len(got) != 1 {
		t.Fatalf("shiftAndFilterCues(shift -3s) returned %d cues, want 1", len(got))
	}
	if got[0].Text != "Late" {
		t.Errorf("shiftAndFilterCues(shift -3s)[0].Text = %q, want %q", got[0].Text, "Late")
	}
	if got[0].Start != 2*time.Second {
		t.Errorf("shiftAndFilterCues(shift -3s)[0].Start = %v, want 2s", got[0].Start)
	}
}

func TestShiftAndFilterCues_start_clamped_to_zero(t *testing.T) {
	t.Parallel()
	cues := []api.SubtitleCue{
		{Start: time.Second, End: 5 * time.Second, Text: "Overlap"},
	}
	got := shiftAndFilterCues(cues, -2*time.Second)
	if len(got) != 1 {
		t.Fatalf("shiftAndFilterCues(shift -2s) returned %d cues, want 1", len(got))
	}
	if got[0].Start != 0 {
		t.Errorf("shiftAndFilterCues(shift -2s)[0].Start = %v, want 0", got[0].Start)
	}
	if got[0].End != 3*time.Second {
		t.Errorf("shiftAndFilterCues(shift -2s)[0].End = %v, want 3s", got[0].End)
	}
}

func TestShiftAndFilterCues_all_filtered(t *testing.T) {
	t.Parallel()
	cues := []api.SubtitleCue{
		{Start: time.Second, End: 2 * time.Second, Text: "A"},
		{Start: 3 * time.Second, End: 4 * time.Second, Text: "B"},
	}
	got := shiftAndFilterCues(cues, -5*time.Second)
	if len(got) != 0 {
		t.Errorf("shiftAndFilterCues(shift -5s) returned %d cues, want 0", len(got))
	}
}

func TestShiftAndFilterCues_nil_input(t *testing.T) {
	t.Parallel()
	got := shiftAndFilterCues(nil, time.Second)
	if len(got) != 0 {
		t.Errorf("shiftAndFilterCues(nil, 1s) returned %d cues, want 0", len(got))
	}
}

func TestShiftAndFilterCues_boundary_end_exactly_zero(t *testing.T) {
	t.Parallel()
	cues := []api.SubtitleCue{
		{Start: time.Second, End: 2 * time.Second, Text: "Exact"},
	}

	// Shift -2s: newEnd = 2s - 2s = 0, which is <= 0, so filtered.
	got := shiftAndFilterCues(cues, -2*time.Second)
	if len(got) != 0 {
		t.Errorf("shiftAndFilterCues(End=2s, shift=-2s) returned %d cues, want 0 (newEnd=0 filtered)", len(got))
	}

	// Shift -1999ms: newEnd = 2s - 1999ms = 1ms > 0, so kept.
	got = shiftAndFilterCues(cues, -1999*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("shiftAndFilterCues(End=2s, shift=-1999ms) returned %d cues, want 1 (newEnd=1ms kept)", len(got))
	}
	if got[0].End != time.Millisecond {
		t.Errorf("shiftAndFilterCues(End=2s, shift=-1999ms)[0].End = %v, want 1ms", got[0].End)
	}
}

func TestShiftAndFilterCues_empty_input(t *testing.T) {
	t.Parallel()
	got := shiftAndFilterCues([]api.SubtitleCue{}, time.Second)
	if len(got) != 0 {
		t.Errorf("shiftAndFilterCues(empty, 1s) returned %d cues, want 0", len(got))
	}
}

func TestShiftAndFilterCues_property_output_times_non_negative(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		cues := make([]api.SubtitleCue, n)
		var cursor int64
		for i := range n {
			gap := rapid.Int64Range(0, 30_000).Draw(t, fmt.Sprintf("gap_%d", i))
			cursor += gap
			durMs := rapid.Int64Range(1, 60_000).Draw(t, fmt.Sprintf("dur_%d", i))
			cues[i] = api.SubtitleCue{
				Start: time.Duration(cursor) * time.Millisecond,
				End:   time.Duration(cursor+durMs) * time.Millisecond,
				Text:  fmt.Sprintf("cue %d", i),
			}
			cursor += durMs
		}
		shiftMs := rapid.Int64Range(-300_000, 300_000).Draw(t, "shift")
		shift := time.Duration(shiftMs) * time.Millisecond

		result := shiftAndFilterCues(cues, shift)

		if len(result) > len(cues) {
			t.Errorf("shiftAndFilterCues(%d cues, %v) returned %d cues, want <= %d",
				len(cues), shift, len(result), len(cues))
		}

		for i, c := range result {
			if c.Start < 0 {
				t.Errorf("result[%d].Start = %v, want >= 0", i, c.Start)
			}
			if c.End <= 0 {
				t.Errorf("result[%d].End = %v, want > 0", i, c.End)
			}
			if c.Text == "" {
				t.Errorf("result[%d].Text is empty", i)
			}
			if i > 0 && result[i].Start < result[i-1].Start {
				t.Errorf("result[%d].Start = %v < result[%d].Start = %v, ordering violated",
					i, result[i].Start, i-1, result[i-1].Start)
			}
		}
	})
}

// --- PBTs for existing helpers ---

func TestMsToVTT_property_format_always_valid(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		ms := rapid.Int64Range(-10_000, 360_000_000).Draw(t, "ms")
		got := msToVTT(ms)
		// Format: HH:MM:SS.mmm (or HHH:... for ≥100h).
		// Verify separators relative to the end: .mmm is last 4 chars,
		// :SS is 3 chars before that, :MM is 3 chars before that.
		n := len(got)
		if n < 12 {
			t.Errorf("msToVTT(%d) = %q, length %d, want >= 12", ms, got, n)
		}
		if got[n-4] != '.' || got[n-7] != ':' || got[n-10] != ':' {
			t.Errorf("msToVTT(%d) = %q, wrong separator positions", ms, got)
		}
		if ms < 0 && got != "00:00:00.000" {
			t.Errorf("msToVTT(%d) = %q, want 00:00:00.000 for negative input", ms, got)
		}
	})
}

func TestSrtToWebVTT_property_cue_count_matches_input(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 30).Draw(t, "n")
		cues := make([]api.SubtitleCue, n)
		for i := range n {
			startMs := rapid.Int64Range(0, 600_000).Draw(t, fmt.Sprintf("start_%d", i))
			durMs := rapid.Int64Range(1, 10_000).Draw(t, fmt.Sprintf("dur_%d", i))
			cues[i] = api.SubtitleCue{
				Start: time.Duration(startMs) * time.Millisecond,
				End:   time.Duration(startMs+durMs) * time.Millisecond,
				Text:  "cue text",
			}
		}
		got := srtToWebVTT(cues)
		if !strings.HasPrefix(got, "WEBVTT\n\n") {
			t.Errorf("srtToWebVTT(%d cues) missing WEBVTT header", n)
		}
		arrowCount := strings.Count(got, " --> ")
		if arrowCount != n {
			t.Errorf("srtToWebVTT(%d cues) has %d arrow separators, want %d", n, arrowCount, n)
		}
	})
}

func TestFindDialogueDenseStart_property_result_non_negative(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 50).Draw(t, "n")
		cues := make([]api.SubtitleCue, n)
		for i := range n {
			startMs := rapid.Int64Range(0, 7_200_000).Draw(t, fmt.Sprintf("start_%d", i))
			durMs := rapid.Int64Range(100, 10_000).Draw(t, fmt.Sprintf("dur_%d", i))
			textLen := rapid.IntRange(1, 200).Draw(t, fmt.Sprintf("textLen_%d", i))
			cues[i] = api.SubtitleCue{
				Start: time.Duration(startMs) * time.Millisecond,
				End:   time.Duration(startMs+durMs) * time.Millisecond,
				Text:  strings.Repeat("x", textLen),
			}
		}
		got := findDialogueDenseStart(cues)
		if got < 0 {
			t.Errorf("findDialogueDenseStart(%d cues) = %d, want >= 0", n, got)
		}
	})
}
