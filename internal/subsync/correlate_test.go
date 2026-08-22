package subsync

import (
	"math"
	"math/cmplx"
	"testing"

	"github.com/cplieger/subflux/internal/subsync/fft"
	"pgregory.net/rapid"
)

func TestFloatEnergy(t *testing.T) {
	t.Parallel()
	signal := []float64{3, 4}
	got := floatEnergy(signal)
	// 3*3 + 4*4 = 9 + 16 = 25
	if got != 25 {
		t.Errorf("floatEnergy([3,4]) = %v, want 25", got)
	}
}

func TestFloatEnergy_empty(t *testing.T) {
	t.Parallel()
	got := floatEnergy(nil)
	if got != 0 {
		t.Errorf("floatEnergy(nil) = %v, want 0", got)
	}
}

func TestNextPow2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input, want int
	}{
		{1, 1},
		{2, 2},
		{3, 4},
		{4, 4},
		{5, 8},
		{7, 8},
		{8, 8},
		{9, 16},
		{100, 128},
		{1024, 1024},
		{1025, 2048},
	}
	for _, tt := range tests {
		if got := fft.NextPow2(tt.input); got != tt.want {
			t.Errorf("fft.NextPow2(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

func TestFFT_single_element(t *testing.T) {
	t.Parallel()
	input := []complex128{complex(42, 0)}
	got := fft.FFT(input)
	if len(got) != 1 || got[0] != complex(42, 0) {
		t.Errorf("fft.FFT([42]) = %v, want [(42+0i)]", got)
	}
}

func TestIFFT_single_element(t *testing.T) {
	t.Parallel()
	input := []complex128{complex(42, 0)}
	got := fft.IFFT(input)
	if len(got) != 1 || got[0] != complex(42, 0) {
		t.Errorf("fft.IFFT([42]) = %v, want [(42+0i)]", got)
	}
}

func TestFFT_IFFT_roundtrip(t *testing.T) {
	t.Parallel()
	input := []complex128{
		complex(1, 0), complex(2, 0), complex(3, 0), complex(4, 0),
	}
	original := make([]complex128, len(input))
	copy(original, input)

	transformed := fft.FFT(input)
	recovered := fft.IFFT(transformed)

	for i := range original {
		diff := cmplx.Abs(original[i] - recovered[i])
		if diff > 1e-10 {
			t.Fatalf("index %d: expected %v, got %v (diff %f)",
				i, original[i], recovered[i], diff)
		}
	}
}

// PBT: FFT/IFFT round-trip preserves signal.
func TestFFT_IFFT_roundtrip_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		// Power of 2 length.
		exp := rapid.IntRange(1, 8).Draw(t, "exp")
		n := 1 << exp
		input := make([]complex128, n)
		for i := range input {
			re := rapid.Float64Range(-1000, 1000).Draw(t, "re")
			input[i] = complex(re, 0)
		}
		original := make([]complex128, n)
		copy(original, input)

		transformed := fft.FFT(input)
		recovered := fft.IFFT(transformed)

		for i := range original {
			diff := cmplx.Abs(original[i] - recovered[i])
			if diff > 1e-6 {
				t.Fatalf("index %d: diff %f exceeds tolerance", i, diff)
			}
		}
	})
}

// PBT: fft.NextPow2 result is always >= input and always a power of 2.
func TestNextPow2_invariants(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(1, 100000).Draw(t, "n")
		got := fft.NextPow2(n)
		if got < n {
			t.Fatalf("fft.NextPow2(%d) = %d, which is less than input", n, got)
		}
		// Check power of 2: got & (got-1) == 0.
		if got&(got-1) != 0 {
			t.Fatalf("fft.NextPow2(%d) = %d, which is not a power of 2", n, got)
		}
	})
}

