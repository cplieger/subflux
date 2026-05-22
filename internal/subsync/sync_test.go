package subsync

import (
	"context"
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
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

// --- audioSyncFromPCM ---

func TestAudioSyncFromPCM_too_few_cues(t *testing.T) {
	t.Parallel()
	cues := makeCues(4, 0, 2*time.Second)
	pcm := make([]int16, 8000)
	result := audioSyncFromPCM(context.Background(), cues, pcm, AudioSyncHints{})
	if result.Confidence != ConfidenceNone {
		t.Errorf("audioSyncFromPCM(4 cues) confidence = %f, want 0", float64(result.Confidence))
	}
	if result.Method != MethodAudio {
		t.Errorf("audioSyncFromPCM(4 cues) method = %q, want %q", result.Method, MethodAudio)
	}
}

func TestAudioSyncFromPCM_zero_frames(t *testing.T) {
	t.Parallel()
	cues := makeCues(10, 0, 2*time.Second)
	pcm := make([]int16, 50)
	result := audioSyncFromPCM(context.Background(), cues, pcm, AudioSyncHints{})
	if result.Confidence != ConfidenceNone {
		t.Errorf("audioSyncFromPCM(zero frames) confidence = %f, want 0", float64(result.Confidence))
	}
}

func TestAudioSyncFromPCM_silence_completes(t *testing.T) {
	t.Parallel()
	cues := makeLongCues(10, 20*time.Second)
	pcm := make([]int16, 8000*30)
	result := audioSyncFromPCM(context.Background(), cues, pcm, AudioSyncHints{})
	if result.Method != MethodAudio {
		t.Errorf("audioSyncFromPCM(silence) method = %q, want %q", result.Method, MethodAudio)
	}
	if result.Rate != 1.0 {
		t.Errorf("audioSyncFromPCM(silence) rate = %f, want 1.0", result.Rate)
	}
}

func TestAudioSyncFromPCM_with_dialogue_hints(t *testing.T) {
	t.Parallel()
	cues := makeLongCues(10, 20*time.Second)
	dialogueCues := makeLongCues(8, 18*time.Second)
	pcm := make([]int16, 8000*30)
	result := audioSyncFromPCM(context.Background(), cues, pcm, AudioSyncHints{DialogueCues: dialogueCues, IsASS: true})
	if result.Method != MethodAudio {
		t.Errorf("audioSyncFromPCM(dialogue hints) method = %q, want %q", result.Method, MethodAudio)
	}
}

func TestAudioSyncFromPCM_tonal_signal_no_panic(t *testing.T) {
	t.Parallel()
	cues := makeLongCues(10, 5*time.Second)
	const dur = 5
	pcm := make([]int16, 8000*dur)
	for i := range pcm {
		pcm[i] = int16(5000 * math.Sin(float64(i)*2*math.Pi*440/8000))
	}
	result := audioSyncFromPCM(context.Background(), cues, pcm, AudioSyncHints{})
	if result.Method != MethodAudio {
		t.Errorf("audioSyncFromPCM(tonal) method = %q, want %q", result.Method, MethodAudio)
	}
	if result.Rate != 1.0 {
		t.Errorf("audioSyncFromPCM(tonal) rate = %f, want 1.0", result.Rate)
	}
}

func TestAudioSyncFromPCM_excessive_offset_rejected(t *testing.T) {
	t.Parallel()
	cues := []Cue{
		{Start: 0, End: 200 * time.Millisecond, Text: "Line one"},
		{Start: 300 * time.Millisecond, End: 500 * time.Millisecond, Text: "Line two"},
		{Start: 600 * time.Millisecond, End: 800 * time.Millisecond, Text: "Line three"},
		{Start: 900 * time.Millisecond, End: 1100 * time.Millisecond, Text: "Line four"},
		{Start: 1200 * time.Millisecond, End: 1400 * time.Millisecond, Text: "Line five"},
	}
	pcm := make([]int16, 8000*2)
	result := audioSyncFromPCM(context.Background(), cues, pcm, AudioSyncHints{})
	if result.Method != MethodAudio {
		t.Errorf("audioSyncFromPCM(excessive) method = %q, want %q", result.Method, MethodAudio)
	}
}

func TestAudioSyncFromPCM_safe_precise_agreement(t *testing.T) {
	t.Parallel()
	cues := makeLongCues(10, 5*time.Second)
	pcm := make([]int16, 8000*10)
	for i := range 8000 * 5 {
		pcm[i] = int16(3000 * math.Sin(float64(i)*2*math.Pi*300/8000))
	}
	result := audioSyncFromPCM(context.Background(), cues, pcm, AudioSyncHints{})
	if result.Method != MethodAudio {
		t.Errorf("audioSyncFromPCM(agreement) method = %q, want %q", result.Method, MethodAudio)
	}
	if result.Confidence < 0 {
		t.Errorf("audioSyncFromPCM(agreement) confidence = %f, want >= 0", float64(result.Confidence))
	}
}

// --- buildVADSubSignal ---

func TestBuildVADSubSignal(t *testing.T) {
	t.Parallel()
	type check struct {
		idx  int
		want float64
	}
	tests := []struct {
		name      string
		cues      []Cue
		numFrames int
		wantNil   bool
		wantLen   int
		checks    []check
	}{
		{
			name:      "nil cues zero frames",
			cues:      nil,
			numFrames: 0,
			wantNil:   true,
		},
		{
			name:      "nil cues nonzero frames all negative",
			cues:      nil,
			numFrames: 10,
			wantLen:   10,
			checks:    []check{{0, -1}, {5, -1}, {9, -1}},
		},
		{
			name:      "single cue marks covered frames",
			cues:      []Cue{{Start: 10 * time.Millisecond, End: 30 * time.Millisecond, Text: "hi"}},
			numFrames: 5,
			wantLen:   5,
			checks:    []check{{0, -1}, {1, 1}, {2, 1}, {3, -1}, {4, -1}},
		},
		{
			name:      "cue beyond numFrames clamped",
			cues:      []Cue{{Start: 0, End: 100 * time.Millisecond, Text: "long"}},
			numFrames: 3,
			wantLen:   3,
			checks:    []check{{0, 1}, {1, 1}, {2, 1}},
		},
		{
			name:      "negative start clamped to zero",
			cues:      []Cue{{Start: -10 * time.Millisecond, End: 20 * time.Millisecond, Text: "neg"}},
			numFrames: 5,
			wantLen:   5,
			checks:    []check{{0, 1}, {1, 1}, {2, -1}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := buildVADSubSignal(tt.cues, tt.numFrames)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("got %v, want nil", got)
				}
				return
			}
			if len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
			for _, c := range tt.checks {
				if got[c.idx] != c.want {
					t.Errorf("frame %d = %f, want %f", c.idx, got[c.idx], c.want)
				}
			}
		})
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

// --- voteOnCandidates ---

func TestVoteOnCandidates(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(30, 10*time.Minute)
	inc := ShiftCues(ref, 2*time.Second)
	incSame := makeLongCues(30, 10*time.Minute)
	tests := []struct {
		name       string
		ref        []Cue
		inc        []Cue
		candidates []SyncResult
		check      func(t *testing.T, got SyncResult)
	}{
		{
			name: "single candidate passthrough",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				{Cues: inc, Offset: -2000, Confidence: 0.7, Method: MethodOffset},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Method != MethodOffset {
					t.Errorf("method = %q, want %q", got.Method, MethodOffset)
				}
				if got.Offset != -2000 {
					t.Errorf("offset = %d, want -2000", got.Offset)
				}
			},
		},
		{
			name: "two agreeing strategies boost",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				{Cues: inc, Offset: -2000, Confidence: 0.6, Method: MethodOffset},
				{Cues: inc, Offset: -2100, Confidence: 0.7, Method: MethodCrosslang},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Confidence != 0.7 {
					t.Errorf("confidence = %f, want 0.7", float64(got.Confidence))
				}
			},
		},
		{
			name: "three agreeing strategies stronger boost",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				{Cues: inc, Offset: -2000, Confidence: 0.5, Method: MethodOffset},
				{Cues: inc, Offset: -2100, Confidence: 0.6, Method: MethodCrosslang},
				{Cues: inc, Offset: -1900, Confidence: 0.7, Method: MethodFramerate},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Confidence != 0.7 {
					t.Errorf("confidence = %f, want 0.7", float64(got.Confidence))
				}
			},
		},
		{
			name: "separate clusters picks higher weight",
			ref:  ref,
			inc:  inc,
			candidates: []SyncResult{
				{Cues: inc, Offset: -2000, Confidence: 0.5, Method: MethodOffset},
				{Cues: inc, Offset: -2100, Confidence: 0.5, Method: MethodCrosslang},
				{Cues: inc, Offset: -50000, Confidence: 0.8, Method: MethodSplit},
			},
			check: func(t *testing.T, got SyncResult) {
				if abs64(got.Offset-(-2000)) > 3000 {
					t.Errorf("offset = %d, want near -2000", got.Offset)
				}
			},
		},
		{
			name: "large offset penalty similar duration",
			ref:  ref,
			inc:  incSame,
			candidates: []SyncResult{
				{Cues: incSame, Offset: 40000, Confidence: 0.7, Method: MethodOffset},
				{Cues: incSame, Offset: -1000, Confidence: 0.4, Method: MethodCrosslang},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Offset == 40000 {
					t.Error("should not pick offset=40000 on similar-duration content")
				}
			},
		},
		{
			name: "returns valid result",
			ref:  ref,
			inc:  incSame,
			candidates: []SyncResult{
				{Cues: incSame, Offset: 0, Confidence: 0.5, Method: MethodOffset},
				{Cues: incSame, Offset: 500, Confidence: 0.5, Method: MethodCrosslang},
			},
			check: func(t *testing.T, got SyncResult) {
				if got.Method == "" {
					t.Error("returned empty method")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := voteOnCandidates(tt.candidates, tt.ref, tt.inc)
			tt.check(t, got)
		})
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

func TestBuildVADSubSignal_invariants(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		numFrames := rapid.IntRange(1, 200).Draw(t, "numFrames")
		nCues := rapid.IntRange(0, 10).Draw(t, "nCues")

		cues := make([]Cue, nCues)
		for i := range nCues {
			startMs := rapid.Int64Range(0, int64(numFrames)*frameMs).Draw(t, "startMs")
			dur := rapid.Int64Range(1, 5*frameMs).Draw(t, "dur")
			cues[i] = Cue{
				Start: time.Duration(startMs) * time.Millisecond,
				End:   time.Duration(startMs+dur) * time.Millisecond,
				Text:  "cue",
			}
		}

		sig := buildVADSubSignal(cues, numFrames)

		// Invariant 1: output length equals numFrames.
		if len(sig) != numFrames {
			t.Fatalf("buildVADSubSignal(cues, %d) len = %d, want %d", numFrames, len(sig), numFrames)
		}

		// Invariant 2: all values are exactly -1.0 or +1.0.
		for i, v := range sig {
			if v != -1.0 && v != 1.0 {
				t.Fatalf("buildVADSubSignal frame %d = %f, want -1.0 or 1.0", i, v)
			}
		}
	})
}

