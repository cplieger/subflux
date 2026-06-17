package vad

// Mutant-killing tests for unit subflux-u1 (package internal/subsync/vad).
//
// Targets gremlins surviving mutants in vad.go (FramesBinaryThresholdTuned) and
// vad_classifier.go (vadGaussProb, gmmProbabilityLLR). Helpers are prefixed
// gk_subflux_u1_ to avoid collisions. Golden state values were derived by
// running the real (unmutated) functions and pinned exactly, so any mutation
// that changes the observable output state or control flow fails an assertion.

import (
	"context"
	"math"
	"testing"
	"time"
)

// ---- helpers -------------------------------------------------------------

// gk_subflux_u1_loudFrame is one 80-sample 440Hz sine frame at amplitude 20000.
func gk_subflux_u1_loudFrame() []int16 {
	f := make([]int16, 80)
	for i := range f {
		f[i] = int16(20000 * math.Sin(float64(i)*2*math.Pi*440/8000))
	}
	return f
}

// gk_subflux_u1_seqCtx is a context whose Err() returns nil on the first call
// and context.Canceled on every call thereafter. Used to distinguish how often
// FramesBinaryThresholdTuned evaluates the cancellation guard (line 177).
type gk_subflux_u1_seqCtx struct{ n int }

func (c *gk_subflux_u1_seqCtx) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c *gk_subflux_u1_seqCtx) Done() <-chan struct{}       { return nil }
func (c *gk_subflux_u1_seqCtx) Value(any) any               { return nil }
func (c *gk_subflux_u1_seqCtx) Err() error {
	c.n++
	if c.n == 1 {
		return nil
	}
	return context.Canceled
}

// gk_subflux_u1_assertState pins the full post-call model state of a vadInst.
func gk_subflux_u1_assertState(t *testing.T, name string, v *vadInst, flag int16, llr float64,
	wantFlag int16, wantLLR float64, wantFC int32,
	wantNM, wantSM, wantNS, wantSS [vadTableSize]int16) {
	t.Helper()
	if flag != wantFlag {
		t.Errorf("%s: flag = %d, want %d", name, flag, wantFlag)
	}
	if llr != wantLLR {
		t.Errorf("%s: llr = %v, want %v", name, llr, wantLLR)
	}
	if v.frameCounter != wantFC {
		t.Errorf("%s: frameCounter = %d, want %d", name, v.frameCounter, wantFC)
	}
	if v.noiseMeans != wantNM {
		t.Errorf("%s: noiseMeans = %v, want %v", name, v.noiseMeans, wantNM)
	}
	if v.speechMeans != wantSM {
		t.Errorf("%s: speechMeans = %v, want %v", name, v.speechMeans, wantSM)
	}
	if v.noiseStds != wantNS {
		t.Errorf("%s: noiseStds = %v, want %v", name, v.noiseStds, wantNS)
	}
	if v.speechStds != wantSS {
		t.Errorf("%s: speechStds = %v, want %v", name, v.speechStds, wantSS)
	}
}

// Crafted feature vectors (input<<3 sits near the noise/speech Gaussian means).
var (
	gk_subflux_u1_fnNoise  = [vadNumCh]int16{842, 611, 883, 839, 846, 421}     // -> vadflag 0
	gk_subflux_u1_fsSpeech = [vadNumCh]int16{1038, 1261, 1260, 1478, 1480, 789} // -> vadflag 1
	gk_subflux_u1_fnUnif   = [vadNumCh]int16{50, 50, 50, 50, 50, 50}            // -> vadflag 0
)

// ---- vad.go : FramesBinaryThresholdTuned --------------------------------

// Kills 177:13 CONDITIONALS_NEGATION (i%1000 == 0 -> != 0).
// With a pre-cancelled context the original returns at i==0 (empty); the !=
// mutant skips i==0 and returns at i==1 (length 1).
func TestGkU1_FramesCancelGuard_equality(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	pcm := make([]int16, 80*4) // 4 frames
	got := FramesBinaryThresholdTuned(ctx, pcm, ModeVeryAggressive, 125, 10, 0, Tuning{})
	if len(got) != 0 {
		t.Errorf("cancelled ctx: len(result) = %d, want 0", len(got))
	}
}

