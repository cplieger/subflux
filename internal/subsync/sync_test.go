package subsync

import (
	"context"
	"testing"
	"time"
)

func TestSyncWithOptions_empty_incorrect(t *testing.T) {
	t.Parallel()
	opts := DefaultSyncOptions()
	result := SyncWithOptions(context.Background(), nil, nil, &opts)
	if result.Method != MethodNone {
		t.Fatalf("expected method 'none', got %q", result.Method)
	}
	if result.Confidence != ConfidenceNone {
		t.Fatalf("expected no confidence, got %f", float64(result.Confidence))
	}
}

func TestSyncWithOptions_no_reference_no_audio(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(30, 10*time.Minute)
	opts := DefaultSyncOptions()
	opts.EnableAudio = false
	result := SyncWithOptions(context.Background(), nil, inc, &opts)
	// No reference and no audio: should return original cues.
	if len(result.Cues) != len(inc) {
		t.Fatalf("expected %d cues, got %d", len(inc), len(result.Cues))
	}
}

func TestSyncWithOptions_constant_offset(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	opts := DefaultSyncOptions()
	opts.EnableFramerate = false
	opts.EnableSplits = false
	result := SyncWithOptions(context.Background(), ref, inc, &opts)
	// Accept any method that finds the correct offset.
	if result.Method == MethodNone {
		t.Fatalf("expected a sync method, got %q", result.Method)
	}
	if abs64(result.Offset-(-2000)) > 100 {
		t.Fatalf("expected offset ~-2000ms, got %d", result.Offset)
	}
}

func TestSyncWithOptions_with_reference(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 3*time.Second)
	opts := DefaultSyncOptions()
	result := SyncWithOptions(context.Background(), ref, inc, &opts)
	if len(result.Cues) != len(inc) {
		t.Fatalf("expected %d cues, got %d", len(inc), len(result.Cues))
	}
}

func TestSyncWithOptions_audio_disabled(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(30, 10*time.Minute)
	opts := DefaultSyncOptions()
	opts.EnableAudio = false
	opts.VideoPath = "/some/video.mkv"
	result := SyncWithOptions(context.Background(), nil, inc, &opts)
	// Audio disabled: should not attempt audio sync.
	if result.Method == MethodAudio {
		t.Fatal("audio sync should not run when disabled")
	}
}

func TestSyncWithOptions_audio_no_video_path(t *testing.T) {
	t.Parallel()
	inc := makeLongCues(30, 10*time.Minute)
	opts := DefaultSyncOptions()
	opts.EnableAudio = true
	opts.VideoPath = ""
	result := SyncWithOptions(context.Background(), nil, inc, &opts)
	if result.Method == MethodAudio {
		t.Fatal("audio sync should not run without video path")
	}
}

