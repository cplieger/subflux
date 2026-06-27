package vad

import (
	"math"
	"testing"

	"pgregory.net/rapid"
)

// --- vadAllpass ---

// TestVadAllpass_filter_output pins the all-pass section's per-sample output and
// its final carried state for a known strided input. The section is a
// fixed-point difference equation, so the exact outputs and state are captured
// from the reference port.
func TestVadAllpass_filter_output(t *testing.T) {
	t.Parallel()
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

// TestVadLogEnergy_high_energy_normalization pins the outputs for a high-energy
// frame (all samples 1000), exercising the NormU32 normalization and the
// rshifts >= 0 total-energy path.
func TestVadLogEnergy_high_energy_normalization(t *testing.T) {
	t.Parallel()
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

// TestVadLogEnergy_low_energy_shift pins the outputs for a very-low-energy frame
// (all samples 1), exercising the normR < 0 left-shift branch and the
// rshifts < 0 total-energy branch.
func TestVadLogEnergy_low_energy_shift(t *testing.T) {
	t.Parallel()
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

// TestVadLogEnergy_rshifts_zero pins the total-energy clamp on the rshifts == 0
// path: an all-16 frame normalizes to rshifts 0, so totalE is clamped to
// vadMinEnergy+1 (11).
func TestVadLogEnergy_rshifts_zero(t *testing.T) {
	t.Parallel()
	data := make([]int16, 80)
	for i := range data {
		data[i] = 16
	}
	_, totalE := vadLogEnergy(data, 80, 368)
	if totalE != 11 {
		t.Errorf("vadLogEnergy(all-16) totalE = %d, want 11 (vadMinEnergy+1 on the rshifts==0 path)", totalE)
	}
}

func TestVadLogEnergy_overflow(t *testing.T) {
	t.Parallel()
	// All samples at max amplitude trigger the energy-accumulation overflow
	// branch (80 × 32767² exceeds uint32), which renormalizes by right-shifting.
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
