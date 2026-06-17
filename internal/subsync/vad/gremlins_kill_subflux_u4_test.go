package vad

// Mutant-killing tests for unit subflux-u4 (package internal/subsync/vad).
//
// Targets the gremlins survivors in gmmProbabilityLLR's model-separation /
// clamping block (vad_classifier.go lines 207-240) and the overhang-smoothing
// block (lines 245-260). All identifiers are prefixed gk_subflux_u4_ so they
// never collide with the sibling u1 / u2 / u3 files that share this package.
//
// Each golden array below was captured from the real (unmutated)
// gmmProbabilityLLR and pinned exactly. Every targeted single-token mutation
// changes a pinned value in the named scenario (verified by reasoning about
// which branch fires and what the operator edit does to the observable state),
// so the matching assertion fails under that mutation.
//
// Scenario design (vadflag is forced via the thresholds so the model-update
// path is deterministic):
//   - "sep" sets noiseMeans=12000, speechMeans=8000 with vadflag forced 0. The
//     model update hi-clamps noiseMeans to ~9216, so the separation test
//     diff = (sGlob>>9)-(nGlob>>9) = 2000-2327 = -327 < minDiff -> separation
//     FIRES (gap=871: sShift=2830 lifts speechMeans 8000->10830, nShift=653
//     drops noiseMeans), and nG = nGlob>>7 = 9310 > maxNoise[0]=9216 -> the
//     NOISE clamp FIRES (d=94). The speech clamp does NOT (sG=8000<maxSpeech).
//   - "speechClamp" sets speechMeans=14000 with vadflag forced 0. sG=14000 >
//     maxSpeech -> the SPEECH clamp FIRES, dropping each speechMeans to exactly
//     maxSpeech[ch]; diff=1500>minDiff so separation does NOT fire.
//   - overhang scenarios close/force the energy gate and the vadflag branch.
//
// Equivalent mutants (NOT killable, deliberately left untested):
//
//	215:12 CONDITIONALS_BOUNDARY  `if diff < minDiff[ch]`   (< -> <=)
//	228:10 CONDITIONALS_BOUNDARY  `if sG > maxs`            (> -> >=)
//	235:10 CONDITIONALS_BOUNDARY  `if nG > maxNoise[ch]`    (> -> >=)
//	  Each guards a separation/clamp whose body is a no-op at the exact
//	  boundary: at diff==minDiff the gap is 0 so sShift=nShift=0 (means += / -=
//	  0); at sG==maxs (resp. nG==maxNoise) the subtracted d = sG-maxs = 0. `<`
//	  vs `<=` (resp. `>` vs `>=`) differ ONLY at that boundary, where the taken
//	  branch leaves the state identical to the skipped branch. No input can
//	  distinguish them -> equivalent. (Same pattern u2/u3 documented for the
//	  earlier lo/hi clamps.)
//
// The INVERT_NEGATIVES entries at 214:28, 216:24, 229:13, 236:13 sit on binary
// subtraction tokens; the only viable single-token edit there is SUB->ADD,
// identical to the co-located ARITHMETIC_BASE mutant, which the same assertion
// kills.

import "testing"

// gk_subflux_u4_setAll fills a 12-element GMM array with one value.
func gk_subflux_u4_setAll(a *[vadTableSize]int16, val int16) {
	for i := range a {
		a[i] = val
	}
}

// gk_subflux_u4_assertModel pins the returned flag/llr, the frame counter, and
// the two GMM mean arrays that the separation/clamp block mutates.
func gk_subflux_u4_assertModel(t *testing.T, name string,
	gotFlag int16, gotLLR float64, gotFC int32, gotNM, gotSM [vadTableSize]int16,
	wantFlag int16, wantLLR float64, wantFC int32, wantNM, wantSM [vadTableSize]int16,
) {
	t.Helper()
	if gotFlag != wantFlag {
		t.Errorf("%s: flag = %d, want %d", name, gotFlag, wantFlag)
	}
	if gotLLR != wantLLR {
		t.Errorf("%s: llr = %v, want %v", name, gotLLR, wantLLR)
	}
	if gotFC != wantFC {
		t.Errorf("%s: frameCounter = %d, want %d", name, gotFC, wantFC)
	}
	if gotNM != wantNM {
		t.Errorf("%s: noiseMeans = %v, want %v", name, gotNM, wantNM)
	}
	if gotSM != wantSM {
		t.Errorf("%s: speechMeans = %v, want %v", name, gotSM, wantSM)
	}
}

