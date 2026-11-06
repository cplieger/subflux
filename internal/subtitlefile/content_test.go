package subtitlefile

import (
	"bytes"
	"testing"
)

func TestLooksLikeText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		head []byte
		want bool
	}{
		{"empty", nil, true},
		{"printable ASCII", []byte("Hello, World!"), true},
		{"TAB LF VT FF CR ESC and high bytes are text", []byte{0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x1B, 0x80, 0xFF, 'a', 'b'}, true},
		{"one NUL in ten", append([]byte{0x00}, bytes.Repeat([]byte("a"), 9)...), true},
		{"one NUL in nine", append([]byte{0x00}, bytes.Repeat([]byte("a"), 8)...), false},
		{"control bytes below TAB", []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08}, false},
		{"control bytes between CR and SPACE", []byte{0x0E, 0x0F, 0x10, 0x1A, 0x1C, 0x1F}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := LooksLikeText(tc.head); got != tc.want {
				t.Errorf("LooksLikeText(%x) = %v, want %v", tc.head, got, tc.want)
			}
		})
	}
}
