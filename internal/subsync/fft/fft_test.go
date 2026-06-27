package fft

import (
	"math"
	"testing"
)

// cplxClose reports whether two complex128 values are within tol.
func cplxClose(a, b complex128, tol float64) bool {
	return math.Abs(real(a)-real(b)) <= tol && math.Abs(imag(a)-imag(b)) <= tol
}

// TestFFT_knownTransform pins the exact DFT of [1,2,3,4] = [10, -2+2i, -2,
// -2-2i]. The size-4 butterfly stage's twiddle factor drives X[1] and X[3], so
// any sign or arithmetic error in the twiddle exponent shifts those bins — a
// sign flip, for instance, turns the forward transform into the unscaled
// inverse and makes X[1] = -2-2i.
func TestFFT_knownTransform(t *testing.T) {
	in := []complex128{complex(1, 0), complex(2, 0), complex(3, 0), complex(4, 0)}
	got := FFT(in)

	want := []complex128{complex(10, 0), complex(-2, 2), complex(-2, 0), complex(-2, -2)}
	const tol = 1e-9
	if len(got) != len(want) {
		t.Fatalf("FFT([1,2,3,4]) length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if !cplxClose(got[i], want[i], tol) {
			t.Errorf("FFT([1,2,3,4])[%d] = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestFFT_smallInputs covers the n<=1 short-circuit and the size-2 butterfly,
// whose twiddle factor is unity so the result is simply [a+b, a-b].
func TestFFT_smallInputs(t *testing.T) {
	// n == 1: returned unchanged.
	one := FFT([]complex128{complex(7, 0)})
	if len(one) != 1 || !cplxClose(one[0], complex(7, 0), 1e-12) {
		t.Errorf("FFT([7]) = %v, want [7]", one)
	}

	// n == 2: [a+b, a-b].
	two := FFT([]complex128{complex(1, 0), complex(3, 0)})
	want := []complex128{complex(4, 0), complex(-2, 0)}
	for i := range want {
		if !cplxClose(two[i], want[i], 1e-12) {
			t.Errorf("FFT([1,3])[%d] = %v, want %v", i, two[i], want[i])
		}
	}
}