func TestConstantOffsetConfidence(t *testing.T) {
	t.Parallel()
	longCues := makeLongCues(20, 5*time.Minute)
	shiftedCues := ShiftCues(longCues, 2*time.Second)
	tests := []struct {
		name    string
		ref     []Cue
		inc     []Cue
		offset  time.Duration
		wantMin Confidence
		wantMax Confidence
	}{
		{
			name:    "identical cues high confidence",
			ref:     longCues,
			inc:     longCues,
			offset:  0,
			wantMin: 0.8,
			wantMax: 0.9,
		},
		{
			name:    "nil both returns none",
			ref:     nil,
			inc:     nil,
			offset:  0,
			wantMin: ConfidenceNone,
			wantMax: ConfidenceNone,
		},
		{
			name:    "zero length ref spans returns none",
			ref:     []Cue{{Start: time.Second, End: time.Second, Text: "zero"}},
			inc:     []Cue{{Start: time.Second, End: 2 * time.Second, Text: "normal"}},
			offset:  0,
			wantMin: ConfidenceNone,
			wantMax: ConfidenceNone,
		},
		{
			name:    "nil reference returns none",
			ref:     nil,
			inc:     []Cue{{Start: time.Second, End: 2 * time.Second, Text: "A"}},
			offset:  0,
			wantMin: ConfidenceNone,
			wantMax: ConfidenceNone,
		},
		{
			name:    "nil incorrect returns none",
			ref:     []Cue{{Start: time.Second, End: 2 * time.Second, Text: "A"}},
			inc:     nil,
			offset:  0,
			wantMin: ConfidenceNone,
			wantMax: ConfidenceNone,
		},
		{
			name:    "high overlap perfect shift",
			ref:     longCues,
			inc:     shiftedCues,
			offset:  -2 * time.Second,
			wantMin: 0.8,
			wantMax: 0.9,
		},
		{
			name:    "ratio capped at 0.9",
			ref:     longCues,
			inc:     longCues,
			offset:  0,
			wantMin: 0,
			wantMax: 0.9,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := constantOffsetConfidence(tt.ref, tt.inc, tt.offset)
			if c < tt.wantMin {
				t.Errorf("constantOffsetConfidence() = %f, want >= %f", float64(c), float64(tt.wantMin))
			}
			if c > tt.wantMax {
				t.Errorf("constantOffsetConfidence() = %f, want <= %f", float64(c), float64(tt.wantMax))
			}
		})
	}
}

func TestReferenceSync_prefers_higher_confidence(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	opts := DefaultSyncOptions()
	result := referenceSync(context.Background(), ref, inc, &opts)
	if result.Confidence == ConfidenceNone {
		t.Fatal("expected some confidence from reference sync")
	}
	if len(result.Cues) != len(inc) {
		t.Fatalf("expected %d cues, got %d", len(inc), len(result.Cues))
	}
}

func TestDefaultSyncOptions(t *testing.T) {
	t.Parallel()
	opts := DefaultSyncOptions()
	if !opts.EnableFramerate {
		t.Fatal("framerate should be enabled by default")
	}
	if !opts.EnableSplits {
		t.Fatal("splits should be enabled by default")
	}
	if opts.EnableAudio {
		t.Fatal("audio should be disabled by default")
	}
	if opts.MinConfidence != 0.5 {
		t.Fatalf("expected min confidence 0.5, got %f", float64(opts.MinConfidence))
	}
}

func TestSyncWithOptions_zero_min_confidence_defaults(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	opts := SyncOptions{
		EnableFramerate: false,
		EnableSplits:    false,
		MinConfidence:   0, // should default to 0.5
	}
	result := SyncWithOptions(context.Background(), ref, inc, &opts)
	if result.Method == MethodNone {
		t.Errorf("expected a sync method, got %q", result.Method)
	}
}

func TestSyncWithOptions_negative_min_confidence_defaults(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	opts := SyncOptions{
		EnableFramerate: false,
		EnableSplits:    false,
		MinConfidence:   -1, // should default to 0.5
	}
	result := SyncWithOptions(context.Background(), ref, inc, &opts)
	if result.Method == MethodNone {
		t.Errorf("expected a sync method, got %q", result.Method)
	}
}

func TestReferenceSync_framerate_strong_returns_early(t *testing.T) {
	t.Parallel()
	// Create a clear framerate mismatch that produces strong confidence.
	ref := makeLongCues(100, 30*time.Minute)
	ratio := 25.0 / 23.976
	inc := scaleCuesForTest(ref, ratio)

	opts := SyncOptions{
		EnableFramerate: true,
		EnableSplits:    true,
		MinConfidence:   0.5,
	}
	result := referenceSync(context.Background(), ref, inc, &opts)
	if result.Method != MethodFramerate {
		t.Errorf("expected method 'framerate', got %q", result.Method)
	}
	if result.Confidence < ConfidenceStrong {
		t.Errorf("expected strong confidence, got %f", float64(result.Confidence))
	}
}

