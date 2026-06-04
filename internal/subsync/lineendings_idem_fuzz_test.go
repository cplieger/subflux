package subsync

import (
	"bytes"
	"testing"
)

func FuzzNormalizeLineEndingsIdempotent(f *testing.F) {
	f.Add([]byte("line1\r\nline2\r\n"))
	f.Add([]byte("line1\nline2\n"))
	f.Add([]byte("line1\rline2\r"))
	f.Add([]byte("mixed\r\nlines\nhere\r"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		once := normalizeLineEndings(data)
		twice := normalizeLineEndings(once)
		if !bytes.Equal(once, twice) {
			t.Errorf("normalizeLineEndings not idempotent: once=%q twice=%q", once, twice)
		}
	})
}
