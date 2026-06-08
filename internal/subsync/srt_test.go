package subsync

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestParseSRT_valid_cues(t *testing.T) {
	t.Parallel()
	input := "1\n00:00:01,000 --> 00:00:04,000\nHello world\n\n2\n00:00:05,500 --> 00:00:08,200\nSecond line\nWith continuation\n\n"

	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("ParseSRT() returned %d cues, want 2", len(cues))
	}

	if cues[0].Start != 1*time.Second {
		t.Errorf("cues[0].Start = %v, want 1s", cues[0].Start)
	}
	if cues[0].End != 4*time.Second {
		t.Errorf("cues[0].End = %v, want 4s", cues[0].End)
	}
	if cues[0].Text != "Hello world" {
		t.Errorf("cues[0].Text = %q, want %q", cues[0].Text, "Hello world")
	}

	if cues[1].Start != 5*time.Second+500*time.Millisecond {
		t.Errorf("cues[1].Start = %v, want 5.5s", cues[1].Start)
	}
	if cues[1].End != 8*time.Second+200*time.Millisecond {
		t.Errorf("cues[1].End = %v, want 8.2s", cues[1].End)
	}
	if cues[1].Text != "Second line\nWith continuation" {
		t.Errorf("cues[1].Text = %q, want %q", cues[1].Text, "Second line\nWith continuation")
	}
}

func TestParseSRT_empty_input(t *testing.T) {
	t.Parallel()
	cues, err := ParseSRT(strings.NewReader(""))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 0 {
		t.Errorf("ParseSRT(\"\") returned %d cues, want 0", len(cues))
	}
}

func TestParseSRT_dot_separator(t *testing.T) {
	t.Parallel()
	input := "1\n00:00:01.000 --> 00:00:02.500\nDot format\n\n"

	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT() returned %d cues, want 1", len(cues))
	}
	if cues[0].Start != 1*time.Second {
		t.Errorf("cues[0].Start = %v, want 1s", cues[0].Start)
	}
	if cues[0].End != 2*time.Second+500*time.Millisecond {
		t.Errorf("cues[0].End = %v, want 2.5s", cues[0].End)
	}
}

func TestParseSRT_no_timing_lines(t *testing.T) {
	t.Parallel()
	input := "This is just text\nwith no timing\n"

	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 0 {
		t.Errorf("ParseSRT(no timing) returned %d cues, want 0", len(cues))
	}
}

func TestWriteSRT_round_trip(t *testing.T) {
	t.Parallel()
	original := []Cue{
		{Start: 1 * time.Second, End: 4 * time.Second, Text: "Hello"},
		{Start: 5*time.Second + 500*time.Millisecond, End: 8*time.Second + 200*time.Millisecond, Text: "World"},
	}

	var buf bytes.Buffer
	if err := WriteSRT(&buf, original); err != nil {
		t.Fatalf("WriteSRT() unexpected error: %v", err)
	}

	parsed, err := ParseSRT(&buf)
	if err != nil {
		t.Fatalf("ParseSRT() round-trip error: %v", err)
	}
	if len(parsed) != len(original) {
		t.Fatalf("round-trip: got %d cues, want %d", len(parsed), len(original))
	}
	for i := range original {
		if parsed[i].Start != original[i].Start {
			t.Errorf("cue[%d].Start = %v, want %v", i, parsed[i].Start, original[i].Start)
		}
		if parsed[i].End != original[i].End {
			t.Errorf("cue[%d].End = %v, want %v", i, parsed[i].End, original[i].End)
		}
		if parsed[i].Text != original[i].Text {
			t.Errorf("cue[%d].Text = %q, want %q", i, parsed[i].Text, original[i].Text)
		}
	}
}

