package vad

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

// --- vadGaussProb ---

// TestVadGaussProb_peak_value pins the exact fixed-point probability at the
// Gaussian peak (input == mean), where the delta is 0 and prob is invStd*1024.
func TestVadGaussProb_peak_value(t *testing.T) {
	t.Parallel()
	prob, delta := vadGaussProb(0, 0, 384)
	if prob != 349184 {
		t.Errorf("vadGaussProb(0,0,384) prob = %d, want 349184", prob)
	}
	if delta != 0 {
		t.Errorf("vadGaussProb(0,0,384) delta = %d, want 0", delta)
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

// --- gmmProbabilityLLR: decision behavior ---
//
// These pin the speech decision (flag / sumLLR) and the overhang state machine.
// The adaptive model-update reference vectors live in
// vad_classifier_modelupdate_test.go.

// TestGMMProbabilityLLR_energy_gate_closed_at_boundary verifies the energy gate
// opens only for totalPower strictly above minEnergy: at equality the whole GMM
// block is skipped, so there is no decision and the model is untouched.
func TestGMMProbabilityLLR_energy_gate_closed_at_boundary(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	flag, llr := v.gmmProbabilityLLR(featSpeech, vadMinEnergy)
	if flag != 0 {
		t.Errorf("flag = %d, want 0 (energy gate closed at boundary)", flag)
	}
	if llr != 0 {
		t.Errorf("llr = %v, want 0", llr)
	}
	if v.frameCounter != 0 {
		t.Errorf("frameCounter = %d, want 0 (GMM block must be skipped)", v.frameCounter)
	}
	if v.noiseMeans != initNoiseMeans {
		t.Errorf("noiseMeans mutated at energy boundary: %v", v.noiseMeans)
	}
}

// TestGMMProbabilityLLR_per_channel_llr_scaled_by_4 verifies the per-channel
// trigger compares llr*4 against localThresh. With the global trigger disabled,
// the speech frame's max per-channel llr (25 -> 100) clears localThresh 50.
func TestGMMProbabilityLLR_per_channel_llr_scaled_by_4(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	v.globalThresh = 32767 // disable the global-sum trigger
	v.localThresh = 50
	flag, _ := v.gmmProbabilityLLR(featSpeech, 100)
	if flag != 1 {
		t.Errorf("flag = %d, want 1 (llr*4=100 > localThresh 50)", flag)
	}
}

// TestGMMProbabilityLLR_per_channel_negative_threshold verifies the per-channel
// trigger fires whenever any channel's llr*4 (always >= 0) exceeds localThresh:
// at localThresh -1 every channel clears it.
func TestGMMProbabilityLLR_per_channel_negative_threshold(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	v.globalThresh = 32767
	v.localThresh = -1
	flag, _ := v.gmmProbabilityLLR(featSpeech, 100)
	if flag != 1 {
		t.Errorf("flag = %d, want 1 (all llr*4 > -1)", flag)
	}
}

// TestGMMProbabilityLLR_per_channel_strict_boundary verifies the per-channel
// trigger is strict (llr*4 > localThresh, not >=): with localThresh set exactly
// to the maximum per-channel llr*4 (100), no channel strictly exceeds it.
func TestGMMProbabilityLLR_per_channel_strict_boundary(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	v.globalThresh = 32767 // disable the global-sum trigger
	v.localThresh = 100    // == max per-channel llr*4, so `>` is false for all
	flag, _ := v.gmmProbabilityLLR(featSpeech, 100)
	if flag != 0 {
		t.Errorf("flag = %d, want 0 (no channel's llr*4 strictly exceeds localThresh 100)", flag)
	}
}

// TestGMMProbabilityLLR_global_threshold_at_equality verifies the global
// trigger is inclusive (sumLLR >= globalThresh): with the per-channel trigger
// disabled and globalThresh equal to the frame's sumLLR, the flag fires.
func TestGMMProbabilityLLR_global_threshold_at_equality(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeQuality)
	v.localThresh = 32767
	v.globalThresh = 1470 // == sumLLR for featSpeech
	flag, llr := v.gmmProbabilityLLR(featSpeech, 100)
	if llr != 1470 {
		t.Fatalf("precondition: sumLLR = %v, want 1470", llr)
	}
	if flag != 1 {
		t.Errorf("flag = %d, want 1 (sumLLR >= globalThresh at equality)", flag)
	}
}

// TestGMMProbabilityLLR_overhang_when_not_speech verifies the non-speech
// hangover branch: with the energy gate closed and a preset overhang, the
// decision is held positive as 2+overHang, the overhang counts down by one,
// numOfSpeech resets, and the model (frameCounter) is untouched.
func TestGMMProbabilityLLR_overhang_when_not_speech(t *testing.T) {
	t.Parallel()
	v := newVADInst(ModeVeryAggressive)
	v.overHang = 5
	flag, _ := v.gmmProbabilityLLR(featUniform1000, 0)
	if flag != 7 {
		t.Errorf("flag = %d, want 7 (2 + overHang 5)", flag)
	}
	if v.overHang != 4 {
		t.Errorf("overHang = %d, want 4 (counted down from 5)", v.overHang)
	}
	if v.numOfSpeech != 0 {
		t.Errorf("numOfSpeech = %d, want 0", v.numOfSpeech)
	}
	if v.frameCounter != 0 {
		t.Errorf("frameCounter = %d, want 0 (energy gate closed)", v.frameCounter)
	}
}

// TestGMMProbabilityLLR_overhang_speech_count verifies the speech-count state
// machine: on a speech frame numOfSpeech increments; once it exceeds
// vadMaxSpeechFr (6) it is clamped to 6 and the longer overhang (overHangMax2)
// is armed, otherwise the shorter overhang (overHangMax1) is armed. The cases
// bracket the strict boundary at numOfSpeech == 6.
func TestGMMProbabilityLLR_overhang_speech_count(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name         string
		presetSpeech int16
		wantSpeech   int16
		wantOverHang int16
	}{
		{"below_threshold", 1, 2, 6}, // 1++ -> 2; 2 > 6 false -> overHangMax1
		{"at_threshold", 5, 6, 6},    // 5++ -> 6; 6 > 6 false -> overHangMax1
		{"above_threshold", 6, 6, 9}, // 6++ -> 7; 7 > 6 true -> clamp 6, overHangMax2
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			v := newVADInst(ModeVeryAggressive) // overHangMax1=6, overHangMax2=9, vadMaxSpeechFr=6
			v.globalThresh = -32768             // force vadflag 1 (sumLLR >= globalThresh always)
			v.numOfSpeech = tc.presetSpeech
			flag, _ := v.gmmProbabilityLLR(featUniform1000, 100)
			if flag != 1 {
				t.Fatalf("%s: flag = %d, want 1 (forced speech branch)", tc.name, flag)
			}
			if v.numOfSpeech != tc.wantSpeech {
				t.Errorf("%s: numOfSpeech = %d, want %d", tc.name, v.numOfSpeech, tc.wantSpeech)
			}
			if v.overHang != tc.wantOverHang {
				t.Errorf("%s: overHang = %d, want %d", tc.name, v.overHang, tc.wantOverHang)
			}
		})
	}
}
