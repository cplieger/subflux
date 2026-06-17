package vad

// Mutant-killing tests for unit subflux-u3 (package internal/subsync/vad).
//
// Targets the gremlins survivors in the standard-deviation update of
// gmmProbabilityLLR's adaptive model-update block (vad_classifier.go lines
// 174-200): the speech-std update (vadflag != 0 branch) and the noise-std
// update (vadflag == 0 branch). All identifiers are prefixed gk_subflux_u3_ so
// they never collide with the sibling u1 / u2 files that share this package.
//
// Each golden std array below was captured from the real (unmutated)
// gmmProbabilityLLR and pinned exactly. Every targeted single-token mutation
// was verified offline (by applying the edit to a copy of the source and
// diffing the resulting model state) to change the pinned std array in the
// named scenario, so the matching assertion fails under that mutation. The
// std-update mutants are confined to a branch-local tmp / ssk / nsk, downstream
// of flag / llr / frameCounter / means, so the std array is necessarily where
// any change lands.
//
// Equivalent mutants (NOT killable, deliberately left untested), each confirmed
// SAME across 450+ crafted inputs:
//
//	174:14 CONDITIONALS_NEGATION  `if tmp2 > 0` (speech-std)
//	193:14 CONDITIONALS_NEGATION  `if tmp1 > 0` (noise-std)
//	193:14 CONDITIONALS_BOUNDARY  `if tmp1 > 0` (noise-std)
//	  These sit on the round-toward-zero idiom
//	    if X > 0 { t = X / D } else { t = -(-X / D) }   (D > 0)
//	  Go integer division already truncates toward zero, so both branches
//	  compute the identical X/D for every X. Negating (> -> <=) only swaps
//	  which identical branch runs; the boundary shift (> -> >=) only matters at
//	  X==0, where both branches yield 0. Output is unchanged for all inputs.
//
//	181:13 CONDITIONALS_BOUNDARY  `if ssk < vadMinStd` (speech-std clamp)
//	200:13 CONDITIONALS_BOUNDARY  `if nsk < vadMinStd` (noise-std clamp)
//	  A lower clamp `if s < vadMinStd { s = vadMinStd }`. `<` vs `<=` differ
//	  only at s == vadMinStd, where the taken branch assigns the same value
//	  the other branch leaves in place. Output identical for all s.

import "testing"

// gk_subflux_u3_setAll fills a 12-element GMM array with one value.
func gk_subflux_u3_setAll(a *[vadTableSize]int16, val int16) {
	for i := range a {
		a[i] = val
	}
}

// gk_subflux_u3_assertStd pins the branch precondition (returned flag proves
// the intended vadflag branch ran; frameCounter==1 proves the energy gate
// opened and the model-update block executed) plus the full std array the
// std-update arithmetic produced. The targeted mutants cannot touch
// flag / frameCounter / means, so any change they make lands in wantStd.
func gk_subflux_u3_assertStd(t *testing.T, name string, gotFlag, wantFlag int16,
	gotFC, wantFC int32, gotStd, wantStd [vadTableSize]int16) {
	t.Helper()
	if gotFlag != wantFlag {
		t.Errorf("%s: flag = %d, want %d (wrong vadflag branch taken)", name, gotFlag, wantFlag)
	}
	if gotFC != wantFC {
		t.Errorf("%s: frameCounter = %d, want %d (model-update block must run)", name, gotFC, wantFC)
	}
	if gotStd != wantStd {
		t.Errorf("%s: std = %v, want %v", name, gotStd, wantStd)
	}
}

// Feature vectors (input<<3 lands near the speech / noise Gaussian means).
var (
	gk_subflux_u3_fsSpeech = [vadNumCh]int16{1038, 1261, 1260, 1478, 1480, 789} // -> vadflag 1
	gk_subflux_u3_fnNoise  = [vadNumCh]int16{842, 611, 883, 839, 846, 421}      // -> vadflag 0
	gk_subflux_u3_lowFeat  = [vadNumCh]int16{50, 50, 50, 50, 50, 50}
	gk_subflux_u3_midFeat  = [vadNumCh]int16{3200, 3200, 3200, 3200, 3200, 3200}
)

