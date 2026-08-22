package subsync

import (
	"bytes"
	"log/slog"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
	"pgregory.net/rapid"
)

// --- audioSyncFromPCM ---

func TestAudioSyncFromPCM_too_few_cues(t *testing.T) {
	t.Parallel()
	cues := makeCues(4, 0, 2*time.Second)
	pcm := make([]int16, 8000)
	result := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})
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
	result := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})
	if result.Confidence != ConfidenceNone {
		t.Errorf("audioSyncFromPCM(zero frames) confidence = %f, want 0", float64(result.Confidence))
	}
}

func TestAudioSyncFromPCM_silence_completes(t *testing.T) {
	t.Parallel()
	cues := makeLongCues(10, 20*time.Second)
	pcm := make([]int16, 8000*30)
	result := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})
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
	result := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{DialogueCues: dialogueCues, IsASS: true})
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
	result := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})
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
	result := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})
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
	result := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})
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
		{
			// The signal is built from each cue's own times, so a list that
			// does not arrive in chronological order still marks every cue.
			name: "unordered_cues_are_all_marked",
			cues: []Cue{
				{Start: 30 * time.Millisecond, End: 60 * time.Millisecond, Text: "second"},
				{Start: 0, End: 20 * time.Millisecond, Text: "first"},
			},
			numFrames: 8,
			wantLen:   8,
			checks:    []check{{0, 1}, {1, 1}, {2, -1}, {3, 1}, {4, 1}, {5, 1}, {6, -1}, {7, -1}},
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

// --- synthetic audio oracle ---
//
// voiceBurstPCM lays five two-second voice-band bursts, one every four
// seconds, into otherwise silent 8kHz PCM of the requested frame count. The
// tones are whole numbers of Hz generated from the absolute sample index, so
// moving firstBurstSec by a whole second reproduces the identical waveform
// later in the buffer — which is what makes the displacement oracle below
// exact rather than approximate. Bursts past the end of the buffer are
// dropped, so a short buffer simply carries fewer of them.
func voiceBurstPCM(frames, firstBurstSec int) []int16 {
	const samplesPerFrame = ffmpeg.PCMSampleRate * frameMs / 1000
	pcm := make([]int16, frames*samplesPerFrame)
	for b := range 5 {
		start := (firstBurstSec + 4*b) * ffmpeg.PCMSampleRate
		for i := range 2 * ffmpeg.PCMSampleRate {
			if start+i >= len(pcm) {
				break
			}
			f := float64(start+i) * 2 * math.Pi / ffmpeg.PCMSampleRate
			pcm[start+i] = int16(3000*math.Sin(f*300) + 2500*math.Sin(f*950) + 2000*math.Sin(f*2100))
		}
	}
	return pcm
}

// dialogueCues returns five two-second cues on the same four-second spacing as
// voiceBurstPCM, displaced by lateMs.
func dialogueCues(firstCueSec int, lateMs int64) []Cue {
	cues := make([]Cue, 5)
	for b := range 5 {
		start := time.Duration(firstCueSec+4*b)*time.Second + time.Duration(lateMs)*time.Millisecond
		cues[b] = Cue{Start: start, End: start + 2*time.Second, Text: "Some dialogue line here."}
	}
	return cues
}

// audioOffsetToleranceMs absorbs the last bits of the parabolic peak refinement,
// which are not portable because Go may fuse a multiply and an add. Every
// behavior asserted against it moves the offset by at least 249ms.
const audioOffsetToleranceMs = 2

// Subtitles that run late against the audio are pulled back by the measured
// displacement, and the cues handed back carry that correction.
func TestAudioSyncFromPCM_recovers_a_known_displacement(t *testing.T) {
	t.Parallel()
	// Six minutes of audio, dialogue one second late against it.
	pcm := voiceBurstPCM(36000, 1)
	cues := dialogueCues(1, 1000)

	got := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})

	// The recovered correction is a little short of the full second because
	// the VAD holds each burst open for its overhang window, widening the
	// speech run past the cue that named it.
	if abs64(got.Offset-(-885)) > audioOffsetToleranceMs {
		t.Errorf("audioSyncFromPCM(cues 1000ms late).Offset = %d, want -885 (+/-%d)",
			got.Offset, audioOffsetToleranceMs)
	}
	if len(got.Cues) != len(cues) {
		t.Fatalf("audioSyncFromPCM(cues 1000ms late) returned %d cues, want %d", len(got.Cues), len(cues))
	}
	// The first cue starts at 2s and must come back 885ms earlier.
	if abs64(got.Cues[0].Start.Milliseconds()-1115) > audioOffsetToleranceMs {
		t.Errorf("audioSyncFromPCM(cues 1000ms late).Cues[0].Start = %v, want 1.115s (+/-%dms)",
			got.Cues[0].Start, audioOffsetToleranceMs)
	}
	if got.Confidence <= ConfidenceNone {
		t.Errorf("audioSyncFromPCM(cues 1000ms late).Confidence = %v, want > 0", got.Confidence)
	}
	// Confidence is the peak scaled by this method's ceiling, so it can never
	// exceed the ceiling itself.
	if got.Confidence > DefaultConfidenceCaps.Audio {
		t.Errorf("audioSyncFromPCM(cues 1000ms late).Confidence = %v, want <= the audio cap %v",
			got.Confidence, DefaultConfidenceCaps.Audio)
	}
	if got.Method != MethodAudio {
		t.Errorf("audioSyncFromPCM(cues 1000ms late).Method = %q, want %q", got.Method, MethodAudio)
	}
}