// Kills 177:7 ARITHMETIC_BASE (i%1000 -> i*1000 and any other arithmetic op).
// The sequenced context allows exactly one Err()==nil then Canceled. The
// original re-checks at i==1000 (returns 1000); a %->* mutant only checks at
// i==0 and processes every frame (returns 1001); %->/ checks at i==1 (returns
// 1); %->+ / %->- never re-check (returns 1001). Only len==1000 matches original.
func TestGkU1_FramesCancelGuard_modulo(t *testing.T) {
	pcm := make([]int16, 80*1001) // 1001 frames, i in [0,1000]
	got := FramesBinaryThresholdTuned(&gk_subflux_u1_seqCtx{}, pcm, ModeVeryAggressive, 125, 10, 0, Tuning{})
	if len(got) != 1000 {
		t.Errorf("sequenced ctx: len(result) = %d, want 1000", len(got))
	}
}

// Kills 180:17 ARITHMETIC_BASE (i*frameSize -> i/frameSize).
// Frame 0 is loud, frame 1 is silent. The original reads pcm[80:160] (silent,
// llr=690 < 750) for frame 1; the i/frameSize mutant reads pcm[0:160] whose
// first 80 samples are the loud frame (llr=812 >= 750), flipping result[1].
func TestGkU1_FramesFrameStart(t *testing.T) {
	pcm := make([]int16, 160)
	copy(pcm, gk_subflux_u1_loudFrame()) // frame 0 loud; frame 1 stays silent
	got := FramesBinaryThresholdTuned(context.Background(), pcm, ModeQuality, 750, 0, 1.0, Tuning{})
	if len(got) != 2 {
		t.Fatalf("len(result) = %d, want 2", len(got))
	}
	if got[1] != -1.0 {
		t.Errorf("result[1] = %v, want -1.0 (original reads silent frame 1)", got[1])
	}
}

// Kills 188:13 INCREMENT_DECREMENT (remaining-- -> remaining++).
// One loud frame (then a re-triggering tail at frame 1) arms a 2-frame overhang.
// The original counts the overhang down to 0 so result[4] is silence (-1); the
// ++ mutant keeps growing remaining so every later frame stays +1.
func TestGkU1_FramesOverhangCountdown(t *testing.T) {
	pcm := make([]int16, 80*7) // loud frame 0, silence frames 1..6
	copy(pcm, gk_subflux_u1_loudFrame())
	got := FramesBinaryThresholdTuned(context.Background(), pcm, ModeQuality, 300, 2, 1.0, Tuning{})
	if len(got) != 7 {
		t.Fatalf("len(result) = %d, want 7", len(got))
	}
	if got[4] != -1.0 {
		t.Errorf("result[4] = %v, want -1.0 (overhang counted down to 0)", got[4])
	}
}

// ---- vad_classifier.go : vadGaussProb -----------------------------------

// Kills 8:25 ARITHMETIC_BASE (131072 + std>>1 -> 131072 - std>>1).
// At the Gaussian peak (input==mean, delta 0) prob = invStd*1024. The rounding
// numerator changes invStd from 341 to 340, i.e. prob 349184 -> 348160.
func TestGkU1_GaussProbRoundingNumerator(t *testing.T) {
	prob, delta := vadGaussProb(0, 0, 384)
	if prob != 349184 {
		t.Errorf("vadGaussProb(0,0,384) prob = %d, want 349184", prob)
	}
	if delta != 0 {
		t.Errorf("vadGaussProb(0,0,384) delta = %d, want 0", delta)
	}
}

// ---- vad_classifier.go : gmmProbabilityLLR control flow -----------------

// Kills 57:16 CONDITIONALS_BOUNDARY (totalPower > minEnergy -> >=).
// At totalPower == minEnergy the original skips the GMM block entirely
// (frameCounter stays 0, llr 0, model untouched); the >= mutant runs it.
func TestGkU1_EnergyGateBoundary(t *testing.T) {
	v := newVADInst(ModeQuality)
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u1_fsSpeech, vadMinEnergy)
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

// Kills 87:10 ARITHMETIC_BASE (llr*4 -> llr/4) in the per-channel decision.
// globalThresh is disabled so vadflag depends solely on the per-channel test.
// Max per-channel llr is 25: 25*4=100 > 50 fires (original flag 1); 25/4=6 <= 50
// for every channel (mutant flag 0).
func TestGkU1_PerChannelScale(t *testing.T) {
	v := newVADInst(ModeQuality)
	v.globalThresh = 32767
	v.localThresh = 50
	flag, _ := v.gmmProbabilityLLR(gk_subflux_u1_fsSpeech, 100)
	if flag != 1 {
		t.Errorf("flag = %d, want 1 (llr*4=100 > localThresh 50)", flag)
	}
}