// TestGkU3_SpeechStdUpdate drives the vadflag==1 path so the speech-std update
// runs for every (channel, gaussian). The pinned speechStds kills every mutant
// in that block:
//
//	175:24 ARITHMETIC_BASE          tmp2 / int32(ssk*10)  (/ -> *)
//	175:35 ARITHMETIC_BASE          ssk*10                (* -> /)
//	177:13 ARITHMETIC_BASE/INVERT   -int16(...)           (drop outer minus)
//	177:20 INVERT/ARITHMETIC_BASE   -tmp2                 (drop inner minus)
//	177:26 ARITHMETIC_BASE          -tmp2 / int32(...)    (/ -> *)
//	177:37 ARITHMETIC_BASE          ssk*10                (* -> /)
//	181:13 CONDITIONALS_NEGATION    ssk < vadMinStd       (< -> >=): every ssk
//	       stays above vadMinStd, so the >= mutant clamps them all down to 384.
//
// Channels with tmp2 < 0 exercise line 177; channels with tmp2 > 0 exercise
// line 175, so a single scenario covers both halves of the divide.
func TestGkU3_SpeechStdUpdate(t *testing.T) {
	v := newVADInst(ModeQuality)
	flag, _ := v.gmmProbabilityLLR(gk_subflux_u3_fsSpeech, 100)
	gk_subflux_u3_assertStd(t, "speechStd", flag, 1, v.frameCounter, 1, v.speechStds,
		[vadTableSize]int16{554, 504, 567, 523, 584, 1231, 509, 828, 492, 1540, 1079, 850})
}

// TestGkU3_NoiseStdUpdate drives the vadflag==0 path with the default model so
// the noise-std update runs. The pinned noiseStds kills:
//
//	187:26 INVERT/ARITHMETIC_BASE   features[ch] - (nmk>>3)  (- -> +)
//	188:32 ARITHMETIC_BASE          deltaN[g] * tmp          (* -> /)
//	191:26 ARITHMETIC_BASE          tmpQ * tmp1              (* -> /)
//	194:24 ARITHMETIC_BASE          tmp1 / int32(nsk)        (/ -> *)
//	196:26 ARITHMETIC_BASE          -tmp1 / int32(nsk)       (/ -> *)
//	200:13 CONDITIONALS_NEGATION    nsk < vadMinStd          (< -> >=)
func TestGkU3_NoiseStdUpdate(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	flag, _ := v.gmmProbabilityLLR(gk_subflux_u3_fnNoise, 100)
	gk_subflux_u3_assertStd(t, "noiseStd", flag, 0, v.frameCounter, 1, v.noiseStds,
		[vadTableSize]int16{384, 1064, 493, 582, 688, 593, 474, 697, 475, 688, 421, 455})
}

// TestGkU3_NoiseStdForceLow forces vadflag==0 with all noise stds at the
// vadMinStd floor and quiet features, so the noise-std update lifts several
// stds just above the floor (385, 386). That spread exposes the sign-handling
// mutants on line 196 (the tmp1 < 0 half of the divide):
//
//	196:13 ARITHMETIC_BASE/INVERT   -int16(...)   (drop outer minus)
//	196:20 INVERT/ARITHMETIC_BASE   -tmp1         (drop inner minus)
//	196:26 ARITHMETIC_BASE          -tmp1 / nsk   (/ -> *)
//
// and re-covers 187:26, 188:32, 191:26, 194:24, 200:13.
func TestGkU3_NoiseStdForceLow(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	v.globalThresh = 32767 // suppress the global-sum trigger -> vadflag 0
	v.localThresh = 32767  // suppress the per-channel trigger   -> vadflag 0
	gk_subflux_u3_setAll(&v.noiseStds, vadMinStd)
	flag, _ := v.gmmProbabilityLLR(gk_subflux_u3_lowFeat, 100)
	gk_subflux_u3_assertStd(t, "noiseStdLow", flag, 0, v.frameCounter, 1, v.noiseStds,
		[vadTableSize]int16{385, 384, 386, 384, 385, 386, 384, 384, 384, 384, 384, 384})
}

// TestGkU3_NoiseStdProbBias forces vadflag==0 with the noise means zeroed and a
// large feature offset, so the conditional-probability term ngprvec[g] is large
// enough that its (+2) rounding bias propagates all the way to a stored std.
// This is the scenario that kills the bias mutant the other inputs mask:
//
//	190:26 ARITHMETIC_BASE  (ngprvec[g] + 2) >> 2  (+ -> -): flipping the +2
//	       rounding bias shifts tmpQ, changing noiseStds[4] from 405 to 395.
//
// (also re-covers 188:32, 191:26, 194:24, 196:* and 200:13.)
func TestGkU3_NoiseStdProbBias(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	v.globalThresh = 32767
	v.localThresh = 32767
	gk_subflux_u3_setAll(&v.noiseStds, 400)
	gk_subflux_u3_setAll(&v.noiseMeans, 0)
	flag, _ := v.gmmProbabilityLLR(gk_subflux_u3_midFeat, 100)
	gk_subflux_u3_assertStd(t, "noiseStdBias", flag, 0, v.frameCounter, 1, v.noiseStds,
		[vadTableSize]int16{398, 398, 400, 404, 405, 403, 402, 402, 401, 396, 395, 398})
}
