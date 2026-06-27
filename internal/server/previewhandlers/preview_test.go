package previewhandlers

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
		{name: "mixed h:m:s.ms", ms: 3_723_456, want: "01:02:03.456"},
		{name: "negative clamped to zero", ms: -500, want: "00:00:00.000"},
		{name: "just under 24h", ms: 86_399_999, want: "23:59:59.999"},
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

	t.Run("empty cues yields header only", func(t *testing.T) {
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
}

// --- findDialogueDenseStart ---

func TestFindDialogueDenseStart(t *testing.T) {
	t.Parallel()

	t.Run("empty cues returns zero", func(t *testing.T) {
		t.Parallel()
		if got := findDialogueDenseStart(nil); got != 0 {
			t.Errorf("findDialogueDenseStart(nil) = %d, want 0", got)
		}
	})

	t.Run("single cue returns start minus lead-in", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 30 * time.Second, End: 32 * time.Second, Text: "Hello world"},
		}
		if got := findDialogueDenseStart(cues); got != 20_000 {
			t.Errorf("findDialogueDenseStart(single@30s) = %d, want 20000", got)
		}
	})

	t.Run("lead-in clamped to zero for early cue", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 5 * time.Second, End: 7 * time.Second, Text: "Early dialogue"},
		}
		if got := findDialogueDenseStart(cues); got != 0 {
			t.Errorf("findDialogueDenseStart(early cue@5s) = %d, want 0", got)
		}
	})

	t.Run("picks densest window", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 10 * time.Second, End: 12 * time.Second, Text: "Hi"},
			{Start: 120 * time.Second, End: 125 * time.Second, Text: "This is a much longer dialogue line with many characters"},
			{Start: 130 * time.Second, End: 135 * time.Second, Text: "Another long line of dialogue that adds character count"},
			{Start: 140 * time.Second, End: 145 * time.Second, Text: "And yet another substantial piece of dialogue text here"},
			{Start: 150 * time.Second, End: 155 * time.Second, Text: "Final long dialogue line in the dense window section here"},
		}
		if got := findDialogueDenseStart(cues); got != 110_000 {
			t.Errorf("findDialogueDenseStart(dense@120s) = %d, want 110000", got)
		}
	})

	t.Run("whitespace-only cues ignored in density", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: 60 * time.Second, End: 62 * time.Second, Text: "   \t  \n  "},
			{Start: 120 * time.Second, End: 122 * time.Second, Text: "Real dialogue"},
		}
		if got := findDialogueDenseStart(cues); got != 110_000 {
			t.Errorf("findDialogueDenseStart(whitespace+real) = %d, want 110000", got)
		}
	})

	t.Run("ties keep the earliest dense window", func(t *testing.T) {
		t.Parallel()
		// Two equally dense, non-overlapping windows 100s apart. A strict ">"
		// keeps the FIRST anchor (20s -> start 10s). A ">=" would drift to the
		// later anchor (120s -> start 110s), so this pins the boundary.
		cues := []api.SubtitleCue{
			{Start: 20 * time.Second, End: 22 * time.Second, Text: "ab"},
			{Start: 120 * time.Second, End: 122 * time.Second, Text: "ab"},
		}
		if got := findDialogueDenseStart(cues); got != 10_000 {
			t.Errorf("findDialogueDenseStart(tie) = %d, want 10000 (earliest window)", got)
		}
	})
}

// --- shiftAndFilterCues ---

