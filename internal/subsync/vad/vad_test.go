package vad

import (
	"context"
	"math"
	"testing"
	"time"

	"pgregory.net/rapid"
)

// loudFrame returns one 80-sample 440Hz sine frame at amplitude 20000.
func loudFrame() []int16 {
	f := make([]int16, 80)
	for i := range f {
		f[i] = int16(20000 * math.Sin(float64(i)*2*math.Pi*440/8000))
	}
	return f
}

// cancelAfterFirstCheckCtx is a context whose Err() returns nil on the first
// call and context.Canceled on every call thereafter. It pins how often
// FramesBinaryThresholdTuned polls the cancellation guard.
type cancelAfterFirstCheckCtx struct{ n int }

func (c *cancelAfterFirstCheckCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *cancelAfterFirstCheckCtx) Done() <-chan struct{}       { return nil }
func (c *cancelAfterFirstCheckCtx) Value(any) any               { return nil }
func (c *cancelAfterFirstCheckCtx) Err() error {
	c.n++
	if c.n == 1 {
		return nil
	}
	return context.Canceled
}

// --- newVADInst ---

func TestNewVADInst_default_mode(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	if v.localThresh != 94 {
		t.Errorf("newVADInst(3).localThresh = %d, want 94", v.localThresh)
	}
	if v.globalThresh != 1100 {
		t.Errorf("newVADInst(3).globalThresh = %d, want 1100", v.globalThresh)
	}
	if v.overHangMax1 != 6 {
		t.Errorf("newVADInst(3).overHangMax1 = %d, want 6", v.overHangMax1)
	}
	if v.overHangMax2 != 9 {
		t.Errorf("newVADInst(3).overHangMax2 = %d, want 9", v.overHangMax2)
	}
	if v.noiseUpdate != noiseUpdateConst {
		t.Errorf("newVADInst(3).noiseUpdate = %d, want %d", v.noiseUpdate, noiseUpdateConst)
	}
	if v.speechUpdate != speechUpdateConst {
		t.Errorf("newVADInst(3).speechUpdate = %d, want %d", v.speechUpdate, speechUpdateConst)
	}
}

func TestNewVADInst_all_modes(t *testing.T) {
	t.Parallel()
	names := [4]string{"quality", "low_bitrate", "aggressive", "very_aggressive"}
	for mode := range Mode(4) {
		t.Run(names[mode], func(t *testing.T) {
			t.Parallel()
			v := newVADInst(mode)
			want := vadModes[int(mode)]
			if v.localThresh != want.local {
				t.Errorf("localThresh = %d, want %d", v.localThresh, want.local)
			}
			if v.globalThresh != want.global {
				t.Errorf("globalThresh = %d, want %d", v.globalThresh, want.global)
			}
			if v.overHangMax1 != want.oh1 {
				t.Errorf("overHangMax1 = %d, want %d", v.overHangMax1, want.oh1)
			}
			if v.overHangMax2 != want.oh2 {
				t.Errorf("overHangMax2 = %d, want %d", v.overHangMax2, want.oh2)
			}
		})
	}
}

func TestNewVADInst_out_of_range_mode_defaults_to_3(t *testing.T) {
	t.Parallel()
	v := newVADInst(Mode(-1))
	if v.localThresh != vadModes[3].local {
		t.Errorf("newVADInst(-1).localThresh = %d, want %d", v.localThresh, vadModes[3].local)
	}
	v2 := newVADInst(Mode(99))
	if v2.localThresh != vadModes[3].local {
		t.Errorf("newVADInst(99).localThresh = %d, want %d", v2.localThresh, vadModes[3].local)
	}
}

func TestNewVADInst_initial_means_copied(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	for i := range vadTableSize {
		if v.noiseMeans[i] != initNoiseMeans[i] {
			t.Errorf("noiseMeans[%d] = %d, want %d", i, v.noiseMeans[i], initNoiseMeans[i])
		}
		if v.speechMeans[i] != initSpeechMeans[i] {
			t.Errorf("speechMeans[%d] = %d, want %d", i, v.speechMeans[i], initSpeechMeans[i])
		}
	}
}

func TestNewVADInst_lowValue_initialized(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	for i := range v.lowValue {
		if v.lowValue[i] != 10000 {
			t.Errorf("lowValue[%d] = %d, want 10000", i, v.lowValue[i])
		}
	}
}

func TestNewVADInst_meanVal_initialized(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	for i := range v.meanVal {
		if v.meanVal[i] != 1600 {
			t.Errorf("meanVal[%d] = %d, want 1600", i, v.meanVal[i])
		}
	}
}

// --- newVADInstAdapt ---