func TestWriteSRT_empty_cues(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := WriteSRT(&buf, nil); err != nil {
		t.Fatalf("WriteSRT(nil) unexpected error: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("WriteSRT(nil) wrote %d bytes, want 0", buf.Len())
	}
}

func TestWriteSRT_format(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{
			Start: 1*time.Hour + 2*time.Minute + 3*time.Second + 456*time.Millisecond,
			End:   1*time.Hour + 2*time.Minute + 6*time.Second + 789*time.Millisecond,
			Text:  "Test",
		},
	}

	var buf bytes.Buffer
	if err := WriteSRT(&buf, cues); err != nil {
		t.Fatalf("WriteSRT() unexpected error: %v", err)
	}

	want := "1\n01:02:03,456 --> 01:02:06,789\nTest\n\n"
	if buf.String() != want {
		t.Errorf("WriteSRT() output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestShiftCues_positive_offset(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 1 * time.Second, End: 3 * time.Second, Text: "A"},
		{Start: 5 * time.Second, End: 7 * time.Second, Text: "B"},
	}

	shifted := ShiftCues(cues, 2*time.Second)

	if shifted[0].Start != 3*time.Second {
		t.Errorf("shifted[0].Start = %v, want 3s", shifted[0].Start)
	}
	if shifted[0].End != 5*time.Second {
		t.Errorf("shifted[0].End = %v, want 5s", shifted[0].End)
	}
	if shifted[1].Start != 7*time.Second {
		t.Errorf("shifted[1].Start = %v, want 7s", shifted[1].Start)
	}
	if shifted[1].End != 9*time.Second {
		t.Errorf("shifted[1].End = %v, want 9s", shifted[1].End)
	}
}

func TestShiftCues_negative_offset_clamps_to_zero(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 1 * time.Second, End: 3 * time.Second, Text: "A"},
	}

	shifted := ShiftCues(cues, -5*time.Second)

	if shifted[0].Start != 0 {
		t.Errorf("shifted[0].Start = %v, want 0 (clamped)", shifted[0].Start)
	}
	if shifted[0].End != 0 {
		t.Errorf("shifted[0].End = %v, want 0 (clamped)", shifted[0].End)
	}
}

func TestShiftCues_preserves_text(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 1 * time.Second, End: 2 * time.Second, Text: "Keep me"},
	}

	shifted := ShiftCues(cues, 500*time.Millisecond)
	if shifted[0].Text != "Keep me" {
		t.Errorf("ShiftCues() text = %q, want %q", shifted[0].Text, "Keep me")
	}
}

func TestShiftCues_does_not_mutate_original(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 1 * time.Second, End: 2 * time.Second, Text: "Original"},
	}

	_ = ShiftCues(cues, 5*time.Second)

	if cues[0].Start != 1*time.Second {
		t.Errorf("original cue mutated: Start = %v, want 1s", cues[0].Start)
	}
	if cues[0].End != 2*time.Second {
		t.Errorf("original cue mutated: End = %v, want 2s", cues[0].End)
	}
	if cues[0].Text != "Original" {
		t.Errorf("original cue mutated: Text = %q, want %q", cues[0].Text, "Original")
	}
}

func TestShiftCues_empty_slice(t *testing.T) {
	t.Parallel()
	shifted := ShiftCues(nil, 1*time.Second)
	if len(shifted) != 0 {
		t.Errorf("ShiftCues(nil) returned %d cues, want 0", len(shifted))
	}
}

func TestCuesToSpans(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 1500 * time.Millisecond, End: 3200 * time.Millisecond},
		{Start: 5 * time.Second, End: 8 * time.Second},
	}

	spans := cuesToSpans(cues)
	if len(spans) != 2 {
		t.Fatalf("CuesToSpans() returned %d spans, want 2", len(spans))
	}
	if spans[0].Start != 1500 || spans[0].End != 3200 {
		t.Errorf("spans[0] = {%d, %d}, want {1500, 3200}", spans[0].Start, spans[0].End)
	}
	if spans[1].Start != 5000 || spans[1].End != 8000 {
		t.Errorf("spans[1] = {%d, %d}, want {5000, 8000}", spans[1].Start, spans[1].End)
	}
}