func TestShiftAndFilterCues(t *testing.T) {
	t.Parallel()

	t.Run("zero shift returns cues unchanged", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: time.Second, End: 2 * time.Second, Text: "Hello"},
		}
		got := shiftAndFilterCues(cues, 0)
		if len(got) != 1 || got[0].Text != "Hello" {
			t.Fatalf("shiftAndFilterCues(1 cue, 0) = %+v, want unchanged single cue", got)
		}
	})

	t.Run("positive shift moves start and end", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: time.Second, End: 3 * time.Second, Text: "A"},
		}
		got := shiftAndFilterCues(cues, 500*time.Millisecond)
		if len(got) != 1 {
			t.Fatalf("shiftAndFilterCues(+500ms) returned %d cues, want 1", len(got))
		}
		if got[0].Start != 1500*time.Millisecond {
			t.Errorf("shiftAndFilterCues(+500ms)[0].Start = %v, want 1.5s", got[0].Start)
		}
		if got[0].End != 3500*time.Millisecond {
			t.Errorf("shiftAndFilterCues(+500ms)[0].End = %v, want 3.5s", got[0].End)
		}
	})

	t.Run("negative shift drops cues that end before zero", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: time.Second, End: 2 * time.Second, Text: "Early"},
			{Start: 5 * time.Second, End: 7 * time.Second, Text: "Late"},
		}
		got := shiftAndFilterCues(cues, -3*time.Second)
		if len(got) != 1 {
			t.Fatalf("shiftAndFilterCues(-3s) returned %d cues, want 1", len(got))
		}
		if got[0].Text != "Late" {
			t.Errorf("shiftAndFilterCues(-3s)[0].Text = %q, want %q", got[0].Text, "Late")
		}
		if got[0].Start != 2*time.Second {
			t.Errorf("shiftAndFilterCues(-3s)[0].Start = %v, want 2s", got[0].Start)
		}
	})

	t.Run("start clamped to zero when shifted past zero", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: time.Second, End: 5 * time.Second, Text: "Overlap"},
		}
		got := shiftAndFilterCues(cues, -2*time.Second)
		if len(got) != 1 {
			t.Fatalf("shiftAndFilterCues(-2s) returned %d cues, want 1", len(got))
		}
		if got[0].Start != 0 {
			t.Errorf("shiftAndFilterCues(-2s)[0].Start = %v, want 0", got[0].Start)
		}
		if got[0].End != 3*time.Second {
			t.Errorf("shiftAndFilterCues(-2s)[0].End = %v, want 3s", got[0].End)
		}
	})

	t.Run("end exactly zero is filtered, one ms past is kept", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: time.Second, End: 2 * time.Second, Text: "Exact"},
		}
		if got := shiftAndFilterCues(cues, -2*time.Second); len(got) != 0 {
			t.Errorf("shiftAndFilterCues(End=2s, -2s) returned %d cues, want 0 (newEnd=0 filtered)", len(got))
		}
		got := shiftAndFilterCues(cues, -1999*time.Millisecond)
		if len(got) != 1 {
			t.Fatalf("shiftAndFilterCues(End=2s, -1999ms) returned %d cues, want 1 (newEnd=1ms kept)", len(got))
		}
		if got[0].End != time.Millisecond {
			t.Errorf("shiftAndFilterCues(End=2s, -1999ms)[0].End = %v, want 1ms", got[0].End)
		}
	})

	t.Run("all cues filtered when shifted far negative", func(t *testing.T) {
		t.Parallel()
		cues := []api.SubtitleCue{
			{Start: time.Second, End: 2 * time.Second, Text: "A"},
			{Start: 3 * time.Second, End: 4 * time.Second, Text: "B"},
		}
		if got := shiftAndFilterCues(cues, -5*time.Second); len(got) != 0 {
			t.Errorf("shiftAndFilterCues(-5s) returned %d cues, want 0", len(got))
		}
	})

	t.Run("nil input with nonzero shift returns empty", func(t *testing.T) {
		t.Parallel()
		if got := shiftAndFilterCues(nil, time.Second); len(got) != 0 {
			t.Errorf("shiftAndFilterCues(nil, 1s) returned %d cues, want 0", len(got))
		}
	})
}

// --- resolveArrConfig ---