// The correction tracks the audio: holding the subtitles still and moving the
// speech seven seconds later moves the recovered offset seven seconds later
// too. Relating two runs pins the correlation to the signal it is given
// without depending on any single measured magnitude.
func TestAudioSyncFromPCM_offset_follows_the_audio(t *testing.T) {
	t.Parallel()
	cues := dialogueCues(1, 1000)

	early := audioSyncFromPCM(t.Context(), cues, voiceBurstPCM(36000, 6), AudioSyncHints{})
	late := audioSyncFromPCM(t.Context(), cues, voiceBurstPCM(36000, 13), AudioSyncHints{})

	if got := late.Offset - early.Offset; got != 7000 {
		t.Errorf("audioSyncFromPCM offset moved by %d when the audio moved 7s later (%d then %d), want 7000",
			got, early.Offset, late.Offset)
	}
	if early.Offset == 0 {
		t.Errorf("audioSyncFromPCM(audio 5s late).Offset = 0, want a nonzero correction")
	}
}

// An offset is discarded when it exceeds a tenth of the audio duration. Both
// rows recover the same 32885ms correction; they differ only in how much audio
// stands behind it, which is what moves the ceiling across that value.
func TestAudioSyncFromPCM_discards_an_offset_past_the_duration_ceiling(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		frames     int
		wantOffset int64
		wantConf   bool
	}{
		{name: "offset_exactly_at_the_ceiling_is_applied", frames: 32885, wantOffset: -32885, wantConf: true},
		{name: "offset_one_ms_past_the_ceiling_is_discarded", frames: 32884, wantOffset: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			pcm := voiceBurstPCM(tt.frames, 1)
			cues := dialogueCues(1, 33000)

			got := audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})

			if abs64(got.Offset-tt.wantOffset) > audioOffsetToleranceMs {
				t.Errorf("audioSyncFromPCM(%d frames, cues 33000ms late).Offset = %d, want %d (+/-%d)",
					tt.frames, got.Offset, tt.wantOffset, audioOffsetToleranceMs)
			}
			if tt.wantConf && got.Confidence <= ConfidenceNone {
				t.Errorf("audioSyncFromPCM(%d frames, cues 33000ms late).Confidence = %v, want > 0",
					tt.frames, got.Confidence)
			}
			if !tt.wantConf && got.Confidence != ConfidenceNone {
				t.Errorf("audioSyncFromPCM(%d frames, cues 33000ms late).Confidence = %v, want 0",
					tt.frames, got.Confidence)
			}
			if len(got.Cues) != len(cues) {
				t.Fatalf("audioSyncFromPCM(%d frames) returned %d cues, want %d",
					tt.frames, len(got.Cues), len(cues))
			}
			if !tt.wantConf && got.Cues[0].Start != cues[0].Start {
				t.Errorf("audioSyncFromPCM(%d frames, discarded).Cues[0].Start = %v, want the input %v",
					tt.frames, got.Cues[0].Start, cues[0].Start)
			}
		})
	}
}