// gk_subflux_u4_feat is a uniform feature vector (input<<3 = 8000, near the
// means). The exact values only set the model-update magnitude; the targeted
// branches' firing depends on the means, which the test sets directly.
var gk_subflux_u4_feat = [vadNumCh]int16{1000, 1000, 1000, 1000, 1000, 1000}

// TestGkU4_SeparationAndNoiseClampStatePin drives the model-separation block
// (separation FIRES, gap=871) and the noise-mean clamp (FIRES, d=94) with
// vadflag forced 0. The pinned noiseMeans/speechMeans kill:
//
//	211:37 ARITHMETIC_BASE        nGlob += noiseMeans[g] * noiseWt[g]  (* -> /):
//	       shrinks nGlob so diff > minDiff (separation skipped) AND nG < maxNoise
//	       (noise clamp skipped) -> means differ from the pinned post-block state.
//	214:28 ARITHMETIC_BASE/INVERT diff = (sGlob>>9) - (nGlob>>9)       (- -> +):
//	       diff jumps to ~4327 > minDiff -> separation skipped -> speechMeans 8000.
//	215:12 CONDITIONALS_NEGATION  if diff < minDiff[ch]                (< -> >=):
//	       -327 >= 544 is false -> separation skipped -> speechMeans 8000.
//	216:24 ARITHMETIC_BASE/INVERT gap = minDiff[ch] - diff             (- -> +):
//	       gap 871 -> 217, sShift 2830 -> 705, speechMeans[0] 10830 -> 8705.
//	217:25 ARITHMETIC_BASE        sShift = (13 * gap) >> 2             (* -> /):
//	       13/871 = 0 -> sShift 0 -> speechMeans[0] 10830 -> 8000.
//	218:24 ARITHMETIC_BASE        nShift = (3 * gap) >> 2              (* -> /):
//	       3/871 = 0 -> nShift 0 -> noiseMeans[0] 8469 -> 9122.
//	220:24 ARITHMETIC_BASE        speechMeans[ch+k*vadNumCh] += sShift (* -> /):
//	       index k/6 collapses both gaussians onto ch -> speechMeans[6] 10830 -> 8000.
//	221:23 ARITHMETIC_BASE        noiseMeans[ch+k*vadNumCh] -= nShift  (* -> /):
//	       index collapse -> noiseMeans[6] 8597 -> 9250.
//	235:10 CONDITIONALS_NEGATION  if nG > maxNoise[ch]                 (> -> <=):
//	       9310 <= 9216 false -> noise clamp skipped -> noiseMeans[0] 8469 -> 8563.
//	236:13 ARITHMETIC_BASE/INVERT d = nG - maxNoise[ch]                (- -> +):
//	       d 94 -> 18526 -> noiseMeans[0] crashes far below 8469.
//	238:23 ARITHMETIC_BASE        noiseMeans[ch+k*vadNumCh] -= d       (* -> /):
//	       index collapse -> noiseMeans[6] 8597 -> 8691.
//	242:17 INCREMENT_DECREMENT    v.frameCounter++                     (++ -> --):
//	       frameCounter 1 -> -1.
func TestGkU4_SeparationAndNoiseClampStatePin(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	v.globalThresh = 32767 // suppress the global-sum trigger -> vadflag 0
	v.localThresh = 32767  // suppress the per-channel trigger -> vadflag 0
	gk_subflux_u4_setAll(&v.noiseMeans, 12000)
	gk_subflux_u4_setAll(&v.speechMeans, 8000)
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u4_feat, 100)
	gk_subflux_u4_assertModel(t, "sep", flag, llr, v.frameCounter, v.noiseMeans, v.speechMeans,
		0, 1546, 1,
		[vadTableSize]int16{8469, 8398, 8282, 8171, 8052, 7915, 8597, 8526, 8410, 8299, 8180, 8043},
		[vadTableSize]int16{10830, 10704, 10697, 10596, 10502, 10421, 10830, 10704, 10697, 10596, 10502, 10421},
	)
}

