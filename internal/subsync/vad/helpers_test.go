package vad

import "testing"

// Shared GMM test fixtures and model-state assertion helpers used across the
// classifier test files. Each feature vector is crafted so that its Q7 form
// (input<<3) lands near the noise or speech Gaussian means, driving the
// classifier down a known branch.
var (
	featNoise       = [vadNumCh]int16{842, 611, 883, 839, 846, 421}      // -> vadflag 0
	featSpeech      = [vadNumCh]int16{1038, 1261, 1260, 1478, 1480, 789} // -> vadflag 1
	featUniformLow  = [vadNumCh]int16{50, 50, 50, 50, 50, 50}
	featUniformMid  = [vadNumCh]int16{3200, 3200, 3200, 3200, 3200, 3200}
	featUniform1000 = [vadNumCh]int16{1000, 1000, 1000, 1000, 1000, 1000}
)

// setAllGMM fills a 12-element GMM parameter array with one value.
func setAllGMM(a *[vadTableSize]int16, val int16) {
	for i := range a {
		a[i] = val
	}
}

// assertGMMState pins the full observable result of one gmmProbabilityLLR call:
// the returned flag and llr, the frame counter, and all four GMM model arrays.
// These are reference-vector (characterization) assertions: the exact values
// were captured from the known-correct port, so any change to the fixed-point
// arithmetic or branch structure shifts at least one pinned value.
func assertGMMState(t *testing.T, name string, v *vadInst, flag int16, llr float64,
	wantFlag int16, wantLLR float64, wantFC int32,
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

// assertGMMStds pins the standard-deviation update in isolation. The returned
// flag proves the intended speech/noise branch ran and frameCounter==1 proves
// the model-update block executed; the std-update arithmetic is downstream of
// flag/frameCounter/means, so any change to it lands in the std array.
func assertGMMStds(t *testing.T, name string, gotFlag, wantFlag int16,
	gotFC, wantFC int32, gotStd, wantStd [vadTableSize]int16,
) {
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

// assertGMMMeans pins the model-separation and mean-clamping result: the
// returned flag/llr, the frame counter, and the two mean arrays that the
// separation/clamp block adjusts.
func assertGMMMeans(t *testing.T, name string, gotFlag int16, gotLLR float64, gotFC int32,
	gotNM, gotSM [vadTableSize]int16,
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
