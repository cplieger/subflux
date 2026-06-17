package vad

// Mutant-killing tests for unit subflux-u5 (package internal/subsync/vad).
//
// Targets gremlins survivors in:
//   - vad_classifier.go: the overhang speech-count boundary (254:20).
//   - vad_filterbank.go: vadAllpass arithmetic (lines 8, 10) and vadLogEnergy
//     normalization arithmetic / conditionals (lines 72, 78, 79, 85, 93).
//   - vad_mintracker.go: findMinimum aging (23, 25), pos-init (35), and the two
//     non-equivalent binary-search comparison negations (39, 40).
//
// All identifiers are prefixed gk_subflux_u5_ so they never collide with the
// sibling u1/u2/u3/u4 files that share this package. Every golden value below
// was derived from a faithful fixed-point reference of the UNMUTATED functions
// (mirroring Go int16/int32/uint32 truncation and arithmetic-vs-logical shift
// semantics) and pinned exactly; the reference reproduces the package's own
// known-correct assertions (vadLogEnergy silence -> (368,0); findMinimum(500,0)
// on a fresh instance -> 1600). Each targeted mutant was confirmed to change a
// pinned value, so the matching assertion fails under that single-token edit.
//
// Equivalent mutants (NOT killable, deliberately left untested):
//
//   vad_filterbank.go:
//     58:13 CONDITIONALS_BOUNDARY  `if energy > 0xFFFFFFFF-sq`  (> -> >=)
//       Only differs when energy+sq == 2^32-1 exactly. At that point the
//       overflow branch (energy>>=1; energy+=sq>>1; rshifts++) is a lossless
//       renormalization, so the final logE/totalE are identical to the
//       non-overflow path. Confirmed identical even on a crafted input whose
//       squares sum to exactly 2^32-1.
//     78:11 CONDITIONALS_BOUNDARY  `if normR < 0`               (< -> <=)
//       Differs only at normR==0, where the if-branch `energy <<= uint(0)` and
//       the else-branch `energy >>= uint(0)` are both no-ops. Output identical.
//     89:12 CONDITIONALS_BOUNDARY  `if totalE <= vadMinEnergy`  (<= -> <)
//       totalE is the named return, always 0 here (never assigned before this
//       line), and 0 <= 10 and 0 < 10 are both true for every input.
//
//   vad_mintracker.go:
//     37:16, 38:17, 39:18, 40:19, 45:25 CONDITIONALS_BOUNDARY  (< -> <=)
//       These guard `featureVal < sv[k]` in the sorted-insert binary search.
//       `<` vs `<=` differ only at featureVal==sv[k], where inserting a
//       duplicate of sv[k] at index k vs k+1 yields the identical sorted
//       array. Confirmed identical post-call lowValue for every node.

import "testing"

// gk_subflux_u5_feat is a uniform feature vector. Its exact values are
// irrelevant to the overhang test: globalThresh=-32768 forces vadflag=1
// regardless, so only numOfSpeech/overHang state (set directly) is observed.
var gk_subflux_u5_feat = [vadNumCh]int16{1000, 1000, 1000, 1000, 1000, 1000}

// gk_subflux_u5_incrLow is a strictly-increasing 16-element lowValue (sv)
// seed so the binary search is well defined and inserts are unambiguous.
func gk_subflux_u5_incrLow() [16]int16 {
	return [16]int16{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160}
}

// gk_subflux_u5_assert16 compares the first 16 elements of got to want.
func gk_subflux_u5_assert16(t *testing.T, name string, got []int16, want [16]int16) {
	t.Helper()
	var g [16]int16
	copy(g[:], got)
	if g != want {
		t.Errorf("%s = %v, want %v", name, g, want)
	}
}

// --- vad_classifier.go : overhang speech-count boundary ------------------