// TestGkU4_SpeechClampStatePin drives the speech-mean clamp (FIRES: sG=14000 >
// maxSpeech[ch], so each speechMeans is pulled down to exactly maxSpeech[ch])
// with vadflag forced 0 and separation NOT firing. The pinned speechMeans kill:
//
//	228:10 CONDITIONALS_NEGATION  if sG > maxs           (> -> <=):
//	       14000 <= 11392 false -> clamp skipped -> speechMeans[0] 11392 -> 14000.
//	229:13 ARITHMETIC_BASE/INVERT d = sG - maxs          (- -> +):
//	       d 2608 -> 25392 -> speechMeans[0] 11392 -> -11392.
//	231:24 ARITHMETIC_BASE        speechMeans[ch+k*vadNumCh] -= d  (* -> /):
//	       index k/6 collapses both gaussians onto ch -> speechMeans[6] 11392 -> 14000.
func TestGkU4_SpeechClampStatePin(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	v.globalThresh = 32767
	v.localThresh = 32767
	gk_subflux_u4_setAll(&v.speechMeans, 14000)
	gk_subflux_u4_setAll(&v.noiseMeans, 8000)
	flag, llr := v.gmmProbabilityLLR(gk_subflux_u4_feat, 100)
	gk_subflux_u4_assertModel(t, "speechClamp", flag, llr, v.frameCounter, v.noiseMeans, v.speechMeans,
		0, -1696, 1,
		[vadTableSize]int16{9122, 9022, 8904, 8770, 8629, 8473, 9250, 9150, 9032, 8898, 8757, 8601},
		[vadTableSize]int16{11392, 11392, 11520, 11520, 11520, 11520, 11392, 11392, 11520, 11520, 11520, 11520},
	)
}

// TestGkU4_OverhangZeroFlag closes the energy gate (totalPower 0 <= minEnergy)
// so vadflag stays 0, then enters the overhang block with a preset overHang.
// Kills:
//
//	248:16 ARITHMETIC_BASE     vadflag = 2 + v.overHang  (+ -> -):
//	       2 + 5 = 7 vs 2 - 5 = -3.
//	249:14 INCREMENT_DECREMENT v.overHang--              (-- -> ++):
//	       5 -> 4 vs 5 -> 6.
//
// frameCounter == 0 proves the energy gate stayed closed (the GMM block, and
// its frameCounter++, never ran).
func TestGkU4_OverhangZeroFlag(t *testing.T) {
	v := newVADInst(ModeVeryAggressive)
	v.overHang = 5
	flag, _ := v.gmmProbabilityLLR(gk_subflux_u4_feat, 0)
	if flag != 7 {
		t.Errorf("flag = %d, want 7 (2 + overHang 5)", flag)
	}
	if v.overHang != 4 {
		t.Errorf("overHang = %d, want 4 (5--)", v.overHang)
	}
	if v.numOfSpeech != 0 {
		t.Errorf("numOfSpeech = %d, want 0", v.numOfSpeech)
	}
	if v.frameCounter != 0 {
		t.Errorf("frameCounter = %d, want 0 (energy gate closed)", v.frameCounter)
	}
}

// TestGkU4_OverhangSpeechCount forces vadflag 1 (globalThresh = -32768 makes
// sumLLR >= globalThresh always true) so the overhang else-branch runs, and
// drives v.numOfSpeech past / below the threshold. Kills:
//
//	253:16 INCREMENT_DECREMENT v.numOfSpeech++                 (++ -> --):
//	       below: 1 -> 2 (pinned) vs 1 -> 0; at: 6 -> clamp 6 (pinned) vs 6 -> 5.
//	254:20 CONDITIONALS_NEGATION if v.numOfSpeech > vadMaxSpeechFr (> -> <=):
//	       below row (2 > 6 false): original takes else (numOfSpeech 2, overHang
//	       overHangMax1=6); mutant takes if (numOfSpeech clamped to 6, overHang
//	       overHangMax2=9). at row (7 > 6 true): original takes if (numOfSpeech
//	       6, overHang 9); mutant takes else (numOfSpeech 7, overHang 6).
//	       The condition is exercised both true and false across the two rows.
func TestGkU4_OverhangSpeechCount(t *testing.T) {
	cases := []struct {
		name         string
		presetSpeech int16
		wantSpeech   int16
		wantOverHang int16
	}{
		{"belowThreshold", 1, 2, 6}, // 1++ ->2; 2>6 false -> overHangMax1 (6)
		{"atThreshold", 6, 6, 9},    // 6++ ->7; 7>6 true -> clamp to 6, overHangMax2 (9)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := newVADInst(ModeVeryAggressive)
			v.globalThresh = -32768 // force vadflag 1 (sumLLR >= globalThresh always)
			v.numOfSpeech = tc.presetSpeech
			flag, _ := v.gmmProbabilityLLR(gk_subflux_u4_feat, 100)
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