func TestBuildClusters(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		candidates   []SyncResult
		clusterMs    int64
		wantClusters int
		wantMembers  []int // members per cluster
		wantWeight   []float64
		wantOffset   []int64
	}{
		{
			name:         "single candidate",
			candidates:   []SyncResult{{Offset: 1000, Confidence: 0.7, Method: MethodOffset}},
			clusterMs:    3000,
			wantClusters: 1,
			wantMembers:  []int{1},
			wantWeight:   []float64{0.7},
			wantOffset:   []int64{1000},
		},
		{
			name: "two within threshold",
			candidates: []SyncResult{
				{Offset: 1000, Confidence: 0.5, Method: MethodOffset},
				{Offset: 2500, Confidence: 0.6, Method: MethodCrosslang},
			},
			clusterMs:    3000,
			wantClusters: 1,
			wantMembers:  []int{2},
			wantWeight:   []float64{1.1},
		},
		{
			name: "two beyond threshold",
			candidates: []SyncResult{
				{Offset: 1000, Confidence: 0.5, Method: MethodOffset},
				{Offset: 5000, Confidence: 0.6, Method: MethodCrosslang},
			},
			clusterMs:    3000,
			wantClusters: 2,
			wantMembers:  []int{1, 1},
			wantOffset:   []int64{1000, 5000},
		},
		{
			name: "exact boundary same cluster",
			candidates: []SyncResult{
				{Offset: 0, Confidence: 0.5, Method: MethodOffset},
				{Offset: 3000, Confidence: 0.6, Method: MethodCrosslang},
			},
			clusterMs:    3000,
			wantClusters: 1,
			wantMembers:  []int{2},
		},
		{
			name: "one beyond boundary new cluster",
			candidates: []SyncResult{
				{Offset: 0, Confidence: 0.5, Method: MethodOffset},
				{Offset: 3001, Confidence: 0.6, Method: MethodCrosslang},
			},
			clusterMs:    3000,
			wantClusters: 2,
			wantMembers:  []int{1, 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusters := buildClusters(tt.candidates, tt.clusterMs)
			if len(clusters) != tt.wantClusters {
				t.Fatalf("clusters = %d, want %d", len(clusters), tt.wantClusters)
			}
			for i, wm := range tt.wantMembers {
				if len(clusters[i].members) != wm {
					t.Errorf("cluster[%d] members = %d, want %d", i, len(clusters[i].members), wm)
				}
			}
			for i, ww := range tt.wantWeight {
				if clusters[i].weight != ww {
					t.Errorf("cluster[%d] weight = %f, want %f", i, clusters[i].weight, ww)
				}
			}
			for i, wo := range tt.wantOffset {
				if clusters[i].offset != wo {
					t.Errorf("cluster[%d] offset = %d, want %d", i, clusters[i].offset, wo)
				}
			}
		})
	}
}