func TestCrossCorrelateEdges_empty(t *testing.T) {
	t.Parallel()
	result := crossCorrelateEdges(t.Context(), nil, nil)
	if result.OffsetFrames != 0 || result.Peak != 0 || result.OffsetMs != 0 {
		t.Fatalf("CrossCorrelateEdges(nil, nil) = {offset=%d, peak=%f, ms=%d}, want all zero",
			result.OffsetFrames, result.Peak, result.OffsetMs)
	}
}

func TestCrossCorrelateEdges_one_empty(t *testing.T) {
	t.Parallel()
	a := []float64{1.0, -1.0, 1.0}
	result := crossCorrelateEdges(t.Context(), a, nil)
	if result.OffsetFrames != 0 || result.Peak != 0 || result.OffsetMs != 0 {
		t.Fatalf("CrossCorrelateEdges(a, nil) = {offset=%d, peak=%f, ms=%d}, want all zero",
			result.OffsetFrames, result.Peak, result.OffsetMs)
	}
}

func TestCrossCorrelateEdges_caps_long_signals(t *testing.T) {
	t.Parallel()
	n := maxCorrelationFrames + 100
	a := make([]float64, n)
	b := make([]float64, n)
	for i := 100; i < 200; i++ {
		a[i] = 1.0
		b[i] = 1.0
	}
	result := crossCorrelateEdges(t.Context(), a, b)
	if result.Peak < 0 || result.Peak > 1 {
		t.Errorf("CrossCorrelateEdges(capped) peak = %f, want in [0, 1]", result.Peak)
	}
}

func TestCrossCorrelateEdges_zero_energy(t *testing.T) {
	t.Parallel()
	a := []float64{0, 0, 0, 0}
	b := []float64{0, 0, 0, 0}
	result := crossCorrelateEdges(t.Context(), a, b)
	if result.Peak != 0 {
		t.Errorf("CrossCorrelateEdges(zeros) peak = %f, want 0", result.Peak)
	}
	if result.OffsetFrames != 0 {
		t.Errorf("CrossCorrelateEdges(zeros) offset = %d, want 0", result.OffsetFrames)
	}
}

func TestCrossCorrelateEdges_known_offset(t *testing.T) {
	t.Parallel()
	// Signal a: activity at frames 20-40.
	a := make([]float64, 100)
	for i := 20; i < 40; i++ {
		a[i] = 1.0
	}
	// Signal b: same activity shifted by 10 frames.
	b := make([]float64, 100)
	for i := 30; i < 50; i++ {
		b[i] = 1.0
	}
	result := crossCorrelateEdges(t.Context(), a, b)
	if math.Abs(float64(result.OffsetFrames)-(-10)) > 2 {
		t.Errorf("CrossCorrelateEdges(known offset): OffsetFrames = %d, want ~-10",
			result.OffsetFrames)
	}
	if result.Peak < 0.5 {
		t.Errorf("CrossCorrelateEdges(known offset): peak = %f, want > 0.5", result.Peak)
	}
}

func TestCrossCorrelateEdges_parabolic_interpolation(t *testing.T) {
	t.Parallel()
	a := make([]float64, 200)
	for i := 40; i < 80; i++ {
		a[i] = 1.0
	}
	b := make([]float64, 200)
	for i := 50; i < 92; i++ {
		b[i] = 1.0
	}
	result := crossCorrelateEdges(t.Context(), a, b)
	// The integer offset should be close to -10.
	if math.Abs(float64(result.OffsetFrames)-(-10)) > 3 {
		t.Fatalf("parabolic interpolation test: OffsetFrames = %d, want ~-10",
			result.OffsetFrames)
	}
	// OffsetMs should be within half a frame of OffsetFrames * frameMs.
	wantMs := int64(result.OffsetFrames) * frameMs
	diff := result.OffsetMs - wantMs
	if diff < 0 {
		diff = -diff
	}
	if diff > frameMs/2 {
		t.Fatalf("parabolic interpolation: OffsetMs = %d, want ~%d (±%d)",
			result.OffsetMs, wantMs, frameMs/2)
	}
}

