package scoring

// Round-2 mutant-killing tests for internal/search/scoring.
//
// identity_match.go EpisodeNumberMatch scene/absolute season fallback:
//
//	44:8 / 53:8 CONDITIONALS_BOUNDARY (`if s <= 0` -> `s < 0`) and
//	44:8 / 53:8 CONDITIONALS_NEGATION (`if s <= 0` -> `s > 0`).
//	`s := req.SceneSeason; if s <= 0 { s = <fallback> }` substitutes a real
//	season for a 0/absent scene season before matchesPair. matchesPair treats a
//	candidate season of 0 as a wildcard, so leaving s==0 (either mutant) makes
//	the season match anything — accepting a sub whose season does NOT match the
//	resolved season. Both mutants therefore flip a non-match to a match.
//
// identity_filter.go release-season guard:
//
//	55:68 CONDITIONALS_BOUNDARY (`if relSeason > 0` -> `>= 0`). When a release
//	name has no extractable season (relSeason==0) the original skips the
//	season-equality check; the `>= 0` mutant runs it and rejects (0 != reqSeason).

import (
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func TestGkSubfluxR2_EpisodeNumberMatchSceneSeasonFallback(t *testing.T) {
	// SceneEpisode is set with SceneSeason==0, so the original substitutes
	// req.Season (2). The sub is season 3, episode 5: with the resolved season
	// 2 it does NOT match (3 != 2); leaving s==0 makes the season a wildcard
	// and (wrongly) matches on episode alone.
	req := &api.SearchRequest{Season: 2, Episode: 9, SceneSeason: 0, SceneEpisode: 5, AbsoluteEpisode: 0}
	if EpisodeNumberMatch(3, 5, req) {
		t.Error("EpisodeNumberMatch(s3e5) should be false: resolved scene season 2 != 3")
	}
}

func TestGkSubfluxR2_EpisodeNumberMatchAbsoluteSeasonFallback(t *testing.T) {
	// AbsoluteEpisode is set with SceneSeason==0, so the original substitutes
	// season 1. The sub is season 2, episode 100: with the resolved season 1 it
	// does NOT match; leaving s==0 makes the season a wildcard and matches.
	req := &api.SearchRequest{Season: 2, Episode: 9, SceneSeason: 0, SceneEpisode: 0, AbsoluteEpisode: 100}
	if EpisodeNumberMatch(2, 100, req) {
		t.Error("EpisodeNumberMatch(s2e100) should be false: resolved absolute season 1 != 2")
	}
}

func TestGkSubfluxR2_ValidateNoMetadataNoReleaseSeason(t *testing.T) {
	// A release name with no extractable season number; with an episode request
	// the original treats "no season info" as acceptable (returns true). The
	// `relSeason >= 0` mutant runs the equality check and rejects (0 != 1).
	sub := &api.Subtitle{ReleaseName: "Great.Film.2020.1080p.WEB"}
	req := &api.SearchRequest{MediaType: api.MediaTypeEpisode, Season: 1}
	if !validateNoMetadata(sub, req) {
		t.Error("validateNoMetadata should accept a release with no extractable season")
	}
}