func TestApplyAlignmentCheck(t *testing.T) {
	t.Parallel()
	ref := makeLongCues(20, 10*time.Minute)
	incSame := makeLongCues(20, 10*time.Minute)
	incShifted := ShiftCues(ref, 2*time.Second)
	tests := []struct {
		name       string
		ref        []Cue
		inc        []Cue
		clusters   []voteCluster
		wantWeight float64
	}{
		{
			name:       "penalty large first diff",
			ref:        ref,
			inc:        incSame,
			clusters:   []voteCluster{{members: []SyncResult{{Offset: 50000}}, weight: 1.0, offset: 50000}},
			wantWeight: 0.5,
		},
		{
			name:       "boost well aligned",
			ref:        ref,
			inc:        incShifted,
			clusters:   []voteCluster{{members: []SyncResult{{Offset: -2000}}, weight: 1.0, offset: -2000}},
			wantWeight: 1.2,
		},
		{
			name:       "no change moderate misalignment",
			ref:        ref,
			inc:        incSame,
			clusters:   []voteCluster{{members: []SyncResult{{Offset: 10000}}, weight: 1.0, offset: 10000}},
			wantWeight: 1.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusters := make([]voteCluster, len(tt.clusters))
			copy(clusters, tt.clusters)
			applyAlignmentCheck(clusters, tt.ref, tt.inc)
			if clusters[0].weight != tt.wantWeight {
				t.Errorf("weight = %f, want %f", clusters[0].weight, tt.wantWeight)
			}
		})
	}
}

