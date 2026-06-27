package syncing_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search/syncing"
)

// PostProcess strips hearing-impaired annotations at the cue level. The
// byte-level pass alone does not remove an inline "[music]" tag, so a correct
// result requires the cue-level parse/process/rewrite to run end to end.
func TestSyncer_PostProcess_strips_hi_at_cue_level(t *testing.T) {
	t.Parallel()
	const in = "1\n00:00:01,000 --> 00:00:02,000\n[music]Hello\n\n"
	const want = "1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"
	got := syncing.Syncer{}.PostProcess([]byte(in), api.PostProcessConfig{StripHI: true})
	if string(got) != want {
		t.Errorf("PostProcess(stripHI) = %q, want %q", got, want)
	}
}
