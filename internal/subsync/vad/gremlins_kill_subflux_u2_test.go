package vad

// Mutant-killing tests for unit subflux-u2 (package internal/subsync/vad).
//
// Targets the gremlins survivors in vad_classifier.go's adaptive model-update
// block of gmmProbabilityLLR (lines 138-174): the long-term noise-mean
// correction and its lo/hi clamps, the speech-mean update and its clamps, and
// the speech-std update. All identifiers are prefixed gk_subflux_u2_ so they
// never collide with the sibling u1 file that shares this package.
//
// Each golden state array below was captured from the real (unmutated)
// gmmProbabilityLLR and pinned exactly. Every targeted mutant was verified
// offline (by applying its single-token edit to a copy of the source and
// diffing the resulting model state) to change at least one pinned value in
// the named scenario, so the matching assertion fails under that mutation.
//
// Equivalent mutants (NOT killable, deliberately left untested):
//   143:13, 147:13, 158:14, 161:14, 174:14 CONDITIONALS_BOUNDARY.
// Each sits on a clamp / sign-split whose body assigns the bound value
// (e.g. `if nmk3 < lo { nmk3 = lo }`, and at 174 both `>0` branches yield 0).
// `<` vs `<=` (resp. `>` vs `>=`) differ only at the exact boundary, where the
// taken branch assigns the same value the other branch leaves in place, so the
// observable output is identical for every input -> no test can distinguish
// them. (Confirmed SAME across all five scenarios below.)
//
// The INVERT_NEGATIVES entries at 146:25, 168:25, 170:19 sit on binary
// subtraction tokens; the only viable single-token edit there is SUB->ADD,
// identical to the co-located ARITHMETIC_BASE mutant, which the noise/speech
// state pins kill.

import "testing"

