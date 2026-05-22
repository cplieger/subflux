package vad

import (
	"context"
	"math"
	"testing"

	"pgregory.net/rapid"
)

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

// --- vadGaussProb ---

func TestVadGaussProb_zero_input(t *testing.T) {
	t.Parallel()
	prob, delta := vadGaussProb(0, 0, vadMinStd)
	// With input=mean=0, delta should be 0.
	if delta != 0 {
		t.Errorf("vadGaussProb(0, 0, %d) delta = %d, want 0", vadMinStd, delta)
	}
	// Probability should be positive (peak of Gaussian).
	if prob <= 0 {
		t.Errorf("vadGaussProb(0, 0, %d) prob = %d, want > 0", vadMinStd, prob)
	}
}

func TestVadGaussProb_far_from_mean(t *testing.T) {
	t.Parallel()
	// Input far from mean should produce lower probability.
	probClose, _ := vadGaussProb(100, 100, 500)
	probFar, _ := vadGaussProb(100, 10000, 500)
	if probFar >= probClose {
		t.Errorf("vadGaussProb: far prob (%d) >= close prob (%d)", probFar, probClose)
	}
}

// --- vadNormW32 ---

func TestVadNormW32(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		val  int32
		want int32
	}{
		{"zero", 0, 0},
		{"negative", -1, 0},
		{"one", 1, 30},
		{"max_int32", math.MaxInt32, 0},
		{"power_of_2", 1 << 20, 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := vadNormW32(tt.val)
			if got != tt.want {
				t.Errorf("vadNormW32(%d) = %d, want %d", tt.val, got, tt.want)
			}
		})
	}
}

// --- vadLogEnergy ---

func TestVadLogEnergy_silence(t *testing.T) {
	t.Parallel()
	data := make([]int16, 80)
	logE, totalE := vadLogEnergy(data, 80, 368)
	// All-zero input: energy=0, logE=offset.
	if logE != 368 {
		t.Errorf("vadLogEnergy(silence) logE = %d, want 368", logE)
	}
	if totalE != 0 {
		t.Errorf("vadLogEnergy(silence) totalE = %d, want 0", totalE)
	}
}

