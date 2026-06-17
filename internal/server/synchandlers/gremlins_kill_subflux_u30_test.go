package synchandlers

import (
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// Kills handler.go:280:12 CONDITIONALS_BOUNDARY (chars > bestChars, > -> >=).
// FindDialogueDenseStart keeps the FIRST window achieving the maximum dialogue
// density because it updates only on a strict ">". Two equally dense,
// non-overlapping windows (cues 100s apart, identical text length) therefore
// select the earlier anchor at 20s -> start 10s (after the 10s lead-in). The
// ">=" mutant updates on ties, selecting the later anchor at 120s -> start 110s.
func Test_gk_subflux_u30_FindDialogueDenseStartTieKeepsFirst(t *testing.T) {
	cues := []api.SubtitleCue{
		{Start: 20_000 * time.Millisecond, End: 22_000 * time.Millisecond, Text: "ab"},
		{Start: 120_000 * time.Millisecond, End: 122_000 * time.Millisecond, Text: "ab"},
	}
	got := FindDialogueDenseStart(cues)
	if got != 10_000 {
		t.Errorf("FindDialogueDenseStart(tie) = %d, want 10000 (earlier anchor must win ties)", got)
	}
}
