package api

import "testing"

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