func TestCuesToSpans_empty(t *testing.T) {
	t.Parallel()
	spans := cuesToSpans(nil)
	if len(spans) != 0 {
		t.Errorf("CuesToSpans(nil) returned %d spans, want 0", len(spans))
	}
}

func TestWriteSRT_error_propagation(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: time.Second, End: 2 * time.Second, Text: "Test"},
	}
	w := &failWriter{failAfter: 0}
	err := WriteSRT(w, cues)
	if err == nil {
		t.Error("WriteSRT(failWriter) expected error")
	}
}

func TestWriteSRT_negative_durations_clamped(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: -500 * time.Millisecond, End: -100 * time.Millisecond, Text: "Negative"},
	}

	var buf bytes.Buffer
	if err := WriteSRT(&buf, cues); err != nil {
		t.Fatalf("WriteSRT() unexpected error: %v", err)
	}

	want := "1\n00:00:00,000 --> 00:00:00,000\nNegative\n\n"
	if buf.String() != want {
		t.Errorf("WriteSRT(negative) output:\n%s\nwant:\n%s", buf.String(), want)
	}
}

func TestParseSRT_crlf_line_endings(t *testing.T) {
	t.Parallel()
	input := "1\r\n00:00:01,000 --> 00:00:02,000\r\nHello\r\n\r\n"
	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT(CRLF) unexpected error: %v", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT(CRLF) returned %d cues, want 1", len(cues))
	}
	if cues[0].Text != "Hello" {
		t.Errorf("cues[0].Text = %q, want %q", cues[0].Text, "Hello")
	}
}

func TestParseSRT_multiline_text(t *testing.T) {
	t.Parallel()
	input := "1\n00:00:01,000 --> 00:00:04,000\nLine one\nLine two\nLine three\n\n"
	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT() returned %d cues, want 1", len(cues))
	}
	if cues[0].Text != "Line one\nLine two\nLine three" {
		t.Errorf("cues[0].Text = %q, want %q", cues[0].Text, "Line one\nLine two\nLine three")
	}
}

func TestParseSRT_single_digit_hours(t *testing.T) {
	t.Parallel()
	input := "1\n0:00:01,000 --> 0:00:02,000\nShort hours\n\n"
	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT() returned %d cues, want 1", len(cues))
	}
	if cues[0].Start != time.Second {
		t.Errorf("cues[0].Start = %v, want 1s", cues[0].Start)
	}
}

func TestParseSRT_no_trailing_blank_line(t *testing.T) {
	t.Parallel()
	// Last cue not terminated by a blank line — parser must still capture it.
	input := "1\n00:00:01,000 --> 00:00:02,000\nNo trailing blank"
	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT() returned %d cues, want 1", len(cues))
	}
	if cues[0].Text != "No trailing blank" {
		t.Errorf("cues[0].Text = %q, want %q", cues[0].Text, "No trailing blank")
	}
}

func TestParseSRT_timing_arithmetic(t *testing.T) {
	t.Parallel()
	// The parseTime function computes: hours*Hour + mins*Minute + secs*Second + millis*Millisecond.
	// If any arithmetic is wrong, the parsed time is wrong.
	input := "1\n01:02:03,456 --> 01:02:06,789\nTest\n\n"
	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT() returned %d cues, want 1", len(cues))
	}

	wantStart := 1*time.Hour + 2*time.Minute + 3*time.Second + 456*time.Millisecond
	if cues[0].Start != wantStart {
		t.Errorf("cues[0].Start = %v, want %v", cues[0].Start, wantStart)
	}

	wantEnd := 1*time.Hour + 2*time.Minute + 6*time.Second + 789*time.Millisecond
	if cues[0].End != wantEnd {
		t.Errorf("cues[0].End = %v, want %v", cues[0].End, wantEnd)
	}
}

