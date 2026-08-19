package search

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/subflux"
)

// TestSyncSkipThreshold pins the exact formula (Source + ReleaseGroup - 1) so a
// mutation to either operator or the constant is caught. The 30/20 case is the
// discriminator: any arithmetic change moves it off 49.
func TestSyncSkipThreshold(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		scores subflux.Scores
		want   int
	}{
		{"specific", subflux.Scores{Source: 30, ReleaseGroup: 20}, 49},
		{"zero_group", subflux.Scores{Source: 10, ReleaseGroup: 0}, 9},
		{"both_zero", subflux.Scores{Source: 0, ReleaseGroup: 0}, -1},
		{"default", subflux.DefaultScores, subflux.DefaultScores.Source + subflux.DefaultScores.ReleaseGroup - 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := syncSkipThreshold(tc.scores); got != tc.want {
				t.Errorf("syncSkipThreshold(%+v) = %d, want %d", tc.scores, got, tc.want)
			}
		})
	}
}

// stripHITestConfig is a config whose PostProcessConfig strips HI annotations,
// so the SyncAndPostProcess variant branch (HI target overrides StripHI to
// false) becomes observable.
type stripHITestConfig struct{ mockConfig }

func (stripHITestConfig) PostProcess() subflux.PostProcessConfig {
	return subflux.PostProcessConfig{StripHI: true, NormalizeEndings: true}
}

// TestEngine_SyncAndPostProcess_HI_variant_preserves_HI asserts the variant
// branch: with strip_hi enabled globally, a standard target strips HI cues
// while an HI target preserves them (the function forces StripHI=false for
// VariantHI). This kills the `variant == VariantHI` conditional.
func TestEngine_SyncAndPostProcess_HI_variant_preserves_HI(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	videoPath := filepath.Join(dir, "movie.mkv") // no reference => sync is a no-op
	hiCue := []byte("1\n00:00:01,000 --> 00:00:02,000\n[door creaks]\n\n" +
		"2\n00:00:03,000 --> 00:00:04,000\nHello there\n")

	e := newEngine(nil, &mockStore{}, &stripHITestConfig{}, nil,
		scorer.New(&subflux.DefaultScores), Syncer{}, noopDetector{})

	hiOut, _ := e.SyncAndPostProcess(t.Context(), hiCue, videoPath, "fr", subflux.VariantHI)
	stdOut, _ := e.SyncAndPostProcess(t.Context(), hiCue, videoPath, "fr", subflux.DefaultVariant)

	if !bytes.Contains(hiOut, []byte("door creaks")) {
		t.Errorf("HI variant stripped the HI cue, want it preserved:\n%s", hiOut)
	}
	if bytes.Contains(stdOut, []byte("door creaks")) {
		t.Errorf("standard variant kept the HI cue, want it stripped:\n%s", stdOut)
	}
}