func TestResolveArrConfig(t *testing.T) {
	t.Parallel()

	ls := &LiveState{
		SonarrConfig: ArrConfig{URL: "http://sonarr:8989", APIKey: "sonarr-key"},
		RadarrConfig: ArrConfig{URL: "http://radarr:7878", APIKey: "radarr-key"},
		HasSonarr:    true,
		HasRadarr:    true,
	}

	t.Run("movie resolves to radarr", func(t *testing.T) {
		t.Parallel()
		url, key, ok := resolveArrConfig(ls, "movie")
		if !ok || url != "http://radarr:7878" || key != "radarr-key" {
			t.Errorf("resolveArrConfig(movie) = (%q, %q, %v), want radarr config, true", url, key, ok)
		}
	})

	t.Run("series resolves to sonarr", func(t *testing.T) {
		t.Parallel()
		url, key, ok := resolveArrConfig(ls, "series")
		if !ok || url != "http://sonarr:8989" || key != "sonarr-key" {
			t.Errorf("resolveArrConfig(series) = (%q, %q, %v), want sonarr config, true", url, key, ok)
		}
	})

	t.Run("unknown media type returns not ok", func(t *testing.T) {
		t.Parallel()
		url, key, ok := resolveArrConfig(ls, "tv")
		if ok || url != "" || key != "" {
			t.Errorf("resolveArrConfig(tv) = (%q, %q, %v), want empty, false", url, key, ok)
		}
	})

	t.Run("movie not ok when radarr absent", func(t *testing.T) {
		t.Parallel()
		none := &LiveState{HasSonarr: false, HasRadarr: false}
		if _, _, ok := resolveArrConfig(none, "movie"); ok {
			t.Error("resolveArrConfig(movie, no radarr) ok = true, want false")
		}
	})

	t.Run("series not ok when sonarr absent", func(t *testing.T) {
		t.Parallel()
		none := &LiveState{HasSonarr: false, HasRadarr: false}
		if _, _, ok := resolveArrConfig(none, "series"); ok {
			t.Error("resolveArrConfig(series, no sonarr) ok = true, want false")
		}
	})
}

// --- properties ---

func TestMsToVTT_property_format_always_valid(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		ms := rapid.Int64Range(-10_000, 360_000_000).Draw(t, "ms")
		got := msToVTT(ms)
		n := len(got)
		if n < 12 {
			t.Fatalf("msToVTT(%d) = %q, length %d, want >= 12", ms, got, n)
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
		if arrows := strings.Count(got, " --> "); arrows != n {
			t.Errorf("srtToWebVTT(%d cues) has %d arrow separators, want %d", n, arrows, n)
		}
	})
}

func TestShiftAndFilterCues_property_output_bounded_and_ordered(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(0, 20).Draw(t, "n")
		cues := make([]api.SubtitleCue, n)
		var cursor int64
		for i := range n {
			cursor += rapid.Int64Range(0, 30_000).Draw(t, fmt.Sprintf("gap_%d", i))
			durMs := rapid.Int64Range(1, 60_000).Draw(t, fmt.Sprintf("dur_%d", i))
			cues[i] = api.SubtitleCue{
				Start: time.Duration(cursor) * time.Millisecond,
				End:   time.Duration(cursor+durMs) * time.Millisecond,
				Text:  fmt.Sprintf("cue %d", i),
			}
			cursor += durMs
		}
		shift := time.Duration(rapid.Int64Range(-300_000, 300_000).Draw(t, "shift")) * time.Millisecond

		result := shiftAndFilterCues(cues, shift)

		if len(result) > len(cues) {
			t.Fatalf("shiftAndFilterCues(%d cues, %v) returned %d cues, want <= %d",
				len(cues), shift, len(result), len(cues))
		}
		for i, c := range result {
			if c.Start < 0 {
				t.Errorf("result[%d].Start = %v, want >= 0", i, c.Start)
			}
			if c.End <= 0 {
				t.Errorf("result[%d].End = %v, want > 0", i, c.End)
			}
			if i > 0 && result[i].Start < result[i-1].Start {
				t.Errorf("result[%d].Start = %v < result[%d].Start = %v, ordering violated",
					i, result[i].Start, i-1, result[i-1].Start)
			}
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
		if got := findDialogueDenseStart(cues); got < 0 {
			t.Errorf("findDialogueDenseStart(%d cues) = %d, want >= 0", n, got)
		}
	})
}
