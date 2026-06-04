package embedded

import (
	"testing"
)

// FuzzNormalizeCodecName exercises codec name normalization with arbitrary inputs
// to ensure it never panics and returns a non-empty string for non-empty input.
func FuzzNormalizeCodecName(f *testing.F) {
	f.Add("subrip")
	f.Add("ass")
	f.Add("webvtt")
	f.Add("hdmv_pgs_subtitle")
	f.Add("unknown_codec")
	f.Add("")

	f.Fuzz(func(t *testing.T, codec string) {
		result := normalizeCodecName(codec)

		// Invariant 1: never panics (implicit).

		// Invariant 2: non-empty input yields non-empty output (passthrough).
		if codec != "" && result == "" {
			t.Fatalf("normalizeCodecName(%q) returned empty", codec)
		}

		// Invariant 3: empty input returns empty (identity).
		if codec == "" && result != "" {
			t.Fatalf("normalizeCodecName(\"\") = %q, want \"\"", result)
		}
	})
}

// FuzzDetectHIFromName exercises hearing-impaired detection with arbitrary names.
func FuzzDetectHIFromName(f *testing.F) {
	f.Add("English SDH")
	f.Add("hearing impaired")
	f.Add("Hard of Hearing")
	f.Add("Normal subtitle")
	f.Add("")

	f.Fuzz(func(t *testing.T, name string) {
		// Must not panic regardless of input.
		_ = detectHIFromName(name)
	})
}

// FuzzDetectForcedFromName exercises forced subtitle detection with arbitrary names.
func FuzzDetectForcedFromName(f *testing.F) {
	f.Add("Forced")
	f.Add("Signs & Songs")
	f.Add("Normal")
	f.Add("")

	f.Fuzz(func(t *testing.T, name string) {
		// Must not panic regardless of input.
		_ = detectForcedFromName(name)
	})
}