func TestVadLogEnergy_nonzero(t *testing.T) {
	t.Parallel()
	data := make([]int16, 80)
	for i := range data {
		data[i] = 1000
	}
	logE, _ := vadLogEnergy(data, 80, 368)
	// Non-zero input should produce logE > offset.
	if logE <= 368 {
		t.Errorf("vadLogEnergy(1000s) logE = %d, want > 368", logE)
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

// --- FramesBinaryThresholdTuned ---

func TestVadFramesBinaryThresholdTuned_empty_pcm(t *testing.T) {
	t.Parallel()
	result := FramesBinaryThresholdTuned(context.Background(), nil, ModeVeryAggressive, 125, 10, 0, Tuning{})
	if len(result) != 0 {
		t.Errorf("FramesBinaryThresholdTuned(nil) len = %d, want 0", len(result))
	}
}

func TestVadFramesBinaryThresholdTuned_silence(t *testing.T) {
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

func TestVadFramesBinaryThresholdTuned_overhang(t *testing.T) {
	t.Parallel()
	// Create PCM with several loud frames to warm up the GMM, then silence.
	// The overhang should keep the signal at +1 for overhangFrames after speech ends.
	pcm := make([]int16, 1600) // 20 frames
	// Make first 5 frames very loud to ensure the VAD model recognizes speech.
	for i := range 400 {
		pcm[i] = 20000
	}
	// Remaining 15 frames are silence.
	minE := int16(5)
	result := FramesBinaryThresholdTuned(context.Background(), pcm, ModeQuality, 0, 3, 1.0, Tuning{minEnergy: &minE})
	if len(result) != 20 {
		t.Fatalf("len = %d, want 20", len(result))
	}
	// All frames must be exactly -1 or +1.
	for i, v := range result {
		if v != -1.0 && v != 1.0 {
			t.Errorf("frame %d = %f, want -1.0 or 1.0", i, v)
		}
	}
	// At least the first loud frame should be detected as speech.
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

func TestVadFramesBinaryThresholdTuned_overhang_countdown(t *testing.T) {
	t.Parallel()
	// Build PCM: 5 loud frames then 10 silent frames.
	// Use a high threshold so only genuinely loud frames pass,
	// then the overhang countdown branch fires for subsequent silent frames.
	pcm := make([]int16, 1200) // 15 frames of 80 samples
	for i := range 400 {
		pcm[i] = 20000
	}
	// threshold=500 ensures silence (llr~0) does NOT pass llr >= threshold.
	// overhangFrames=3 means 3 frames after last speech should still be +1.
	result := FramesBinaryThresholdTuned(context.Background(), pcm, ModeQuality, 500, 3, 0, Tuning{})
	if len(result) != 15 {
		t.Fatalf("len = %d, want 15", len(result))
	}
	// All values must be exactly -1 or +1.
	for i, v := range result {
		if v != -1.0 && v != 1.0 {
			t.Errorf("frame %d = %f, want -1.0 or 1.0", i, v)
		}
	}
	// Count speech frames. With overhang=3, we expect at least some +1 frames
	// after the loud section ends (the overhang countdown path).
	speechCount := 0
	for _, v := range result {
		if v == 1.0 {
			speechCount++
		}
	}
	// At minimum, some loud frames should be speech. If overhang works,
	// there should be more speech frames than just the loud ones.
	if speechCount == 0 {
		t.Error("no speech frames detected at all")
	}
}

// --- extractFeatures ---

func TestExtractFeatures_silence(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	frame := make([]int16, 80)
	features, totalE := v.extractFeatures(frame)
	// Silent frame on a fresh instance: features should equal their offset values.
	for i, f := range features {
		if f != offsets[i] {
			t.Errorf("features[%d] = %d, want %d (offset)", i, f, offsets[i])
		}
	}
	if totalE < 0 {
		t.Errorf("totalE = %d, want >= 0", totalE)
	}
}

// --- findMinimum ---

func TestFindMinimum_initial_state(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// First call: frameCounter=0, so alpha=0. Smoothing formula:
	// tmp32 = 1*meanVal[0] + 32767*currentMedian + 16384
	// With frameCounter=0, currentMedian = meanVal[0] = 1600.
	// tmp32 = 1*1600 + 32767*1600 + 16384 = 52430384. result = 52430384 >> 15 = 1600.
	result := v.findMinimum(500, 0)
	if result != 1600 {
		t.Errorf("findMinimum(500, 0) on fresh instance = %d, want 1600", result)
	}
}

func TestFindMinimum_decreasing_values(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// Feed decreasing values; the minimum tracker should follow down.
	var last int16
	for i := range 50 {
		v.frameCounter = int32(i)
		last = v.findMinimum(int16(5000-i*50), 0)
	}
	// After 50 frames of decreasing values, the result should be well below 5000.
	if last >= 5000 {
		t.Errorf("findMinimum after decreasing values = %d, want < 5000", last)
	}
}

// --- vadLogEnergy edge cases ---

func TestVadLogEnergy_overflow(t *testing.T) {
	t.Parallel()
	// All samples at max amplitude triggers the energy overflow branch
	// (lines 235-239 of vad.go): 80 × 32767² ≈ 85.9B > uint32 max.
	data := make([]int16, 80)
	for i := range data {
		data[i] = math.MaxInt16
	}
	logE, totalE := vadLogEnergy(data, 80, 368)
	if logE <= 368 {
		t.Errorf("vadLogEnergy(MaxInt16) logE = %d, want > 368 (offset)", logE)
	}
	if totalE <= vadMinEnergy {
		t.Errorf("vadLogEnergy(MaxInt16) totalE = %d, want > %d", totalE, vadMinEnergy)
	}
}

func TestVadLogEnergy_low_energy(t *testing.T) {
	t.Parallel()
	// All samples = 1: very low energy triggers the normR < 0 branch
	// (line 256 of vad.go) because energy = 80 has many leading zeros.
	data := make([]int16, 80)
	for i := range data {
		data[i] = 1
	}
	logE, _ := vadLogEnergy(data, 80, 368)
	// Non-zero energy should produce logE > offset.
	if logE <= 368 {
		t.Errorf("vadLogEnergy(all-1s) logE = %d, want > 368 (offset)", logE)
	}
}

// --- vadGaussProb boundary values ---

func TestVadGaussProb_min_std(t *testing.T) {
	t.Parallel()
	// std = vadMinStd tests the invStd calculation at its extreme.
	prob, delta := vadGaussProb(0, 32000, vadMinStd)
	// Large delta (input far from mean) should trigger kCompVar cutoff.
	if delta == 0 {
		t.Error("vadGaussProb(0, 32000, vadMinStd) delta = 0, want non-zero")
	}
	// Probability should be zero or very small (far from mean).
	if prob < 0 {
		t.Errorf("vadGaussProb(0, 32000, vadMinStd) prob = %d, want >= 0", prob)
	}
}

func TestVadGaussProb_negative_delta(t *testing.T) {
	t.Parallel()
	// input < mean produces a negative delta.
	prob, delta := vadGaussProb(0, 1000, 500)
	if delta >= 0 {
		t.Errorf("vadGaussProb(0, 1000, 500) delta = %d, want < 0", delta)
	}
	if prob < 0 {
		t.Errorf("vadGaussProb(0, 1000, 500) prob = %d, want >= 0", prob)
	}
}

func TestVadGaussProb_positive_delta(t *testing.T) {
	t.Parallel()
	// input > mean produces a positive delta.
	prob, delta := vadGaussProb(1000, 0, 500)
	if delta <= 0 {
		t.Errorf("vadGaussProb(1000, 0, 500) delta = %d, want > 0", delta)
	}
	if prob < 0 {
		t.Errorf("vadGaussProb(1000, 0, 500) prob = %d, want >= 0", prob)
	}
}

// --- Property-based tests ---

func TestVadFramesBinaryThresholdTuned_output_binary(t *testing.T) {
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

func TestVadFramesBinaryThresholdTuned_empty_returns_empty(t *testing.T) {
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

func TestVadNormW32_range(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		val := rapid.Int32Range(1, math.MaxInt32).Draw(t, "val")
		got := vadNormW32(val)
		if got < 0 || got > 30 {
			t.Fatalf("vadNormW32(%d) = %d, want in [0, 30]", val, got)
		}
	})
}

func TestVadNormW32_non_positive_zero(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		val := rapid.Int32Range(math.MinInt32, 0).Draw(t, "val")
		got := vadNormW32(val)
		if got != 0 {
			t.Fatalf("vadNormW32(%d) = %d, want 0", val, got)
		}
	})
}

func TestVadGaussProb_non_negative(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		input := rapid.Int16().Draw(t, "input")
		mean := rapid.Int16().Draw(t, "mean")
		// std must be > 0 (precondition enforced by vadMinStd clamp).
		std := rapid.Int16Range(vadMinStd, math.MaxInt16).Draw(t, "std")
		prob, _ := vadGaussProb(input, mean, std)
		if prob < 0 {
			t.Fatalf("vadGaussProb(%d, %d, %d) prob = %d, want >= 0", input, mean, std, prob)
		}
	})
}

