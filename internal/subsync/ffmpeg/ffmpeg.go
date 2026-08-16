// Package ffmpeg provides low-level ffmpeg/ffprobe subprocess wrappers for
// stream probing, subtitle extraction, and PCM audio extraction.
// This package is pure I/O — it does not depend on subsync's Cue type.
package ffmpeg

import (
	"os/exec"
)

// MaxExtractBytes is the maximum size for ffmpeg stdout reads (subtitle
// extraction, ASS conversion). Prevents OOM from pathological streams.
const MaxExtractBytes = 50 * 1024 * 1024

// LangMapper resolves a raw language identifier — an ffprobe stream tag in any
// published code system, with or without a region — onto the caller's internal
// code space, returning "" for anything it cannot represent. It is injected
// rather than imported so this package stays free of provider dependencies;
// subflux supplies classify.Alpha2FromAlpha3.
type LangMapper func(string) string

// Available checks if ffmpeg is on PATH.
func Available() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

// ProbeAvailable checks if ffprobe is on PATH.
func ProbeAvailable() bool {
	_, err := exec.LookPath("ffprobe")
	return err == nil
}
