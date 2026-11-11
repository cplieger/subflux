package search

import (
	"encoding/binary"
	"errors"
	"testing"
)

// FuzzVideoHash_oracle checks the hash against the published algorithm
// computed directly over the bytes.
func FuzzVideoHash_oracle(f *testing.F) {
	f.Add([]byte{})
	f.Add(make([]byte, 100))
	f.Add(make([]byte, 2*hashBlock))
	f.Add(make([]byte, 2*hashBlock+1))
	f.Fuzz(func(t *testing.T, data []byte) {
		h, err := hashOf(t, data)
		if len(data) < 2*hashBlock {
			if !errors.Is(err, errHashTooSmall) {
				t.Fatalf("a %d-byte file: %v, want errHashTooSmall", len(data), err)
			}
			return
		}
		want := uint64(len(data))
		for _, b := range [][]byte{data[:hashBlock], data[len(data)-hashBlock:]} {
			for i := 0; i < hashBlock; i += 8 {
				want += binary.LittleEndian.Uint64(b[i:])
			}
		}
		if err != nil || h != want {
			t.Fatalf("hash = %#x, %v; want %#x", h, err, want)
		}
	})
}
