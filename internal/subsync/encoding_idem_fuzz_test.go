package subsync

import (
	"bytes"
	"testing"
)

func FuzzNormalizeEncodingIdempotent(f *testing.F) {
	f.Add([]byte("hello"))
	f.Add([]byte{0xEF, 0xBB, 0xBF, 'h', 'i'})
	f.Add([]byte{0xFF, 0xFE, 'a', 0})
	f.Add([]byte{0x93, 0x94})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		once := NormalizeEncoding(data)
		twice := NormalizeEncoding(once)
		if !bytes.Equal(once, twice) {
			t.Errorf("NormalizeEncoding not idempotent: once=%q twice=%q", once, twice)
		}
	})
}
