package subsync

import (
	"bytes"
	"testing"
	"unicode/utf8"
)

func FuzzNormalizeEncoding(f *testing.F) {
	// UTF-8 BOM
	f.Add([]byte{0xEF, 0xBB, 0xBF, 'h', 'e', 'l', 'l', 'o'})
	// UTF-16 LE BOM
	f.Add([]byte{0xFF, 0xFE, 'h', 0, 'i', 0})
	// UTF-16 BE BOM
	f.Add([]byte{0xFE, 0xFF, 0, 'h', 0, 'i'})
	// Plain ASCII
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
	// Windows-1252 with special chars
	f.Add([]byte{0x93, 0x94, 0x96}) // smart quotes, en-dash
	// Empty
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		// The function's job is to produce UTF-8, so that is what is asserted:
		// every path either passes through bytes utf8.Valid accepted or builds
		// its output rune by rune. It must also not panic on arbitrary input.
		result := NormalizeEncoding(data)
		if !utf8.Valid(result) {
			t.Errorf("NormalizeEncoding(%q) = %q, which is not valid UTF-8", data, result)
		}
	})
}

// FuzzNormalizeEncodingIdempotent checks that encoding normalization reaches a
// fixed point: once converted to UTF-8 (BOM stripped), a second pass must
// produce identical bytes.
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
