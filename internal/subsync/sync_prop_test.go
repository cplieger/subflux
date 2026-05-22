package subsync

import (
	"context"
	"testing"
	"time"

	"pgregory.net/rapid"
)

func TestSyncWithOptions_preserves_cue_count(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 50).Draw(t, "num_incorrect")
		incorrect := make([]Cue, n)
		for i := range incorrect {
			start := rapid.Int64Range(0, 3_600_000).Draw(t, "start")
			dur := rapid.Int64Range(500, 10_000).Draw(t, "dur")
			incorrect[i] = Cue{
				Text:  "line",
				Start: time.Duration(start) * time.Millisecond,
				End:   time.Duration(start+dur) * time.Millisecond,
			}
		}

		nRef := rapid.IntRange(0, 50).Draw(t, "num_reference")
		reference := make([]Cue, nRef)
		for i := range reference {
			start := rapid.Int64Range(0, 3_600_000).Draw(t, "ref_start")
			dur := rapid.Int64Range(500, 10_000).Draw(t, "ref_dur")
			reference[i] = Cue{
				Text:  "ref",
				Start: time.Duration(start) * time.Millisecond,
				End:   time.Duration(start+dur) * time.Millisecond,
			}
		}

		opts := &SyncOptions{
			EnableFramerate: rapid.Bool().Draw(t, "framerate"),
			EnableSplits:    rapid.Bool().Draw(t, "splits"),
			EnableAudio:     false, // requires ffmpeg
			MinConfidence:   ShouldApplyThreshold,
		}

		result := SyncWithOptions(context.Background(), reference, incorrect, opts)

		if len(result.Cues) != n {
			t.Fatalf("cue count changed: got %d, want %d (method=%s)",
				len(result.Cues), n, result.Method)
		}
	})
}