// PBT: CrossCorrelateEdges peak is in [0, 1] for random float64 signals.
func TestCrossCorrelateEdges_peak_bounded(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(10, 200).Draw(t, "n")
		a := make([]float64, n)
		b := make([]float64, n)
		for i := range n {
			a[i] = rapid.Float64Range(-1, 1).Draw(t, "a")
			b[i] = rapid.Float64Range(-1, 1).Draw(t, "b")
		}
		result := crossCorrelateEdges(t.Context(), a, b)
		if result.Peak < 0 || result.Peak > 1.0 {
			t.Fatalf("CrossCorrelateEdges peak %f out of [0, 1] range", result.Peak)
		}
	})
}

func TestCrossCorrelateEdges_identical_signals(t *testing.T) {
	t.Parallel()
	signal := make([]float64, 100)
	for i := 20; i < 40; i++ {
		signal[i] = 1.0
	}
	result := crossCorrelateEdges(t.Context(), signal, signal)
	if result.OffsetFrames != 0 {
		t.Errorf("CrossCorrelateEdges(identical): OffsetFrames = %d, want 0", result.OffsetFrames)
	}
	if result.Peak < 0.9 {
		t.Errorf("CrossCorrelateEdges(identical): Peak = %f, want >= 0.9", result.Peak)
	}
}

func TestCrossCorrelateEdges_offset_ms_consistent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		n := rapid.IntRange(10, 200).Draw(t, "n")
		a := make([]float64, n)
		b := make([]float64, n)
		for i := range n {
			a[i] = rapid.Float64Range(-1, 1).Draw(t, "a")
			b[i] = rapid.Float64Range(-1, 1).Draw(t, "b")
		}
		result := crossCorrelateEdges(t.Context(), a, b)
		wantMs := int64(result.OffsetFrames) * frameMs
		diff := result.OffsetMs - wantMs
		if diff < 0 {
			diff = -diff
		}
		if diff > frameMs/2 {
			t.Fatalf("CrossCorrelateEdges: OffsetMs = %d, want ~OffsetFrames(%d) * %d = %d (diff %d > %d)",
				result.OffsetMs, result.OffsetFrames, frameMs, wantMs, diff, frameMs/2)
		}
	})
}

func TestFloatEnergy_negative_values(t *testing.T) {
	t.Parallel()
	signal := []float64{-3, -4}
	got := floatEnergy(signal)
	if got != 25 {
		t.Errorf("floatEnergy([-3,-4]) = %v, want 25", got)
	}
}

// The correlation peak is refined between frames, so the reported offset is
// not restricted to whole multiples of the frame duration. Here the peak sits
// at lag 0 (3*1 + 2*2 = 7) with unequal neighbours — the lag -1 overlap is
// 3*2 = 6 and the lag +1 overlap is 2*1 + 1*2 = 4 — which pulls the true peak
// a quarter of a frame towards the stronger side: 0.25 * 10ms = 3ms.
func TestCrossCorrelateEdges_refines_the_peak_between_frames(t *testing.T) {
	t.Parallel()
	a := []float64{3, 2, 1, 0}
	b := []float64{1, 2}

	got := crossCorrelateEdges(t.Context(), a, b)

	if got.OffsetFrames != 0 {
		t.Errorf("crossCorrelateEdges(%v, %v).OffsetFrames = %d, want 0", a, b, got.OffsetFrames)
	}
	if got.OffsetMs != 3 {
		t.Errorf("crossCorrelateEdges(%v, %v).OffsetMs = %d, want 3", a, b, got.OffsetMs)
	}
}