func TestFormatTime_boundary_values(t *testing.T) {
	t.Parallel()
	// Zero duration produces the zero timestamp.
	got := formatTime(0)
	if got != "00:00:00,000" {
		t.Errorf("formatTime(0) = %q, want %q", got, "00:00:00,000")
	}

	// Sub-millisecond duration (1ns) rounds to zero in millisecond computation.
	got = formatTime(1 * time.Nanosecond)
	if got != "00:00:00,000" {
		t.Errorf("formatTime(1ns) = %q, want %q", got, "00:00:00,000")
	}

	// Smallest positive duration: 1 millisecond.
	got = formatTime(1 * time.Millisecond)
	if got != "00:00:00,001" {
		t.Errorf("formatTime(1ms) = %q, want %q", got, "00:00:00,001")
	}

	// Negative durations clamp to zero.
	got = formatTime(-1 * time.Millisecond)
	if got != "00:00:00,000" {
		t.Errorf("formatTime(-1ms) = %q, want %q", got, "00:00:00,000")
	}

	// Large value exercises upper boundary of format fields.
	got = formatTime(99*time.Hour + 59*time.Minute + 59*time.Second + 999*time.Millisecond)
	if got != "99:59:59,999" {
		t.Errorf("formatTime(99:59:59,999) = %q, want %q", got, "99:59:59,999")
	}
}

func TestParseSRT_exact_millisecond_values(t *testing.T) {
	t.Parallel()
	// Additional precision test for parseTime arithmetic.
	// Verifies each component contributes correctly.
	input := "1\n00:00:00,001 --> 00:00:01,000\nOne ms\n\n" +
		"2\n00:01:00,000 --> 00:02:00,000\nOne min\n\n" +
		"3\n01:00:00,000 --> 02:00:00,000\nOne hour\n\n"

	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT() unexpected error: %v", err)
	}
	if len(cues) != 3 {
		t.Fatalf("ParseSRT() returned %d cues, want 3", len(cues))
	}

	if cues[0].Start != 1*time.Millisecond {
		t.Errorf("cues[0].Start = %v, want 1ms", cues[0].Start)
	}
	if cues[0].End != 1*time.Second {
		t.Errorf("cues[0].End = %v, want 1s", cues[0].End)
	}
	if cues[1].Start != 1*time.Minute {
		t.Errorf("cues[1].Start = %v, want 1m", cues[1].Start)
	}
	if cues[1].End != 2*time.Minute {
		t.Errorf("cues[1].End = %v, want 2m", cues[1].End)
	}
	if cues[2].Start != 1*time.Hour {
		t.Errorf("cues[2].Start = %v, want 1h", cues[2].Start)
	}
	if cues[2].End != 2*time.Hour {
		t.Errorf("cues[2].End = %v, want 2h", cues[2].End)
	}
}

func TestParseTime_error_branches(t *testing.T) {
	t.Parallel()
	// Covers the 4 error return paths in parseTime (srt.go:119-133).
	// These are unreachable via ParseSRT (regex pre-validates numeric format),
	// but testing them directly ensures the error handling is correct.
	tests := []struct {
		name        string
		h, m, s, ms string
	}{
		{"invalid hours", "abc", "00", "00", "000"},
		{"invalid minutes", "00", "xy", "00", "000"},
		{"invalid seconds", "00", "00", "zz", "000"},
		{"invalid milliseconds", "00", "00", "00", "nnn"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := parseTime(tt.h, tt.m, tt.s, tt.ms)
			if err == nil {
				t.Errorf("parseTime(%q, %q, %q, %q) = nil error, want error",
					tt.h, tt.m, tt.s, tt.ms)
			}
		})
	}
}

