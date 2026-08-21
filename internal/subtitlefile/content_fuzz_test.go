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

// FuzzValidate pins the error contract downstream relies on: Validate never
// panics, every refusal is classifiable — it wraps ErrEmpty or errBinary,
// never a bare error — and a zero-byte payload is always refused, because
// accepting it turns a provider's empty 200 into a saved subtitle of no bytes.
// Callers branch on errors.Is to tell an absent download from an archive one.
func FuzzValidate(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"))
	f.Add([]byte{0x50, 0x4B, 0x03, 0x04}) // ZIP magic
	f.Add([]byte{0x1F, 0x8B})             // gzip magic
	f.Add([]byte(""))
	f.Add([]byte("plain text subtitle content"))
	f.Fuzz(func(t *testing.T, data []byte) {
		err := Validate(data)
		if len(data) == 0 {
			if !errors.Is(err, ErrEmpty) {
				t.Fatalf("Validate(zero bytes) = %v, want ErrEmpty", err)
			}
			return
		}
		if err != nil && !errors.Is(err, errBinary) {
			t.Errorf("Validate(%q) error = %v, want it to wrap errBinary", data, err)
		}
	})
}
