package ffmpeg

import (
	"bytes"
	"testing"
)

func FuzzReadPCMSamples(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x00, 0x01})
	f.Add([]byte{0xFF})
	f.Add(make([]byte, 1000))

	f.Fuzz(func(t *testing.T, data []byte) {
		const maxSamples = 8000
		result := readPCMSamples(bytes.NewReader(data), maxSamples)
		if len(result) > maxSamples {
			t.Fatalf("readPCMSamples returned %d samples, exceeds max %d", len(result), maxSamples)
		}
	})
}