func TestNewVADInstAdapt_default_scale(t *testing.T) {
	t.Parallel()
	v := newVADInstAdapt(ModeVeryAggressive, 1.0)
	if v.noiseUpdate != noiseUpdateConst {
		t.Errorf("adaptScale=1.0: noiseUpdate = %d, want %d", v.noiseUpdate, noiseUpdateConst)
	}
	if v.speechUpdate != speechUpdateConst {
		t.Errorf("adaptScale=1.0: speechUpdate = %d, want %d", v.speechUpdate, speechUpdateConst)
	}
}

func TestNewVADInstAdapt_double_speed(t *testing.T) {
	t.Parallel()
	v := newVADInstAdapt(ModeVeryAggressive, 2.0)
	wantNoise := int16(float64(noiseUpdateConst) * 2.0)
	wantSpeech := int16(float64(speechUpdateConst) * 2.0)
	if v.noiseUpdate != wantNoise {
		t.Errorf("adaptScale=2.0: noiseUpdate = %d, want %d", v.noiseUpdate, wantNoise)
	}
	if v.speechUpdate != wantSpeech {
		t.Errorf("adaptScale=2.0: speechUpdate = %d, want %d", v.speechUpdate, wantSpeech)
	}
}

func TestNewVADInstAdapt_zero_scale_clamps_to_1(t *testing.T) {
	t.Parallel()
	v := newVADInstAdapt(ModeVeryAggressive, 0)
	if v.noiseUpdate < 1 {
		t.Errorf("adaptScale=0: noiseUpdate = %d, want >= 1", v.noiseUpdate)
	}
	if v.speechUpdate < 1 {
		t.Errorf("adaptScale=0: speechUpdate = %d, want >= 1", v.speechUpdate)
	}
}

func TestNewVADInstAdapt_negative_scale_clamps(t *testing.T) {
	t.Parallel()
	v := newVADInstAdapt(ModeVeryAggressive, -5.0)
	if v.noiseUpdate < 1 {
		t.Errorf("adaptScale=-5: noiseUpdate = %d, want >= 1", v.noiseUpdate)
	}
}

func TestNewVADInstAdapt_large_scale_clamped(t *testing.T) {
	t.Parallel()
	v := newVADInstAdapt(ModeVeryAggressive, 100.0)
	// Clamped to 32767/speechUpdateConst ≈ 4.9995.
	maxScale := float64(32767) / float64(speechUpdateConst)
	wantNoise := int16(float64(noiseUpdateConst) * maxScale)
	if v.noiseUpdate != wantNoise {
		t.Errorf("adaptScale=100 (clamped): noiseUpdate = %d, want %d", v.noiseUpdate, wantNoise)
	}
	// speechUpdate must not overflow int16.
	wantSpeech := int16(float64(speechUpdateConst) * maxScale)
	if v.speechUpdate != wantSpeech {
		t.Errorf("adaptScale=100 (clamped): speechUpdate = %d, want %d", v.speechUpdate, wantSpeech)
	}
	if v.speechUpdate < 0 {
		t.Errorf("speechUpdate overflowed to negative: %d", v.speechUpdate)
	}
}

// --- newVADInstTuned ---

func TestNewVADInstTuned_custom_weights(t *testing.T) {
	t.Parallel()
	custom := [vadNumCh]int16{1, 2, 3, 4, 5, 6}
	v := newVADInstTuned(ModeVeryAggressive, 1.0, Tuning{specWeights: &custom})
	if v.specWeights != custom {
		t.Errorf("custom specWeights not applied: got %v, want %v", v.specWeights, custom)
	}
}

func TestNewVADInstTuned_custom_minEnergy(t *testing.T) {
	t.Parallel()
	minE := int16(50)
	v := newVADInstTuned(ModeVeryAggressive, 1.0, Tuning{minEnergy: &minE})
	if v.minEnergy != 50 {
		t.Errorf("custom minEnergy not applied: got %d, want 50", v.minEnergy)
	}
}

func TestNewVADInstTuned_nil_tuning_uses_defaults(t *testing.T) {
	t.Parallel()
	v := newVADInstTuned(ModeVeryAggressive, 1.0, Tuning{})
	if v.specWeights != specWeight {
		t.Errorf("nil tuning should use default specWeights")
	}
	if v.minEnergy != vadMinEnergy {
		t.Errorf("nil tuning should use default minEnergy: got %d, want %d", v.minEnergy, vadMinEnergy)
	}
}

// --- processFrameLLR ---

func TestProcessFrameLLR_silence(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	frame := make([]int16, 80)
	flag, llr := v.processFrameLLR(frame)
	// Silent frame should not be classified as speech.
	if flag != 0 {
		t.Errorf("processFrameLLR(silence) flag = %d, want 0", flag)
	}
	// Silent frame: totalPower <= minEnergy, so GMM is skipped and llr = 0.
	if llr != 0 {
		t.Errorf("processFrameLLR(silence) llr = %f, want 0", llr)
	}
}