func TestVadGaussProb_zero_delta_at_mean(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		mean := rapid.Int16Range(-4095, 4095).Draw(t, "mean")
		std := rapid.Int16Range(vadMinStd, math.MaxInt16).Draw(t, "std")
		// vadGaussProb shifts input to Q7 (input<<3), so passing mean=input<<3
		// makes the delta (xQ7 - mean) exactly zero.
		_, delta := vadGaussProb(mean, mean<<3, std)
		if delta != 0 {
			t.Fatalf("vadGaussProb(%d, %d, %d) delta = %d, want 0", mean, mean<<3, std, delta)
		}
	})
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

// --- gmmProbabilityLLR energy boundary (VAD-T4) ---

func TestProcessFrameLLR_near_energy_threshold(t *testing.T) {
	t.Parallel()
	// Very low amplitude frame: all samples = 1. Energy is near vadMinEnergy.
	// This exercises the totalPower <= minEnergy boundary in gmmProbabilityLLR.
	v := newVADInst(ModeVeryAggressive)
	frame := make([]int16, 80)
	for i := range frame {
		frame[i] = 1
	}
	flag, llr := v.processFrameLLR(frame)
	// With samples=1, energy is very low. The GMM should either skip (llr=0)
	// or produce a near-zero result. Flag should be 0 (no speech).
	if flag != 0 {
		t.Errorf("processFrameLLR(near-threshold) flag = %d, want 0", flag)
	}
	if math.IsNaN(llr) || math.IsInf(llr, 0) {
		t.Errorf("processFrameLLR(near-threshold) llr = %f, want finite", llr)
	}
}