// logField returns the value of a key=value attribute in one slog text line.
func logField(line, key string) (string, bool) {
	for field := range strings.FieldsSeq(line) {
		if value, ok := strings.CutPrefix(field, key+"="); ok {
			return value, true
		}
	}
	return "", false
}

// The completion line is the operator's record of a sync, so it reports the
// audio duration in seconds, how many dialogue cues drove the correlation, and
// which of the two VAD passes the result came from.
func TestAudioSyncFromPCM_logs_the_completed_sync(t *testing.T) {
	// Not parallel: this swaps the process-wide default logger.
	var buf bytes.Buffer
	restore := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(restore) })

	// Eleven seconds of audio: no other test in this package uses that
	// duration, so the reported duration identifies this run's line even when
	// a parallel sibling logs into the same buffer.
	pcm := voiceBurstPCM(1100, 1)
	cues := dialogueCues(1, 0)

	audioSyncFromPCM(t.Context(), cues, pcm, AudioSyncHints{})

	var found []string
	for line := range strings.SplitSeq(buf.String(), "\n") {
		if !strings.Contains(line, `msg="audio sync complete"`) {
			continue
		}
		if dur, ok := logField(line, "audio_dur_s"); ok && dur == "11" {
			found = append(found, line)
		}
	}
	if len(found) != 1 {
		t.Fatalf("audioSyncFromPCM(11s of audio) logged %d completion lines reporting audio_dur_s=11, want 1; captured:\n%s",
			len(found), buf.String())
	}
	if cueCount, _ := logField(found[0], "dialogue_cues"); cueCount != "5" {
		t.Errorf("audioSyncFromPCM(5 cues) logged dialogue_cues=%q, wanted 5, in %q", cueCount, found[0])
	}
	if signal, _ := logField(found[0], "signal"); signal != "gmm-precise" {
		t.Errorf("audioSyncFromPCM(agreeing passes) logged signal=%q, wanted gmm-precise, in %q", signal, found[0])
	}
}

// PBT: the marked frames are exactly the union of the cue windows clamped to
// the signal — no more (a cue cannot mark past its own end) and no less (an
// overlapping or out-of-order cue cannot be dropped). Cues are drawn from a
// window narrow enough that overlaps are the common case.
func TestBuildVADSubSignal_marks_the_union_of_the_cue_windows(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		numFrames := rapid.IntRange(1, 40).Draw(t, "numFrames")
		nCues := rapid.IntRange(0, 6).Draw(t, "nCues")
		cues := make([]Cue, nCues)
		for i := range nCues {
			startMs := rapid.Int64Range(-2*frameMs, int64(numFrames)*frameMs).Draw(t, "startMs")
			durMs := rapid.Int64Range(0, 8*frameMs).Draw(t, "durMs")
			cues[i] = Cue{
				Start: time.Duration(startMs) * time.Millisecond,
				End:   time.Duration(startMs+durMs) * time.Millisecond,
				Text:  "cue",
			}
		}

		got := buildVADSubSignal(cues, numFrames)

		want := make([]float64, numFrames)
		for i := range want {
			want[i] = -1
		}
		for _, c := range cues {
			from := min(max(int(c.Start.Milliseconds()/frameMs), 0), numFrames)
			to := min(max(int(c.End.Milliseconds()/frameMs), 0), numFrames)
			for f := from; f < to; f++ {
				want[f] = 1
			}
		}
		if !slices.Equal(got, want) {
			t.Fatalf("buildVADSubSignal(%v, %d) = %v, want %v", cues, numFrames, got, want)
		}
	})
}