func TestProcessFrameLLR_loud_signal(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality) // quality mode (most sensitive)
	// Generate a loud 440Hz sine signal.
	frame := make([]int16, 80)
	for i := range frame {
		frame[i] = int16(10000 * math.Sin(float64(i)*2*math.Pi*440/8000))
	}
	flag, llr := v.processFrameLLR(frame)
	// A loud signal in the most sensitive mode should be detected as speech.
	if flag != 1 {
		t.Errorf("processFrameLLR(loud, mode=0) flag = %d, want 1 (speech)", flag)
	}
	// LLR should be positive (speech-like features).
	if llr <= 0 {
		t.Errorf("processFrameLLR(loud, mode=0) llr = %f, want > 0", llr)
	}
}

// TestProcessFrameLLR_near_energy_threshold exercises the totalPower <=
// minEnergy boundary: a very low-amplitude frame (all samples = 1) has energy
// near vadMinEnergy, so the GMM is skipped or returns near-zero and the frame
// is not classified as speech.
func TestProcessFrameLLR_near_energy_threshold(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	frame := make([]int16, 80)
	for i := range frame {
		frame[i] = 1
	}
	flag, llr := v.processFrameLLR(frame)
	if flag != 0 {
		t.Errorf("processFrameLLR(near-threshold) flag = %d, want 0", flag)
	}
	if math.IsNaN(llr) || math.IsInf(llr, 0) {
		t.Errorf("processFrameLLR(near-threshold) llr = %f, want finite", llr)
	}
}

// TestProcessFrameLLR_overhang_hangover verifies the speech-to-silence
// hangover: after sustained speech the overhang keeps the decision positive
// for several silent frames before it expires.
func TestProcessFrameLLR_overhang_hangover(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality) // quality mode: overHangMax1=8, overHangMax2=14
	loud := loudFrame()
	for range 20 {
		v.processFrameLLR(loud)
	}
	// After 20 loud frames, numOfSpeech should be > 0.
	if v.numOfSpeech == 0 {
		t.Fatal("numOfSpeech = 0 after 20 loud frames, want > 0")
	}
	// Now feed silent frames. The overhang should keep flag > 0 for some frames.
	silent := make([]int16, 80)
	hangoverCount := 0
	for range 20 {
		flag, _ := v.processFrameLLR(silent)
		if flag > 0 {
			hangoverCount++
		}
	}
	if hangoverCount == 0 {
		t.Error("no hangover frames detected after speech-to-silence transition")
	}
}

func TestProcessFrameLLR_never_panics(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		frame := make([]int16, 80)
		for i := range frame {
			frame[i] = rapid.Int16().Draw(t, "sample")
		}
		mode := Mode(rapid.IntRange(0, 3).Draw(t, "mode"))
		v := newVADInst(mode)
		flag, llr := v.processFrameLLR(frame)
		if flag < 0 || flag > 1 {
			t.Fatalf("flag = %d, want 0 or 1", flag)
		}
		if math.IsNaN(llr) || math.IsInf(llr, 0) {
			t.Fatalf("llr = %f, want finite", llr)
		}
	})
}

// --- FramesBinaryThresholdTuned ---

func TestFramesBinaryThresholdTuned_empty_pcm(t *testing.T) {
	t.Parallel()
	result := FramesBinaryThresholdTuned(context.Background(), nil, ModeVeryAggressive, 125, 10, 0, Tuning{})
	if len(result) != 0 {
		t.Errorf("FramesBinaryThresholdTuned(nil) len = %d, want 0", len(result))
	}
}

func TestFramesBinaryThresholdTuned_silence(t *testing.T) {
	t.Parallel()
	// 10 frames of silence (800 samples at 80 samples/frame).
	pcm := make([]int16, 800)
	result := FramesBinaryThresholdTuned(context.Background(), pcm, ModeVeryAggressive, 125, 10, 0, Tuning{})
	if len(result) != 10 {
		t.Fatalf("FramesBinaryThresholdTuned(silence) len = %d, want 10", len(result))
	}
	// All frames should be -1 (silence).
	for i, v := range result {
		if v != -1.0 {
			t.Errorf("frame %d = %f, want -1.0 (silence)", i, v)
		}
	}
}

func TestFramesBinaryThresholdTuned_detects_loud_warmup(t *testing.T) {
	t.Parallel()
	// 5 loud frames warm up the GMM, then 15 silent frames. The output must be
	// strictly bipolar and at least one loud frame must read as speech.
	pcm := make([]int16, 1600) // 20 frames
	for i := range 400 {
		pcm[i] = 20000
	}
	minE := int16(5)
	result := FramesBinaryThresholdTuned(context.Background(), pcm, ModeQuality, 0, 3, 1.0, Tuning{minEnergy: &minE})
	if len(result) != 20 {
		t.Fatalf("len = %d, want 20", len(result))
	}
	for i, v := range result {
		if v != -1.0 && v != 1.0 {
			t.Errorf("frame %d = %f, want -1.0 or 1.0", i, v)
		}
	}
	speechCount := 0
	for _, v := range result[:5] {
		if v == 1.0 {
			speechCount++
		}
	}
	if speechCount == 0 {
		t.Error("no speech detected in any of the 5 loud frames")
	}
}

