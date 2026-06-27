package syncing_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/search/syncing"
)

// ParseSRT parses valid SRT data into cues, returning the parsed cues with a
// nil error.
func TestSubtitleProcessor_ParseSRT_valid(t *testing.T) {
	t.Parallel()
	const srt = "1\n00:00:01,000 --> 00:00:02,000\nHello world\n\n"
	cues, err := syncing.SubtitleProcessor{}.ParseSRT([]byte(srt))
	if err != nil {
		t.Fatalf("ParseSRT(valid) error = %v, want nil", err)
	}
	if len(cues) != 1 {
		t.Fatalf("ParseSRT(valid) returned %d cues, want 1", len(cues))
	}
	if cues[0].Text != "Hello world" {
		t.Errorf("ParseSRT(valid) cue text = %q, want %q", cues[0].Text, "Hello world")
	}
}
