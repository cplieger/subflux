package api

import "testing"

func FuzzValidateSubtitleData(f *testing.F) {
	f.Add([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"))
	f.Add([]byte{0x50, 0x4B, 0x03, 0x04}) // ZIP magic
	f.Add([]byte{0x1F, 0x8B})             // gzip magic
	f.Add([]byte(""))
	f.Add([]byte("plain text subtitle content"))
	f.Fuzz(func(t *testing.T, data []byte) {
		_ = ValidateSubtitleData(data)
	})
}

func FuzzIsValidMediaPrefix(f *testing.F) {
	f.Add("tvdb-12345")
	f.Add("tmdb-67890")
	f.Add("tvdb-12345-s01e02")
	f.Add("")
	f.Add("invalid")
	f.Add("tvdb-")
	f.Add("tmdb-abc")
	f.Add("tvdb-99999-s99e99")
	f.Fuzz(func(t *testing.T, prefix string) {
		// Must not panic on any input.
		_ = IsValidMediaPrefix(prefix)
	})
}
