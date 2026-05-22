// Package ffmpeg provides low-level ffmpeg/ffprobe subprocess wrappers for
// stream probing, subtitle extraction, and PCM audio extraction.
// This package is pure I/O — it does not depend on subsync's Cue type.
package ffmpeg

import (
	"context"
	"os/exec"
)

// MaxExtractBytes is the maximum size for ffmpeg stdout reads (subtitle
// extraction, ASS conversion). Prevents OOM from pathological streams.
const MaxExtractBytes = 50 * 1024 * 1024

// LangMapper maps ISO 639-3 language codes to ISO 639-1.
type LangMapper func(string) string

// NoopLangMapper is the fallback when no mapper is provided.
func NoopLangMapper(string) string { return "" }

// CommandRunner abstracts subprocess execution for testability. The default
// implementation uses exec.CommandContext. Test code can inject a mock runner
// to exercise parsing/selection logic without real ffmpeg binaries.
type CommandRunner interface {
	// Run executes a command and returns its combined stdout output.
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
}

// execRunner is the default CommandRunner using os/exec.
type execRunner struct{}

// Run implements CommandRunner by running the command and capturing stdout.
func (execRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.Output()
}

// DefaultRunner is the package-level CommandRunner used by exported functions.
// Override in tests to inject a mock.
var DefaultRunner CommandRunner = execRunner{}

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
