package fft

import (
	"math"
	"testing"
)

func FuzzFFTRoundtrip(f *testing.F) {
	f.Add(1.0, 2.0, 3.0, 4.0)
	f.Add(0.0, 0.0, 0.0, 0.0)
	f.Add(-1.5, 2.7, -0.3, 8.1)

	f.Fuzz(func(t *testing.T, r0, r1, r2, r3 float64) {
		if math.IsNaN(r0) || math.IsInf(r0, 0) ||
			math.IsNaN(r1) || math.IsInf(r1, 0) ||
			math.IsNaN(r2) || math.IsInf(r2, 0) ||
			math.IsNaN(r3) || math.IsInf(r3, 0) {
			return
		}
		orig := []complex128{complex(r0, 0), complex(r1, 0), complex(r2, 0), complex(r3, 0)}
		input := make([]complex128, 4)
		copy(input, orig)

		freq := FFT(input)
		result := IFFT(freq)

		const tol = 1e-6
		for i, v := range result {
			if math.Abs(real(v)-real(orig[i])) > tol || math.Abs(imag(v)) > tol {
				t.Fatalf("FFT/IFFT roundtrip mismatch at [%d]: got %v, want %v", i, v, orig[i])
			}
		}
	})
}

func FuzzNextPow2(f *testing.F) {
	f.Add(1)
	f.Add(2)
	f.Add(3)
	f.Add(1023)
	f.Add(1024)

	f.Fuzz(func(t *testing.T, n int) {
		if n <= 0 || n > 1<<20 {
			return
		}
		p := NextPow2(n)
		if p < n {
			t.Fatalf("NextPow2(%d) = %d, want >= %d", n, p, n)
		}
		if p&(p-1) != 0 {
			t.Fatalf("NextPow2(%d) = %d, not a power of 2", n, p)
		}
		if p > 1 && p/2 >= n {
			t.Fatalf("NextPow2(%d) = %d, but %d also works", n, p, p/2)
		}
	})
}