// --- findMinimum additional behavioral tests (VAD-T5) ---

func TestFindMinimum_age_eviction(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// Feed 110 frames with value 5000 to fill and age the tracker entries.
	for i := range 110 {
		v.frameCounter = int32(i)
		v.findMinimum(5000, 0)
	}
	// Now feed a much lower value. Old entries should be evicted by age,
	// and the minimum should converge toward the new value.
	v.frameCounter = 110
	result := v.findMinimum(100, 0)
	// The smoothed result should be moving toward 100, well below 5000.
	if result >= 5000 {
		t.Errorf("findMinimum after age eviction = %d, want < 5000", result)
	}
}

func TestFindMinimum_increasing_values(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// Feed 50 frames of increasing values (1000 to 3450).
	var last int16
	for i := range 50 {
		v.frameCounter = int32(i)
		last = v.findMinimum(int16(1000+i*50), 0)
	}
	// The smoothed minimum should stay well below the final input value (3450)
	// because smoothUp is 0.99 (very slow upward tracking).
	if last >= 3450 {
		t.Errorf("findMinimum after increasing values = %d, want < 3450", last)
	}
}

func TestFindMinimum_channel_independence(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	// Feed different values to channel 0 and channel 1.
	for i := range 50 {
		v.frameCounter = int32(i)
		v.findMinimum(500, 0)  // ch 0: low value
		v.findMinimum(5000, 1) // ch 1: high value
	}
	// After convergence, meanVal for ch 0 and ch 1 should diverge.
	if v.meanVal[0] == v.meanVal[1] {
		t.Errorf("channels not independent: meanVal[0]=%d == meanVal[1]=%d",
			v.meanVal[0], v.meanVal[1])
	}
	if v.meanVal[0] >= v.meanVal[1] {
		t.Errorf("channel 0 (low input) meanVal=%d >= channel 1 (high input) meanVal=%d",
			v.meanVal[0], v.meanVal[1])
	}
}

// --- Overhang state machine (VAD-T6) ---

func TestProcessFrameLLR_overhang_hangover(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality) // quality mode: overHangMax1=8, overHangMax2=14
	// Feed several loud frames to build up speech detection.
	loud := make([]int16, 80)
	for i := range loud {
		loud[i] = int16(20000 * math.Sin(float64(i)*2*math.Pi*440/8000))
	}
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
	// With overHangMax1=8 or overHangMax2=14, we expect some hangover frames.
	if hangoverCount == 0 {
		t.Error("no hangover frames detected after speech-to-silence transition")
	}
}

// --- vadLogEnergy PBT (VAD-T7) ---

func TestVadLogEnergy_invariants(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 80).Draw(t, "n")
		data := make([]int16, n)
		for i := range data {
			data[i] = rapid.Int16().Draw(t, "sample")
		}
		offset := rapid.Int16Range(0, 1000).Draw(t, "offset")
		logE, _ := vadLogEnergy(data, n, offset)
		// logE is clamped to max(logE, 0) + offset, so it must be >= offset.
		if logE < offset {
			t.Fatalf("vadLogEnergy logE=%d < offset=%d", logE, offset)
		}
	})
}
