package subtitlefile

import (
	"errors"
	"testing"
)

func FuzzCountNonText(f *testing.F) {
	f.Add([]byte("hello world"))
	f.Add([]byte{0x00, 0x01, 0x02})
	f.Add([]byte{})
	f.Add([]byte("\t\n\r normal"))
	f.Fuzz(func(t *testing.T, data []byte) {
		n := CountNonText(data)
		if n < 0 || n > len(data) {
			t.Errorf("CountNonText=%d out of range [0,%d]", n, len(data))
		}
	})
}

// FuzzValidate pins the error contract downstream relies on:
// Validate never panics, and every non-nil error it returns wraps
// errBinary. Callers branch on errors.Is(err, errBinary) to decide a
// download was an archive rather than a subtitle, so a non-wrapping error
// would silently break that dispatch.
func FuzzValidate(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"))
	f.Add([]byte{0x50, 0x4B, 0x03, 0x04}) // ZIP magic
	f.Add([]byte{0x1F, 0x8B})             // gzip magic
	f.Add([]byte(""))
	f.Add([]byte("plain text subtitle content"))
	f.Fuzz(func(t *testing.T, data []byte) {
		if err := Validate(data); err != nil && !errors.Is(err, errBinary) {
			t.Errorf("Validate(%q) error = %v, want it to wrap errBinary", data, err)
		}
	})
}