// Kills 87:13 CONDITIONALS_NEGATION (llr*4 > localThresh -> <=).
// localThresh=-1 so every channel's llr*4 (>= 0) exceeds it: original fires
// (flag 1); the <= mutant is false for all channels (flag 0).
func TestGkU1_PerChannelComparison(t *testing.T) {
	v := newVADInst(ModeQuality)
	v.globalThresh = 32767
	v.localThresh = -1
	flag, _ := v.gmmProbabilityLLR(gk_subflux_u1_fsSpeech, 100)
	if flag != 1 {
		t.Errorf("flag = %d, want 1 (all llr*4 > -1)", flag)
	}
}

// Kills 107:13 CONDITIONALS_BOUNDARY (sumLLR >= globalThresh -> >) AND
// CONDITIONALS_NEGATION (>= -> <). localThresh is disabled so vadflag depends
// solely on the global test; globalThresh == sumLLR (1470). Original >= is true
// at equality (flag 1); both > and < are false at equality (flag 0).
func TestGkU1_GlobalThresholdBoundary(t *testing.T) {
	v := newVADInst(ModeQuality)
	v.localThresh = 32767
	v.globalThresh = 1470 // == sumLLR for fsSpeech
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u1_fsSpeech, 100)
	if llr != 1470 {
		t.Fatalf("precondition: sumLLR = %v, want 1470", llr)
	}
	if flag != 1 {
		t.Errorf("flag = %d, want 1 (sumLLR >= globalThresh at equality)", flag)
	}
}

// ---- vad_classifier.go : adaptive model-update state pins ---------------

// Noise-path pin (vadflag==0). Kills:
//   95:57 (/->* ngprvec[ch]), 96:34 (- -> + / invert ngprvec[ch+6]),
//   119:16 & 125:16 (g index + -> - : negative-index panic),
//   120:43 (* -> / noiseGlobal), 133:16 (== -> != vadflag test),
//   135:17 (+ -> -) and 135:37 (* -> /) noise-mean delt update,
//   139:32 (- -> + long-term noise correction).
// Each mutation changes the pinned noiseMeans (verified by exhaustive
// re-implementation diff) or panics on a negative slice index.
func TestGkU1_NoisePathStatePin(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u1_fnNoise, 100)
	gk_subflux_u1_assertState(t, "FN", v, flag, llr,
		0, -608, 1,
		[vadTableSize]int16{8663, 8848, 8769, 8633, 8353, 7725, 8791, 8699, 8897, 8761, 8481, 7869},
		[vadTableSize]int16{10298, 11128, 10666, 12417, 13039, 9500, 11465, 10614, 11467, 8175, 9376, 10674},
		[vadTableSize]int16{384, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{555, 505, 567, 524, 585, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// Speech-path pin (vadflag==1). Kills:
//   102:57 (/->* sgprvec[ch]), 103:34 (- -> + / invert sgprvec[ch+6]).
// Both mutations change the pinned speechMeans / speechStds.
func TestGkU1_SpeechPathStatePin(t *testing.T) {
	v := newVADInst(ModeQuality)
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u1_fsSpeech, 100)
	gk_subflux_u1_assertState(t, "FS", v, flag, llr,
		1, 1470, 1,
		[vadTableSize]int16{8663, 8848, 8768, 8633, 8353, 7724, 8791, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{10298, 11128, 10669, 12417, 13039, 9503, 11463, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{554, 504, 567, 523, 584, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// Noise-path pin with uniform low features (vadflag==0). Kills 134:39
// (* -> / in the noise-mean delt computation); under this input the mutated
// delt survives the >>22 Q-scaling and changes the pinned noiseMeans.
func TestGkU1_NoiseDeltStatePin(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u1_fnUnif, 100)
	gk_subflux_u1_assertState(t, "FN3", v, flag, llr,
		0, 0, 1,
		[vadTableSize]int16{8663, 8848, 8769, 8633, 8353, 7723, 8791, 8699, 8897, 8761, 8481, 7869},
		[vadTableSize]int16{10298, 11128, 10666, 12417, 13039, 9500, 11465, 10614, 11467, 8175, 9376, 10674},
		[vadTableSize]int16{384, 1064, 491, 585, 690, 594, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{555, 505, 567, 524, 585, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}
