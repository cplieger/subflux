package subsync

import (
	"context"
	"math/rand/v2"
	"testing"

	"subflux/internal/subsync/fft"
)

func generateSignal(n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = rand.Float64()*2 - 1
	}
	return s
}

func BenchmarkCrossCorrelateEdges(b *testing.B) {
	cases := []struct {
		name string
		lenA int
		lenB int
	}{
		{"Short_1000", 1000, 1000},
		{"Medium_10000", 10000, 10000},
		{"Long_100000", 100000, 100000},
		{"Asymmetric_5000x50000", 5000, 50000},
	}
	for _, tc := range cases {
		a := generateSignal(tc.lenA)
		bSig := generateSignal(tc.lenB)
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				crossCorrelateEdges(context.Background(), a, bSig)
			}
		})
	}
}

func BenchmarkFFT(b *testing.B) {
	sizes := []int{256, 1024, 4096}
	for _, n := range sizes {
		x := make([]complex128, n)
		for i := range x {
			x[i] = complex(rand.Float64()*2-1, 0)
		}
		b.Run(sizeLabel(n), func(b *testing.B) {
			b.ReportAllocs()
			buf := make([]complex128, n)
			for range b.N {
				copy(buf, x)
				fft.FFT(buf)
			}
		})
	}
}

func sizeLabel(n int) string {
	switch {
	case n >= 4096:
		return "4096"
	case n >= 1024:
		return "1024"
	default:
		return "256"
	}
}