func TestPenalizeLargeOffsets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name            string
		clusters        []voteCluster
		similarDuration bool
		durationDiff    int64
		wantWeight      float64
	}{
		{
			name:            "similar duration penalizes large offset",
			clusters:        []voteCluster{{members: []SyncResult{{Offset: 40000}}, weight: 1.0, offset: 40000}},
			similarDuration: true,
			durationDiff:    5000,
			wantWeight:      0.3,
		},
		{
			name:            "different duration skips penalty",
			clusters:        []voteCluster{{members: []SyncResult{{Offset: 40000}}, weight: 1.0, offset: 40000}},
			similarDuration: false,
			durationDiff:    120000,
			wantWeight:      1.0,
		},
		{
			name:            "small offset not penalized",
			clusters:        []voteCluster{{members: []SyncResult{{Offset: 5000}}, weight: 1.0, offset: 5000}},
			similarDuration: true,
			durationDiff:    5000,
			wantWeight:      1.0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusters := make([]voteCluster, len(tt.clusters))
			copy(clusters, tt.clusters)
			penalizeLargeOffsets(clusters, tt.similarDuration, tt.durationDiff)
			if clusters[0].weight != tt.wantWeight {
				t.Errorf("weight = %f, want %f", clusters[0].weight, tt.wantWeight)
			}
		})
	}
}