// Sub-frame refinement applies strictly inside a half-frame window. A peak
// exactly half a frame from its neighbour is equally close to both, so the
// whole-frame lag stands rather than being nudged either way.
func TestCrossCorrelateEdges_ignores_a_half_frame_refinement(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		a, b []float64
	}{
		// Lag 0 and lag +1 both overlap with weight 2, so the peak is as
		// close to the following frame as to its own.
		{name: "tie_with_the_following_frame", a: []float64{1, 1, 1}, b: []float64{1, 1}},
		// Lag 0 and lag -1 both overlap with weight 3.
		{name: "tie_with_the_preceding_frame", a: []float64{1, 0}, b: []float64{3, 3}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := crossCorrelateEdges(t.Context(), tt.a, tt.b)
			if got.OffsetFrames != 0 {
				t.Errorf("crossCorrelateEdges(%v, %v).OffsetFrames = %d, want 0",
					tt.a, tt.b, got.OffsetFrames)
			}
			if got.OffsetMs != 0 {
				t.Errorf("crossCorrelateEdges(%v, %v).OffsetMs = %d, want 0",
					tt.a, tt.b, got.OffsetMs)
			}
		})
	}
}

// The peak is normalized by the energy of both signals, so it reports how well
// the two shapes agree and not how loud they are: a signal against itself
// scores 1 at any amplitude. Tolerance 1e-9 covers the FFT round trip, whose
// last bits are not portable (Go may fuse a multiply and an add).
func TestCrossCorrelateEdges_peak_is_amplitude_invariant(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		amp  float64
	}{
		{name: "unit_amplitude", amp: 1.0},
		{name: "quiet_amplitude", amp: 0.2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			signal := make([]float64, 100)
			for i := 20; i < 40; i++ {
				signal[i] = tt.amp
			}

			got := crossCorrelateEdges(t.Context(), signal, signal)

			if math.Abs(got.Peak-1.0) > 1e-9 {
				t.Errorf("crossCorrelateEdges(signal, signal).Peak = %v for amplitude %v, want 1",
					got.Peak, tt.amp)
			}
			if got.OffsetFrames != 0 {
				t.Errorf("crossCorrelateEdges(signal, signal).OffsetFrames = %d for amplitude %v, want 0",
					got.OffsetFrames, tt.amp)
			}
		})
	}
}

// A signal with no energy cannot agree with anything, so the peak is zero
// rather than an undefined ratio.
func TestCrossCorrelateEdges_silent_signal_scores_zero(t *testing.T) {
	t.Parallel()
	silent := make([]float64, 8)
	loud := []float64{1, -1, 1, -1, 1, -1, 1, -1}
	tests := []struct {
		name string
		a, b []float64
	}{
		{name: "silent_audio", a: silent, b: loud},
		{name: "silent_reference", a: loud, b: silent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := crossCorrelateEdges(t.Context(), tt.a, tt.b)
			if got.Peak != 0 {
				t.Errorf("crossCorrelateEdges(%v, %v).Peak = %v, want 0", tt.a, tt.b, got.Peak)
			}
		})
	}
}

// Two equal-length signals that oppose each other at every overlapping lag
// correlate best where they do not overlap at all. That lag is reported as a
// forward offset of the signal length, and the peak stays zero because nothing
// actually matched.
func TestCrossCorrelateEdges_opposed_signals_report_a_forward_lag(t *testing.T) {
	t.Parallel()
	const n = 64
	high := make([]float64, n)
	low := make([]float64, n)
	for i := range n {
		high[i] = 1
		low[i] = -1
	}

	got := crossCorrelateEdges(t.Context(), high, low)

	if got.OffsetFrames != n {
		t.Errorf("crossCorrelateEdges(+1 x %d, -1 x %d).OffsetFrames = %d, want %d",
			n, n, got.OffsetFrames, n)
	}
	if got.OffsetMs != n*frameMs {
		t.Errorf("crossCorrelateEdges(+1 x %d, -1 x %d).OffsetMs = %d, want %d",
			n, n, got.OffsetMs, n*frameMs)
	}
	if got.Peak != 0 {
		t.Errorf("crossCorrelateEdges(+1 x %d, -1 x %d).Peak = %v, want 0", n, n, got.Peak)
	}
}
