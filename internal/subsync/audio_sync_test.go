package subsync

import (
	"context"
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

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
		checks    []check
		numFrames int
		wantLen   int
		wantNil   bool
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