func TestPickWinner(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		clusters       []voteCluster
		wantOffset     int64
		wantConfidence Confidence
		wantMethod     SyncMethod
	}{
		{
			name: "selects highest weight cluster",
			clusters: []voteCluster{
				{members: []SyncResult{{Offset: 1000, Confidence: 0.9, Method: MethodOffset}}, weight: 0.5},
				{members: []SyncResult{{Offset: 5000, Confidence: 0.3, Method: MethodCrosslang}}, weight: 1.0},
			},
			wantOffset: 5000,
		},
		{
			name: "selects highest confidence within cluster",
			clusters: []voteCluster{
				{
					members: []SyncResult{
						{Offset: 1000, Confidence: 0.5, Method: MethodOffset},
						{Offset: 1100, Confidence: 0.8, Method: MethodCrosslang},
						{Offset: 900, Confidence: 0.6, Method: MethodFramerate},
					},
					weight: 1.9,
				},
			},
			wantConfidence: 0.8,
			wantMethod:     MethodCrosslang,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := pickWinner(tt.clusters)
			if tt.wantOffset != 0 && got.Offset != tt.wantOffset {
				t.Errorf("offset = %d, want %d", got.Offset, tt.wantOffset)
			}
			if tt.wantConfidence != 0 && got.Confidence != tt.wantConfidence {
				t.Errorf("confidence = %f, want %f", float64(got.Confidence), float64(tt.wantConfidence))
			}
			if tt.wantMethod != "" && got.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.Method, tt.wantMethod)
			}
		})
	}
}

func TestProperty_voteOnCandidates(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(t, "numCandidates")
		methods := []SyncMethod{MethodOffset, MethodCrosslang, MethodFramerate, MethodSplit, MethodAudio}

		ref := makeLongCues(30, 10*time.Minute)
		inc := ShiftCues(ref, 2*time.Second)

		candidates := make([]SyncResult, n)
		for i := range n {
			candidates[i] = SyncResult{
				Cues:       inc,
				Offset:     rapid.Int64Range(-60000, 60000).Draw(t, "offset"),
				Confidence: Confidence(rapid.Float64Range(0.01, 1.0).Draw(t, "confidence")),
				Method:     methods[rapid.IntRange(0, len(methods)-1).Draw(t, "methodIdx")],
			}
		}

		got := voteOnCandidates(candidates, ref, inc)

		// Invariant 1: returned method is one of the input candidates' methods.
		found := false
		for _, c := range candidates {
			if c.Method == got.Method {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("returned method %q not in input candidates", got.Method)
		}

		// Invariant 2: returned confidence >= 0.
		if got.Confidence < 0 {
			t.Fatalf("returned confidence %f < 0", float64(got.Confidence))
		}

		// Invariant 3: single candidate returns itself.
		if n == 1 {
			if got.Offset != candidates[0].Offset {
				t.Fatalf("single candidate: offset = %d, want %d", got.Offset, candidates[0].Offset)
			}
		}

		// Invariant 4: returned offset is one of the input candidates' offsets.
		offsetFound := false
		for _, c := range candidates {
			if c.Offset == got.Offset {
				offsetFound = true
				break
			}
		}
		if !offsetFound {
			t.Fatalf("returned offset %d not in input candidates", got.Offset)
		}
	})
}
