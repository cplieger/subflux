// Package fft provides a radix-2 Cooley-Tukey FFT implementation for
// cross-correlation in the subsync package.
package fft

import (
	"math"
	"math/cmplx"
	"sync"
)

// WorkspacePool reuses allocated complex128 slices across correlations
// to reduce GC pressure from large FFT buffers.
var WorkspacePool = sync.Pool{
	New: func() any { return &Workspace{} },
}

// Workspace holds reusable buffers for FFT cross-correlation.
type Workspace struct {
	A, B, Product []complex128
}

// Ensure grows workspace buffers to at least n elements, reusing capacity.
func (w *Workspace) Ensure(n int) {
	if cap(w.A) < n {
		w.A = make([]complex128, n)
	} else {
		w.A = w.A[:n]
	}
	if cap(w.B) < n {
		w.B = make([]complex128, n)
	} else {
		w.B = w.B[:n]
	}
	if cap(w.Product) < n {
		w.Product = make([]complex128, n)
	} else {
		w.Product = w.Product[:n]
	}
}

// NextPow2 returns the smallest power of 2 >= n.
func NextPow2(n int) int {
	p := 1
	for p < n {
		p <<= 1
	}
	return p
}

// FFT computes the discrete Fourier transform in-place using the iterative
// Cooley-Tukey radix-2 decimation-in-time algorithm. Input length must be
// a power of 2. Variable names follow standard DSP convention.
func FFT(x []complex128) []complex128 {
	n := len(x)
	if n <= 1 {
		return x
	}
	j := 0
	for i := 1; i < n; i++ {
		bit := n >> 1
		for j&bit != 0 {
			j ^= bit
			bit >>= 1
		}
		j ^= bit
		if i < j {
			x[i], x[j] = x[j], x[i] //nolint:gosec // G602: bit-reversal permutation, i,j < n == len(x) (n is power of two)
		}
	}
	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		wn := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			w := complex(1, 0)
			for k := range half {
				u := x[start+k]
				v := w * x[start+k+half]
				x[start+k] = u + v
				x[start+k+half] = u - v
				w *= wn
			}
		}
	}
	return x
}

// IFFT computes the inverse discrete Fourier transform by conjugating,
// applying the forward FFT, then conjugating and scaling.
func IFFT(x []complex128) []complex128 {
	n := len(x)
	for i := range x {
		x[i] = cmplx.Conj(x[i])
	}
	x = FFT(x)
	scale := complex(1.0/float64(n), 0)
	for i := range x {
		x[i] = cmplx.Conj(x[i]) * scale
	}
	return x
}
