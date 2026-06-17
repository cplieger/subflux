package fft

import (
	"math"
	"testing"
)

// gk_subflux_u28_cplxClose reports whether two complex128 values are within tol.
func gk_subflux_u28_cplxClose(a, b complex128, tol float64) bool {
	return math.Abs(real(a)-real(b)) <= tol && math.Abs(imag(a)-imag(b)) <= tol
}

// Test_gk_subflux_u28_FFT_knownTransform pins the exact DFT of [1,2,3,4] =
// [10, -2+2i, -2, -2-2i]. The size-4 butterfly stage uses the twiddle factor
// wn = cmplx.Exp(complex(0, -2*math.Pi/float64(size))) (fft.go:72), so every
// mutant on that line changes X[1]/X[3]:
//   - 72:30 INVERT_NEGATIVES / ARITHMETIC_BASE flip the exponent sign,
//     turning the forward transform into the (unscaled) inverse, so X[1]
//     becomes -2-2i instead of -2+2i.
//   - 72:32 ARITHMETIC_BASE (* -> /) changes the angle to -2/(pi*size).
//   - 72:40 ARITHMETIC_BASE (/ -> *) makes the size-4 twiddle exp(-i*8*pi)=1.
func Test_gk_subflux_u28_FFT_knownTransform(t *testing.T) {
	in := []complex128{complex(1, 0), complex(2, 0), complex(3, 0), complex(4, 0)}
	got := FFT(in)

	want := []complex128{complex(10, 0), complex(-2, 2), complex(-2, 0), complex(-2, -2)}
	const tol = 1e-9
	if len(got) != len(want) {
		t.Fatalf("FFT([1,2,3,4]) length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !gk_subflux_u28_cplxClose(got[i], want[i], tol) {
			t.Errorf("FFT([1,2,3,4])[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// Test_gk_subflux_u28_FFT_smallInputs documents the short-circuit (n<=1) and
// bit-reversal paths. These exercise but cannot kill the equivalent mutants
// at 55:7, 59:16, and 66:8 (see the unit report for the equivalence proofs).
func Test_gk_subflux_u28_FFT_smallInputs(t *testing.T) {
	// n == 1: returned unchanged.
	one := FFT([]complex128{complex(7, 0)})
	if len(one) != 1 || !gk_subflux_u28_cplxClose(one[0], complex(7, 0), 1e-12) {
		t.Errorf("FFT([7]) = %v, want [7]", one)
	}

	// n == 2: [a+b, a-b]. The twiddle factor is unused at size 2.
	two := FFT([]complex128{complex(1, 0), complex(3, 0)})
	want := []complex128{complex(4, 0), complex(-2, 0)}
	for i := range want {
		if !gk_subflux_u28_cplxClose(two[i], want[i], 1e-12) {
			t.Errorf("FFT([1,3])[%d] = %v, want %v", i, two[i], want[i])
		}
	}
}
