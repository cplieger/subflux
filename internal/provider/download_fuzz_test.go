package provider

import (
	"testing"

	"github.com/cplieger/subflux/internal/epmarker"
	"github.com/cplieger/subflux/internal/timing"
)

// FuzzExtractAndValidate: no panic, and a result is non-nil, error-free and
// accepted by timing.ParseCues.
func FuzzExtractAndValidate(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 1, 5)
	f.Add([]byte{}, 0, 0)
	f.Add([]byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHi"), 1, 1)
	f.Add([]byte{0x00, 0x01, 0x02, 0x03}, 1, 1)
	f.Add([]byte("plain text that might look like a subtitle"), 0, 0)
	f.Add([]byte{'P', 'K', 3, 4, 0, 0, 0, 0}, 1, 1)           // ZIP magic
	f.Add([]byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x00}, 1, 1) // RAR magic
	f.Add(make([]byte, 128), 1, 1)                            // all zeros

	f.Fuzz(func(t *testing.T, data []byte, season, episode int) {
		result, err := ExtractAndValidate(data,
			epmarker.For(epmarker.Marker{Season: season, Episode: episode}))

		if err == nil {
			if result == nil {
				t.Fatal("ExtractAndValidate returned nil data with nil error")
			}
			if _, perr := timing.ParseCues(result); perr != nil {
				t.Fatalf("ExtractAndValidate returned data ParseCues refuses: %v", perr)
			}
		}

		if err != nil && result != nil {
			t.Fatal("ExtractAndValidate returned non-nil data with non-nil error")
		}
	})
}