func TestReferenceSync_splits_strong_returns_early(t *testing.T) {
	t.Parallel()
	// Create a two-segment case with clear offsets.
	ref := makeLongCues(40, 20*time.Minute)
	inc := make([]Cue, 40)
	for i := range 20 {
		inc[i] = Cue{
			Start: ref[i].Start + time.Second,
			End:   ref[i].End + time.Second,
			Text:  ref[i].Text,
		}
	}
	for i := 20; i < 40; i++ {
		inc[i] = Cue{
			Start: ref[i].Start + 10*time.Second,
			End:   ref[i].End + 10*time.Second,
			Text:  ref[i].Text,
		}
	}

	opts := SyncOptions{
		EnableFramerate: false,
		EnableSplits:    true,
		SplitPenalty:    100,
		MinConfidence:   0.5,
	}
	result := referenceSync(context.Background(), ref, inc, &opts)
	// Should use split method since there's a clear split point.
	if len(result.Cues) != 40 {
		t.Errorf("expected 40 cues, got %d", len(result.Cues))
	}
}

func TestSyncWithOptions_low_confidence_fallback(t *testing.T) {
	t.Parallel()
	// With very few cues, reference sync produces low confidence.
	// The fallback should return the original cues.
	ref := makeCues(3, 0, 2*time.Second)
	inc := makeCues(3, 10*time.Second, 2*time.Second)
	opts := SyncOptions{
		EnableFramerate: false,
		EnableSplits:    false,
		MinConfidence:   0.99, // very high threshold → nothing passes
	}
	result := SyncWithOptions(context.Background(), ref, inc, &opts)
	if len(result.Cues) != 3 {
		t.Fatalf("expected 3 cues in fallback, got %d", len(result.Cues))
	}
}

func TestSyncWithOptions_reference_below_threshold_returns_best(t *testing.T) {
	t.Parallel()
	// Reference sync produces some confidence but below threshold.
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	opts := SyncOptions{
		EnableFramerate: false,
		EnableSplits:    false,
		MinConfidence:   0.99, // very high → offset sync won't meet it
	}
	result := SyncWithOptions(context.Background(), ref, inc, &opts)
	// Should still return the best result found (any method).
	if result.Method == MethodNone {
		t.Errorf("expected a sync method, got %q", result.Method)
	}
}

func TestSyncWithOptions_nil_opts_uses_defaults(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)

	result := SyncWithOptions(context.Background(), ref, inc, nil)

	if result.Method == MethodNone {
		t.Errorf("SyncWithOptions(context.Background(), ref, inc, nil) method = %q, want a sync method", result.Method)
	}
	if len(result.Cues) != len(inc) {
		t.Errorf("SyncWithOptions(context.Background(), ref, inc, nil) cue count = %d, want %d", len(result.Cues), len(inc))
	}
}

func TestReferenceSync_no_candidates_returns_original(t *testing.T) {
	t.Parallel()
	// Zero-length cues produce totalRef == 0 in constantOffsetConfidence,
	// and crossLangAlign returns ConfidenceNone for single-cue inputs.
	// This exercises the len(candidates) == 0 early return.
	ref := []Cue{
		{Start: time.Second, End: time.Second, Text: "A"},
	}
	inc := []Cue{
		{Start: 2 * time.Second, End: 2 * time.Second, Text: "Z"},
	}
	opts := SyncOptions{
		EnableFramerate: false,
		EnableSplits:    false,
		MinConfidence:   0.5,
	}
	result := referenceSync(context.Background(), ref, inc, &opts)
	if result.Method != MethodNone {
		t.Errorf("referenceSync(zero-length cues) method = %q, want %q", result.Method, MethodNone)
	}
	if result.Confidence != ConfidenceNone {
		t.Errorf("referenceSync(zero-length cues) confidence = %f, want 0", float64(result.Confidence))
	}
	if len(result.Cues) != 1 {
		t.Errorf("referenceSync(zero-length cues) cue count = %d, want 1", len(result.Cues))
	}
}