func TestParseSRT_garbage_between_cues(t *testing.T) {
	t.Parallel()
	// Non-timing, non-numeric lines between cues should be skipped.
	input := "1\n00:00:01,000 --> 00:00:02,000\nFirst\n\nrandom garbage\nmore junk\n\n2\n00:00:03,000 --> 00:00:04,000\nSecond\n\n"
	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT(garbage) unexpected error: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("ParseSRT(garbage) returned %d cues, want 2", len(cues))
	}
	if cues[0].Text != "First" {
		t.Errorf("cues[0].Text = %q, want %q", cues[0].Text, "First")
	}
	if cues[1].Text != "Second" {
		t.Errorf("cues[1].Text = %q, want %q", cues[1].Text, "Second")
	}
}

func TestParseSRT_empty_text_cue(t *testing.T) {
	t.Parallel()
	// A timing line followed immediately by a blank line produces a cue with empty text.
	input := "1\n00:00:01,000 --> 00:00:02,000\n\n"
	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT(empty text) unexpected error: %v", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT(empty text) returned %d cues, want 1", len(cues))
	}
	if cues[0].Text != "" {
		t.Errorf("cues[0].Text = %q, want empty", cues[0].Text)
	}
}

func TestParseSRT_whitespace_only_lines_between_cues(t *testing.T) {
	t.Parallel()
	// Lines with only spaces/tabs between cues should act as separators.
	input := "1\n00:00:01,000 --> 00:00:02,000\nHello\n   \n2\n00:00:03,000 --> 00:00:04,000\nWorld\n\n"
	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT(whitespace separator) unexpected error: %v", err)
	}
	if len(cues) != 2 {
		t.Fatalf("ParseSRT(whitespace separator) returned %d cues, want 2", len(cues))
	}
	if cues[0].Text != "Hello" {
		t.Errorf("cues[0].Text = %q, want %q", cues[0].Text, "Hello")
	}
	if cues[1].Text != "World" {
		t.Errorf("cues[1].Text = %q, want %q", cues[1].Text, "World")
	}
}

// --- Property-based tests ---

func TestParseSRT_WriteSRT_round_trip(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 20).Draw(t, "num_cues")
		cues := make([]Cue, n)
		for i := range n {
			cues[i] = genCue(t)
		}

		var buf bytes.Buffer
		if err := WriteSRT(&buf, cues); err != nil {
			t.Fatalf("WriteSRT() error: %v", err)
		}

		parsed, err := ParseSRT(&buf)
		if err != nil {
			t.Fatalf("ParseSRT() error: %v", err)
		}

		if len(parsed) != len(cues) {
			t.Fatalf("round-trip: got %d cues, want %d", len(parsed), len(cues))
		}

		for i := range cues {
			if parsed[i].Start != cues[i].Start {
				t.Errorf("cue[%d].Start = %v, want %v", i, parsed[i].Start, cues[i].Start)
			}
			if parsed[i].End != cues[i].End {
				t.Errorf("cue[%d].End = %v, want %v", i, parsed[i].End, cues[i].End)
			}
			if parsed[i].Text != cues[i].Text {
				t.Errorf("cue[%d].Text = %q, want %q", i, parsed[i].Text, cues[i].Text)
			}
		}
	})
}

func TestShiftCues_never_negative(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "num_cues")
		cues := make([]Cue, n)
		for i := range n {
			cues[i] = genCue(t)
		}

		offsetMs := rapid.Int64Range(-7_200_000, 7_200_000).Draw(t, "offset_ms")
		offset := time.Duration(offsetMs) * time.Millisecond

		shifted := ShiftCues(cues, offset)

		for i, c := range shifted {
			if c.Start < 0 {
				t.Errorf("shifted[%d].Start = %v, must be >= 0", i, c.Start)
			}
			if c.End < 0 {
				t.Errorf("shifted[%d].End = %v, must be >= 0", i, c.End)
			}
		}
	})
}