// gk_subflux_u2_assertState pins the full post-call model state of a vadInst:
// the returned flag/llr, the frame counter, and all four 12-element GMM arrays.
func gk_subflux_u2_assertState(t *testing.T, name string, v *vadInst,
	flag int16, llr float64, wantFlag int16, wantLLR float64, wantFC int32,
	wantNM, wantSM, wantNS, wantSS [vadTableSize]int16,
) {
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

// Feature vectors (input<<3 lands near the noise / speech Gaussian means).
var (
	gk_subflux_u2_fnNoise  = [vadNumCh]int16{842, 611, 883, 839, 846, 421}      // -> vadflag 0
	gk_subflux_u2_fsSpeech = [vadNumCh]int16{1038, 1261, 1260, 1478, 1480, 789} // -> vadflag 1
)

// TestGkU2_NoiseBranchStatePin drives gmmProbabilityLLR down the vadflag==0
// path: the noise-mean long-term correction + clamps run, the noise-std
// else-branch runs, and the speech branch is skipped. The pinned state kills:
//
//	139:32 ARITHMETIC_BASE        (featureMin<<4) - nGlobalQ8  (- -> +)
//	140:18 ARITHMETIC_BASE        nmk2 + correction            (+ -> -)
//	140:39 ARITHMETIC_BASE        int32(ndelt)*backEta         (* -> /)
//	143:13 CONDITIONALS_NEGATION  nmk3 < lo                    (< -> >=)
//	146:21 ARITHMETIC_BASE        72 + k                       (+ -> -)
//	146:25 ARITHMETIC_BASE        k - ch                       (- -> +)
//	146:25 INVERT_NEGATIVES       k - ch                       (SUB->ADD, same edit)
//	147:13 CONDITIONALS_NEGATION  nmk3 > hi                    (> -> <=)
//	152:16 CONDITIONALS_NEGATION  vadflag != 0                 (!= -> ==): the
//	       == mutant takes the speech branch here, mutating speechMeans/Stds.
func TestGkU2_NoiseBranchStatePin(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u2_fnNoise, 100)
	gk_subflux_u2_assertState(t, "noise", v, flag, llr,
		0, -608, 1,
		[vadTableSize]int16{8663, 8848, 8769, 8633, 8353, 7725, 8791, 8699, 8897, 8761, 8481, 7869},
		[vadTableSize]int16{10298, 11128, 10666, 12417, 13039, 9500, 11465, 10614, 11467, 8175, 9376, 10674},
		[vadTableSize]int16{384, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{555, 505, 567, 524, 585, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// TestGkU2_SpeechBranchStatePin drives the vadflag==1 path: the noise-mean
// correction runs with nmk2==nmk, and the speech-mean + speech-std updates run.
// The pinned state kills:
//
//	155:32 ARITHMETIC_BASE        int32(delt) * speechUpdate   (* -> /)
//	156:18 ARITHMETIC_BASE        smk + ((tmp+1)>>1)           (+ -> -)
//	156:26 ARITHMETIC_BASE        tmp + 1                      (+ -> -)
//	158:14 CONDITIONALS_NEGATION  smk2 < minMean[k]            (< -> >=)
//	161:14 CONDITIONALS_NEGATION  smk2 > mx                    (> -> <=)
//	168:25 ARITHMETIC_BASE        features[ch] - tmp           (- -> +)
//	168:25 INVERT_NEGATIVES       features[ch] - tmp           (SUB->ADD, same edit)
//	170:19 ARITHMETIC_BASE        tmp1 - 4096                  (- -> +)
//	170:19 INVERT_NEGATIVES       tmp1 - 4096                  (SUB->ADD, same edit)
//
// (It also re-covers the noise-branch arithmetic mutants.)
func TestGkU2_SpeechBranchStatePin(t *testing.T) {
	v := newVADInst(ModeQuality)
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u2_fsSpeech, 100)
	gk_subflux_u2_assertState(t, "speech", v, flag, llr,
		1, 1470, 1,
		[vadTableSize]int16{8663, 8848, 8768, 8633, 8353, 7724, 8791, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{10298, 11128, 10669, 12417, 13039, 9503, 11463, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{554, 504, 567, 523, 584, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// TestGkU2_LowClampArithmetic forces vadflag=1 and positions channel-0 gauss-0
// so its corrected noise mean lands just below lo = (k+5)<<7 (= 640), making
// the lower clamp `if nmk3 < lo { nmk3 = lo }` decisive. Kills:
//
//	142:20 ARITHMETIC_BASE  (k + 5) << 7  (+ -> - changes lo to (k-5)<<7, so
//	       the clamp activation/target differs and noiseMeans[0] changes).
func TestGkU2_LowClampArithmetic(t *testing.T) {
	v := newVADInst(ModeQuality)
	v.globalThresh = -32768 // force vadflag == 1 (sumLLR >= globalThresh always)
	v.noiseMeans[0] = 0     // nmk2 for channel 0, gaussian 0
	v.noiseMeans[6] = 16000 // raises nGlobalQ8 so the long-term correction is small
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u2_fsSpeech, 100)
	gk_subflux_u2_assertState(t, "loclamp", v, flag, llr,
		1, 1614, 1,
		[vadTableSize]int16{607, 8848, 8768, 8633, 8353, 7724, 9311, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{8449, 11128, 10669, 12417, 13039, 9503, 9614, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{554, 504, 567, 523, 584, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}

// TestGkU2_SpeechMeanUpperClampArithmetic forces vadflag=1 with a high initial
// speech mean so the updated smk2 sits between the mutated and original upper
// clamp mx = maxspe + 640 (= 13440; maxspe is the hardcoded 12800). Kills:
//
//	157:19 ARITHMETIC_BASE  maxspe + 640  (+ -> - lowers mx to 12160, which
//	       then clamps smk2 and changes speechMeans[0]).
func TestGkU2_SpeechMeanUpperClampArithmetic(t *testing.T) {
	v := newVADInst(ModeQuality)
	v.globalThresh = -32768
	v.speechMeans[0] = 13000
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u2_fsSpeech, 100)
	gk_subflux_u2_assertState(t, "mxclamp", v, flag, llr,
		1, 1452, 1,
		[vadTableSize]int16{8991, 8848, 8768, 8633, 8353, 7724, 9119, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{13568, 11128, 10669, 12417, 13039, 9503, 10026, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{555, 504, 567, 523, 584, 1231, 512, 828, 492, 1540, 1079, 850},
	)
}

// TestGkU2_SpeechStdMeanShiftArithmetic forces vadflag=1 with channel-0 speech
// parameters tuned so the speech-std update's `(smk + 4) >> 3` term propagates
// a one-LSB difference all the way to the stored std. Kills:
//
//	167:18 ARITHMETIC_BASE  (smk + 4) >> 3  (+ -> - shifts the smoothed-mean
//	       term by one, changing speechStds[0] from 800 to 799).
func TestGkU2_SpeechStdMeanShiftArithmetic(t *testing.T) {
	v := newVADInst(ModeQuality)
	v.globalThresh = -32768
	v.speechMeans[0] = 500
	v.speechStds[0] = 800
	f := gk_subflux_u2_fsSpeech // array copy; package var is not mutated
	f[0] = 50
	flag, llr := v.gmmProbabilityLLR(f, 100)
	gk_subflux_u2_assertState(t, "stdshift", v, flag, llr,
		1, 1608, 1,
		[vadTableSize]int16{8124, 8848, 8768, 8633, 8353, 7724, 8252, 8699, 8896, 8761, 8481, 7867},
		[vadTableSize]int16{4965, 11128, 10669, 12417, 13039, 9503, 13798, 10615, 11466, 8175, 9376, 10674},
		[vadTableSize]int16{378, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455},
		[vadTableSize]int16{800, 504, 567, 523, 584, 1231, 509, 828, 492, 1540, 1079, 850},
	)
}