// Kills 254:20 CONDITIONALS_BOUNDARY (`v.numOfSpeech > vadMaxSpeechFr` -> >=).
// globalThresh=-32768 forces vadflag=1 so the overhang else-branch runs.
// Preset numOfSpeech=5 -> after `numOfSpeech++` it is exactly vadMaxSpeechFr(6).
// Original `6 > 6` is false -> else -> overHang = overHangMax1 (6).
// Boundary mutant `6 >= 6` is true -> if -> overHang = overHangMax2 (9).
func TestGkSubfluxU5_OverhangSpeechCountBoundary(t *testing.T) {
	v := newVADInst(ModeVeryAggressive) // overHangMax1=6, overHangMax2=9, vadMaxSpeechFr=6
	v.globalThresh = -32768             // force vadflag 1 (sumLLR >= globalThresh always)
	v.numOfSpeech = 5                   // ++ -> 6 == vadMaxSpeechFr
	flag, _ := v.gmmProbabilityLLR(gk_subflux_u5_feat, 100)
	if flag != 1 {
		t.Fatalf("flag = %d, want 1 (forced speech branch)", flag)
	}
	if v.numOfSpeech != 6 {
		t.Errorf("numOfSpeech = %d, want 6", v.numOfSpeech)
	}
	if v.overHang != 6 {
		t.Errorf("overHang = %d, want 6 (overHangMax1; > -> >= mutant gives overHangMax2=9)", v.overHang)
	}
}

// --- vad_filterbank.go : vadAllpass arithmetic ---------------------------

// Kills 8:15 (st + coef*in : + -> -), 8:39 (i*stride on line 8 : * -> /),
// 10:19 (i*stride on line 10 : * -> /), and 10:36 ((x<<14) - coef*out :
// - -> +, both ARITHMETIC_BASE and INVERT_NEGATIVES, same edit).
// stride=2 makes i*stride (0,2) differ from i/stride (0,0); the inputs are
// non-trivial so every arithmetic edit on lines 8/10 changes out or state.
// Golden out=[6243 2885], state=-3895 from the faithful reference.
func TestGkSubfluxU5_VadAllpassArithmetic(t *testing.T) {
	in := []int16{16384, 8192, -4096, 2048}
	out := make([]int16, 2)
	state := int16(1000)
	vadAllpass(in, 2, 2, allpassUpper, &state, out) // allpassUpper = 20972
	if out[0] != 6243 || out[1] != 2885 {
		t.Errorf("vadAllpass out = %v, want [6243 2885]", out)
	}
	if state != -3895 {
		t.Errorf("vadAllpass final state = %d, want -3895", state)
	}
}

// --- vad_filterbank.go : vadLogEnergy (high-energy normalization path) ----

// Kills 72:8  CONDITIONALS_NEGATION (`for t < 0x80000000` -> >=): NormU32 loop.
//
//	78:11 CONDITIONALS_NEGATION (`if normR < 0` -> >=): picks the wrong shift.
//	85:23 ARITHMETIC_BASE       (`kLogIntPart + term` -> -): shifts log2e.
//
// All three shift logE for this input (energy = 80 * 1000^2 = 80,000,000,
// normU=5, normR=12, rshifts=12). Golden (logE,totalE) = (1628, 11).
func TestGkSubfluxU5_VadLogEnergyHighEnergy(t *testing.T) {
	data := make([]int16, 80)
	for i := range data {
		data[i] = 1000
	}
	logE, totalE := vadLogEnergy(data, 80, 368)
	if logE != 1628 {
		t.Errorf("vadLogEnergy(all-1000) logE = %d, want 1628", logE)
	}
	if totalE != 11 {
		t.Errorf("vadLogEnergy(all-1000) totalE = %d, want 11", totalE)
	}
}

// --- vad_filterbank.go : vadLogEnergy (rshifts < 0 path) -----------------

// Kills 79:19 ARITHMETIC_BASE + INVERT_NEGATIVES (`energy <<= uint(-normR)`:
//
//	dropping the minus makes the shift count huge -> energy becomes 0,
//	changing logE), and 93:34 ARITHMETIC_BASE (`int16(energy >> uint(-rshifts))`:
//	dropping the minus zeroes totalE without touching logE).
//
// All-ones input (n=80): energy=80, normU=25, normR=-8, rshifts=-8 -> the
// `normR < 0` left-shift branch and the `rshifts < 0` totalE branch both run.
// Golden (logE,totalE) = (668, 80); 79 changes logE (668->656), 93 changes
// totalE (80->0).
func TestGkSubfluxU5_VadLogEnergyLowEnergyShift(t *testing.T) {
	data := make([]int16, 80)
	for i := range data {
		data[i] = 1
	}
	logE, totalE := vadLogEnergy(data, 80, 368)
	if logE != 668 {
		t.Errorf("vadLogEnergy(all-1) logE = %d, want 668", logE)
	}
	if totalE != 80 {
		t.Errorf("vadLogEnergy(all-1) totalE = %d, want 80", totalE)
	}
}

