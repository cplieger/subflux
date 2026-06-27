package api

import (
	"errors"
	"testing"
)

func FuzzCountNonTextBytes(f *testing.F) {
	f.Add([]byte("hello world"))
	f.Add([]byte{0x00, 0x01, 0x02})
	f.Add([]byte{})
	f.Add([]byte("\t\n\r normal"))
	f.Fuzz(func(t *testing.T, data []byte) {
		n := CountNonTextBytes(data)
		if n < 0 || n > len(data) {
			t.Errorf("CountNonTextBytes=%d out of range [0,%d]", n, len(data))
		}
	})
}

// FuzzValidateSubtitleData pins the error contract downstream relies on:
// ValidateSubtitleData never panics, and every non-nil error it returns wraps
// ErrBinaryData. Callers branch on errors.Is(err, ErrBinaryData) to decide a
// download was an archive rather than a subtitle, so a non-wrapping error
// would silently break that dispatch.
func FuzzValidateSubtitleData(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"))
	f.Add([]byte{0x50, 0x4B, 0x03, 0x04}) // ZIP magic
	f.Add([]byte{0x1F, 0x8B})             // gzip magic
	f.Add([]byte(""))
	f.Add([]byte("plain text subtitle content"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := ValidateSubtitleData(data); err != nil && !errors.Is(err, ErrBinaryData) {
			t.Errorf("ValidateSubtitleData(%q) error = %v, want it to wrap ErrBinaryData", data, err)
		}
	})
}