// TestFramesBinaryThresholdTuned_cancelled_context_returns_early verifies the
// cancellation guard fires on the first frame: a pre-cancelled context yields
// no results at all.
func TestFramesBinaryThresholdTuned_cancelled_context_returns_early(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pcm := make([]int16, 80*4) // 4 frames
	got := FramesBinaryThresholdTuned(ctx, pcm, ModeVeryAggressive, 125, 10, 0, Tuning{})
	if len(got) != 0 {
		t.Errorf("cancelled ctx: len(result) = %d, want 0", len(got))
	}
}

// TestFramesBinaryThresholdTuned_cancellation_polled_every_1000_frames pins the
// cancellation poll cadence. The context reports OK on its first poll (frame 0)
// then cancelled, so processing stops at the second poll (frame 1000), yielding
// exactly 1000 results.
func TestFramesBinaryThresholdTuned_cancellation_polled_every_1000_frames(t *testing.T) {
	t.Parallel()
	pcm := make([]int16, 80*1001) // 1001 frames, i in [0,1000]
	got := FramesBinaryThresholdTuned(&cancelAfterFirstCheckCtx{}, pcm, ModeVeryAggressive, 125, 10, 0, Tuning{})
	if len(got) != 1000 {
		t.Errorf("len(result) = %d, want 1000", len(got))
	}
}

// TestFramesBinaryThresholdTuned_frame_windowing verifies each frame is scored
// from its own 80-sample window. Frame 0 is loud and frame 1 is silent, so with
// no overhang frame 1 must be silence (it must not bleed in frame 0's window).
func TestFramesBinaryThresholdTuned_frame_windowing(t *testing.T) {
	t.Parallel()
	pcm := make([]int16, 160)
	copy(pcm, loudFrame()) // frame 0 loud; frame 1 stays silent
	got := FramesBinaryThresholdTuned(context.Background(), pcm, ModeQuality, 750, 0, 1.0, Tuning{})
	if len(got) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(got))
	}
	if got[1] != -1.0 {
		t.Errorf("result[1] = %v, want -1.0 (silent frame scored on its own window)", got[1])
	}
}

// TestFramesBinaryThresholdTuned_overhang_counts_down verifies the overhang
// extends speech for a bounded number of frames after speech ends and then
// expires: with a 2-frame overhang, frame 4 (well past the loud frame) is
// silence again.
func TestFramesBinaryThresholdTuned_overhang_counts_down(t *testing.T) {
	t.Parallel()
	pcm := make([]int16, 80*7) // loud frame 0, silence frames 1..6
	copy(pcm, loudFrame())
	got := FramesBinaryThresholdTuned(context.Background(), pcm, ModeQuality, 300, 2, 1.0, Tuning{})
	if len(got) != 7 {
		t.Fatalf("len(result) = %d, want 7", len(got))
	}
	if got[4] != -1.0 {
		t.Errorf("result[4] = %v, want -1.0 (overhang counted down to 0)", got[4])
	}
}

func TestFramesBinaryThresholdTuned_output_binary(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		nFrames := rapid.IntRange(1, 50).Draw(t, "nFrames")
		pcm := make([]int16, nFrames*80)
		for i := range pcm {
			pcm[i] = rapid.Int16().Draw(t, "sample")
		}
		mode := Mode(rapid.IntRange(0, 3).Draw(t, "mode"))
		result := FramesBinaryThresholdTuned(context.Background(), pcm, mode, 125, 10, 0, Tuning{})
		// Output length must equal number of frames.
		if len(result) != nFrames {
			t.Fatalf("len(result) = %d, want %d", len(result), nFrames)
		}
		// Every output value must be exactly -1.0 or +1.0.
		for i, v := range result {
			if v != -1.0 && v != 1.0 {
				t.Fatalf("frame %d = %f, want -1.0 or 1.0", i, v)
			}
		}
	})
}

func TestFramesBinaryThresholdTuned_empty_returns_empty(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Any PCM shorter than 80 samples produces zero frames.
		n := rapid.IntRange(0, 79).Draw(t, "n")
		pcm := make([]int16, n)
		result := FramesBinaryThresholdTuned(context.Background(), pcm, ModeVeryAggressive, 125, 10, 0, Tuning{})
		if len(result) != 0 {
			t.Fatalf("len(result) = %d for %d samples, want 0", len(result), n)
		}
	})
}