// --- vad_mintracker.go : findMinimum binary-search negation --------------

// Kills 39:18 CONDITIONALS_NEGATION (`featureVal < sv[1]` -> >=) and
//
//	40:19 CONDITIONALS_NEGATION (`featureVal < sv[0]` -> >=).
//
// indexVec defaults to 0, so the aging step only increments (no eviction) and
// leaves sv stable; FV=15 sorts to pos=1. Either negation diverts the search to
// a different pos (39n -> pos 2; 40n -> pos 0), changing the inserted lowValue.
func TestGkSubfluxU5_FindMinimumSearchNegation(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	seed := gk_subflux_u5_incrLow()
	copy(v.lowValue[:16], seed[:])
	v.findMinimum(15, 0)
	gk_subflux_u5_assert16(t, "lowValue after findMinimum(15)", v.lowValue[:16],
		[16]int16{10, 15, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150})
}

// --- vad_mintracker.go : findMinimum pos initializer ---------------------

// Kills 35:9 ARITHMETIC_BASE + INVERT_NEGATIVES (`pos := -1` -> `pos := 1`).
// FV=200 is >= sv[15]=160, so the search leaves pos at its initial value and
// the `if pos >= 0` insert is skipped -> lowValue unchanged. If pos initializes
// to 1 instead of -1, FV is inserted at index 1, perturbing lowValue.
func TestGkSubfluxU5_FindMinimumPosInit(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	seed := gk_subflux_u5_incrLow()
	copy(v.lowValue[:16], seed[:])
	v.findMinimum(200, 0)
	gk_subflux_u5_assert16(t, "lowValue after findMinimum(200) no-insert", v.lowValue[:16],
		[16]int16{10, 20, 30, 40, 50, 60, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160})
}

// --- vad_mintracker.go : findMinimum aging increment ---------------------

// Kills 23:10 INCREMENT_DECREMENT (`age[i]++` -> `age[i]--`).
// All indexVec entries set to 5 (none == 100) so every age is incremented to 6
// (the decrement mutant yields 4). FV=200 does not insert, so indexVec changes
// only via the aging loop.
func TestGkSubfluxU5_FindMinimumAgeIncrement(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	seed := gk_subflux_u5_incrLow()
	copy(v.lowValue[:16], seed[:])
	for i := range 16 {
		v.indexVec[i] = 5
	}
	v.findMinimum(200, 0)
	gk_subflux_u5_assert16(t, "indexVec after aging increment", v.indexVec[:16],
		[16]int16{6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6})
}

// --- vad_mintracker.go : findMinimum eviction shift ----------------------

// Kills 25:18 CONDITIONALS_NEGATION (eviction loop `for j := i; j < 15` -> >=).
// indexVec[5]=100 triggers eviction at i=5: the original loop shifts sv[5..14]
// left by one (sv[5]=sv[6]...) and sets sv[15]=10000. The `j >= 15` mutant skips
// the loop entirely (5 >= 15 is false), so sv[5..14] keep their original values.
// FV=10001 is >= the post-eviction sv[15]=10000, so no insertion perturbs sv.
func TestGkSubfluxU5_FindMinimumEvictionShift(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	seed := gk_subflux_u5_incrLow()
	copy(v.lowValue[:16], seed[:])
	for i := range 16 {
		v.indexVec[i] = 1
	}
	v.indexVec[5] = 100
	v.findMinimum(10001, 0)
	gk_subflux_u5_assert16(t, "lowValue after eviction shift", v.lowValue[:16],
		[16]int16{10, 20, 30, 40, 50, 70, 80, 90, 100, 110, 120, 130, 140, 150, 160, 10000})
}
