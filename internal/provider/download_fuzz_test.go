package provider

import (
	"testing"

	"subflux/internal/api"
)

// FuzzExtractAndValidate exercises ExtractAndValidate with arbitrary data,
// verifying that it never panics and that successful results pass subtitle
// validation invariants.
func FuzzExtractAndValidate(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"), 1, 5)
	f.Add([]byte{}, 0, 0)
	f.Add([]byte("WEBVTT\n\n00:00.000 --> 00:01.000\nHi"), 1, 1)
	f.Add([]byte{0x00, 0x01, 0x02, 0x03}, 1, 1)
	f.Add([]byte("plain text that might look like a subtitle"), 0, 0)
	f.Add([]byte{'P', 'K', 3, 4, 0, 0, 0, 0}, 1, 1) // ZIP magic
	f.Add([]byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x00}, 1, 1) // RAR magic
	f.Add(make([]byte, 128), 1, 1) // all zeros

	f.Fuzz(func(t *testing.T, data []byte, season, episode int) {
		result, err := ExtractAndValidate(data, season, episode)

		// Invariant 1: never panics (implicit).

		// Invariant 2: if no error, result is non-nil and passes ValidateSubtitleData.
		if err == nil {
			if result == nil {
				t.Fatal("ExtractAndValidate returned nil data with nil error")
			}
			if valErr := api.ValidateSubtitleData(result); valErr != nil {
				t.Fatalf("ExtractAndValidate returned data that fails ValidateSubtitleData: %v", valErr)
			}
		}

		// Invariant 3: if error, result must be nil.
		if err != nil && result != nil {
			t.Fatal("ExtractAndValidate returned non-nil data with non-nil error")
		}
	})
}
