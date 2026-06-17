package opensubtitles

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// Kills opensubtitles_search.go:59:16 CONDITIONALS_BOUNDARY (absSeason <= 0, <= -> <).
// For an episode with no scene season but an absolute episode number, the
// absolute mapping defaults its season to 1 only when absSeason <= 0. With
// SceneSeason == 0 the original takes that branch and emits season 1. The
// "< 0" mutant leaves absSeason at 0, and add() then substitutes req.Season (5)
// for the zero season, changing the emitted numbering.
func Test_gk_subflux_u30_EpisodeNumberingsAbsoluteSeasonDefault(t *testing.T) {
	req := &api.SearchRequest{
		MediaType:       api.MediaTypeEpisode,
		Season:          5,
		Episode:         0, // no aired numbering, so only the absolute entry is emitted
		SceneSeason:     0,
		SceneEpisode:    0,
		AbsoluteEpisode: 99,
	}
	out := episodeNumberings(req)
	if len(out) != 1 {
		t.Fatalf("episodeNumberings len = %d, want 1 (got %+v)", len(out), out)
	}
	if out[0].scheme != "absolute" || out[0].season != 1 || out[0].episode != 99 {
		t.Errorf("episodeNumberings[0] = %+v, want {scheme:absolute season:1 episode:99}", out[0])
	}
}