func TestShiftCues_idempotent_zero_offset(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "num_cues")
		cues := make([]Cue, n)
		for i := range n {
			cues[i] = genCue(t)
		}

		shifted := ShiftCues(cues, 0)

		for i := range cues {
			if shifted[i].Start != cues[i].Start {
				t.Errorf("ShiftCues(0)[%d].Start = %v, want %v",
					i, shifted[i].Start, cues[i].Start)
			}
			if shifted[i].End != cues[i].End {
				t.Errorf("ShiftCues(0)[%d].End = %v, want %v",
					i, shifted[i].End, cues[i].End)
			}
			if shifted[i].Text != cues[i].Text {
				t.Errorf("ShiftCues(0)[%d].Text = %q, want %q",
					i, shifted[i].Text, cues[i].Text)
			}
		}
	})
}

// --- Test helpers ---

type failWriter struct {
	failAfter int
	written   int
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.written >= f.failAfter {
		return 0, errors.New("write failed")
	}
	f.written += len(p)
	return len(p), nil
}

func genCue(t *rapid.T) Cue {
	startMs := rapid.Int64Range(0, 3_600_000).Draw(t, "start_ms")
	durMs := rapid.Int64Range(100, 10_000).Draw(t, "dur_ms")
	// Non-whitespace text avoids SRT parser treating it as a blank separator.
	text := rapid.StringMatching(`[A-Za-z0-9]{1,50}`).Draw(t, "text")
	return Cue{
		Start: time.Duration(startMs) * time.Millisecond,
		End:   time.Duration(startMs+durMs) * time.Millisecond,
		Text:  text,
	}
}

// --- Mutant-killing: ParseSRT scanner buffer ---

func TestParseSRT_large_line(t *testing.T) {
	t.Parallel()
	// Exercises the scanner buffer capacity. The default bufio.Scanner buffer
	// is 64KB; ParseSRT sets max to 1MB. A line longer than 64KB but shorter
	// than 1MB should parse successfully.
	// Build a cue with a very long text line (70KB).
	longText := strings.Repeat("A", 70_000)
	input := "1\n00:00:01,000 --> 00:00:02,000\n" + longText + "\n\n"

	cues, err := ParseSRT(strings.NewReader(input))
	if err != nil {
		t.Fatalf("ParseSRT(70KB line) unexpected error: %v", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT(70KB line) returned %d cues, want 1", len(cues))
	}
	if len(cues[0].Text) != 70_000 {
		t.Errorf("ParseSRT(70KB line) text length = %d, want 70000", len(cues[0].Text))
	}
}

// --- Mutant-killing: ParseSRT scanner.Err() propagation ---

func TestParseSRT_reader_error_propagated(t *testing.T) {
	t.Parallel()
	// ParseSRT returns scanner.Err() at the end. Verify that I/O errors
	// from the underlying reader are propagated, not silently swallowed.
	// The errReader returns valid SRT data then fails mid-stream.
	r := &errReader{
		data: "1\n00:00:01,000 --> 00:00:02,000\nHello\n\n2\n00:00:03,000 --> 00:00:04,000\n",
		err:  errors.New("disk read failed"),
	}
	cues, err := ParseSRT(r)
	if err == nil {
		t.Fatal("ParseSRT(errReader) expected error, got nil")
	}
	if err.Error() != "disk read failed" {
		t.Errorf("ParseSRT(errReader) error = %q, want %q", err.Error(), "disk read failed")
	}
	// Should still return cues parsed before the error.
	if len(cues) < 1 {
		t.Errorf("ParseSRT(errReader) returned %d cues, want >= 1", len(cues))
	}
}

// errReader returns data from a string, then returns an error.
type errReader struct {
	err  error
	data string
	off  int
}

func (r *errReader) Read(p []byte) (int, error) {
	if r.off >= len(r.data) {
		return 0, r.err
	}
	n := copy(p, r.data[r.off:])
	r.off += n
	return n, nil
}
