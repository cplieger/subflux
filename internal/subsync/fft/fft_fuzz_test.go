package fft

import (
	"math"
	"testing"
)

func FuzzFFTRoundtrip(f *testing.F) {
	f.Add([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0x3F, 0xF0, 0, 0, 0, 0, 0, 0})
	f.Add([]byte{})
	f.Add([]byte{0x40, 0x00, 0, 0, 0, 0, 0, 0, 0x40, 0x08, 0, 0, 0, 0, 0, 0, 0x40, 0x10, 0, 0, 0, 0, 0, 0, 0x40, 0x14, 0, 0, 0, 0, 0, 0})

	f.Fuzz(func(t *testing.T, data []byte) {
		// Build a power-of-2-length slice from the fuzz data.
		// Each complex128 needs 16 bytes (2×float64).
		nComplex := len(data) / 16
		if nComplex < 1 {
			return
		}
		n := NextPow2(nComplex)
		if n > 1024 {
			return // cap size for speed
		}

		input := make([]complex128, n)
		for i := range min(nComplex, n) {
			re := math.Float64frombits(uint64(data[i*16])<<56 | uint64(data[i*16+1])<<48 |
				uint64(data[i*16+2])<<40 | uint64(data[i*16+3])<<32 |
				uint64(data[i*16+4])<<24 | uint64(data[i*16+5])<<16 |
				uint64(data[i*16+6])<<8 | uint64(data[i*16+7]))
			im := math.Float64frombits(uint64(data[i*16+8])<<56 | uint64(data[i*16+9])<<48 |
				uint64(data[i*16+10])<<40 | uint64(data[i*16+11])<<32 |
				uint64(data[i*16+12])<<24 | uint64(data[i*16+13])<<16 |
				uint64(data[i*16+14])<<8 | uint64(data[i*16+15]))
			if math.IsNaN(re) || math.IsInf(re, 0) || math.IsNaN(im) || math.IsInf(im, 0) {
				return
			}
			input[i] = complex(re, im)
		}

		// Forward FFT must not panic.
		freq := FFT(input)
		if len(freq) != n {
			t.Fatalf("FFT returned %d elements, want %d", len(freq), n)
		}

		// Inverse must not panic and must return same length.
		recovered := IFFT(freq)
		if len(recovered) != n {
			t.Fatalf("IFFT returned %d elements, want %d", len(recovered), n)
		}
	})
}
