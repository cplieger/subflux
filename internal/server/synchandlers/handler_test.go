package synchandlers

import (
	"fmt"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
	"pgregory.net/rapid"
)

// --- ShiftAndFilterCues ---

func TestShiftAndFilterCues_zero_shift_returns_original(t *testing.T) {
	t.Parallel()
	cues := []subflux.SubtitleCue{
		{Start: time.Second, End: 2 * time.Second, Text: "Hello"},
	}
	got := ShiftAndFilterCues(cues, 0)
	if len(got) != 1 {
		t.Fatalf("ShiftAndFilterCues(1 cue, 0) returned %d cues, want 1", len(got))
	}
	if got[0].Text != "Hello" {
		t.Errorf("ShiftAndFilterCues(1 cue, 0)[0].Text = %q, want %q", got[0].Text, "Hello")
	}
}

func TestShiftAndFilterCues_positive_shift(t *testing.T) {
	t.Parallel()
	cues := []subflux.SubtitleCue{
		{Start: time.Second, End: 3 * time.Second, Text: "A"},
	}
	got := ShiftAndFilterCues(cues, 500*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("ShiftAndFilterCues(shift +500ms) returned %d cues, want 1", len(got))
	}
	if got[0].Start != 1500*time.Millisecond {
		t.Errorf("ShiftAndFilterCues(shift +500ms)[0].Start = %v, want 1.5s", got[0].Start)
	}
	if got[0].End != 3500*time.Millisecond {
		t.Errorf("ShiftAndFilterCues(shift +500ms)[0].End = %v, want 3.5s", got[0].End)
	}
}

func TestShiftAndFilterCues_negative_shift_filters_ended_cues(t *testing.T) {
	t.Parallel()
	cues := []subflux.SubtitleCue{
		{Start: time.Second, End: 2 * time.Second, Text: "Early"},
		{Start: 5 * time.Second, End: 7 * time.Second, Text: "Late"},
	}
	got := ShiftAndFilterCues(cues, -3*time.Second)
	if len(got) != 1 {
		t.Fatalf("ShiftAndFilterCues(shift -3s) returned %d cues, want 1", len(got))
	}
	if got[0].Text != "Late" {
		t.Errorf("ShiftAndFilterCues(shift -3s)[0].Text = %q, want %q", got[0].Text, "Late")
	}
	if got[0].Start != 2*time.Second {
		t.Errorf("ShiftAndFilterCues(shift -3s)[0].Start = %v, want 2s", got[0].Start)
	}
}

func TestShiftAndFilterCues_start_clamped_to_zero(t *testing.T) {
	t.Parallel()
	cues := []subflux.SubtitleCue{
		{Start: time.Second, End: 5 * time.Second, Text: "Overlap"},
	}
	got := ShiftAndFilterCues(cues, -2*time.Second)
	if len(got) != 1 {
		t.Fatalf("ShiftAndFilterCues(shift -2s) returned %d cues, want 1", len(got))
	}
	if got[0].Start != 0 {
		t.Errorf("ShiftAndFilterCues(shift -2s)[0].Start = %v, want 0", got[0].Start)
	}
	if got[0].End != 3*time.Second {
		t.Errorf("ShiftAndFilterCues(shift -2s)[0].End = %v, want 3s", got[0].End)
	}
}

func TestShiftAndFilterCues_all_filtered(t *testing.T) {
	t.Parallel()
	cues := []subflux.SubtitleCue{
		{Start: time.Second, End: 2 * time.Second, Text: "A"},
		{Start: 3 * time.Second, End: 4 * time.Second, Text: "B"},
	}
	got := ShiftAndFilterCues(cues, -5*time.Second)
	if len(got) != 0 {
		t.Errorf("ShiftAndFilterCues(shift -5s) returned %d cues, want 0", len(got))
	}
}

func TestShiftAndFilterCues_nil_input(t *testing.T) {
	t.Parallel()
	got := ShiftAndFilterCues(nil, time.Second)
	if len(got) != 0 {
		t.Errorf("ShiftAndFilterCues(nil, 1s) returned %d cues, want 0", len(got))
	}
}

func TestShiftAndFilterCues_boundary_end_exactly_zero(t *testing.T) {
	t.Parallel()
	cues := []subflux.SubtitleCue{
		{Start: time.Second, End: 2 * time.Second, Text: "Exact"},
	}

	got := ShiftAndFilterCues(cues, -2*time.Second)
	if len(got) != 0 {
		t.Errorf("ShiftAndFilterCues(End=2s, shift=-2s) returned %d cues, want 0 (newEnd=0 filtered)", len(got))
	}

	got = ShiftAndFilterCues(cues, -1999*time.Millisecond)
	if len(got) != 1 {
		t.Fatalf("ShiftAndFilterCues(End=2s, shift=-1999ms) returned %d cues, want 1 (newEnd=1ms kept)", len(got))
	}
	if got[0].End != time.Millisecond {
		t.Errorf("ShiftAndFilterCues(End=2s, shift=-1999ms)[0].End = %v, want 1ms", got[0].End)
	}
}

func TestShiftAndFilterCues_empty_input(t *testing.T) {
	t.Parallel()
	got := ShiftAndFilterCues([]subflux.SubtitleCue{}, time.Second)
	if len(got) != 0 {
		t.Errorf("ShiftAndFilterCues(empty, 1s) returned %d cues, want 0", len(got))
	}
}

func TestShiftAndFilterCues_property_output_times_non_negative(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		cues := make([]subflux.SubtitleCue, n)
		var cursor int64
		for i := range n {
			gap := rapid.Int64Range(0, 30_000).Draw(t, fmt.Sprintf("gap_%d", i))
			cursor += gap
			durMs := rapid.Int64Range(1, 60_000).Draw(t, fmt.Sprintf("dur_%d", i))
			cues[i] = subflux.SubtitleCue{
				Start: time.Duration(cursor) * time.Millisecond,
				End:   time.Duration(cursor+durMs) * time.Millisecond,
				Text:  fmt.Sprintf("cue %d", i),
			}
			cursor += durMs
		}
		shiftMs := rapid.Int64Range(-300_000, 300_000).Draw(t, "shift")
		shift := time.Duration(shiftMs) * time.Millisecond

		result := ShiftAndFilterCues(cues, shift)

		if len(result) > len(cues) {
			t.Errorf("ShiftAndFilterCues(%d cues, %v) returned %d cues, want <= %d",
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
