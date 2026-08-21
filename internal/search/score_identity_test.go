package search

import (
	"context"

	"github.com/cplieger/subflux/internal/search/scoring"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/subsync"
)

// Test-only aliases for functions moved to the scoring sub-package.
// These allow existing tests to continue exercising the scoring logic
// without re-exporting dead shims in production code.

func normalizeTitle(s string) string {
	return scoring.NormalizeTitle(s)
}

// releaseNameMatchesTitle observes the single-title release-name match through
// the production entry point: with no alternative titles,
// scoring.AnyReleaseNameMatches is exactly that comparison.
func releaseNameMatchesTitle(reqTitle, releaseName string) bool {
	return scoring.AnyReleaseNameMatches(&subflux.SearchRequest{Title: reqTitle}, releaseName)
}

func titlesMatch(a, b string) bool {
	return scoring.TitlesMatch(a, b)
}

func identityOK(sub *subflux.Subtitle, req *subflux.SearchRequest) bool {
	return scoring.IdentityOK(sub, req)
}

func episodeNumberMatch(subSeason, subEpisode int, req *subflux.SearchRequest) bool {
	return scoring.EpisodeNumberMatch(subSeason, subEpisode, req)
}

func identityTitleOK(sub *subflux.Subtitle, req *subflux.SearchRequest) bool {
	return scoring.IdentityTitleOK(sub, req)
}

func anyTitleMatches(req *subflux.SearchRequest, candidate string) bool {
	return scoring.AnyTitleMatches(req, candidate)
}

func anyReleaseNameMatches(req *subflux.SearchRequest, releaseName string) bool {
	return scoring.AnyReleaseNameMatches(req, releaseName)
}

func extractReleaseSeason(releaseName string) int {
	return scoring.ExtractReleaseSeason(releaseName)
}

// filterByIdentity is a test-only alias for scoring.FilterByIdentity.
func filterByIdentity(results []subflux.Subtitle, req *subflux.SearchRequest) []subflux.Subtitle {
	kept, _ := scoring.FilterByIdentity(results, req)
	return kept
}

// syncAgainstReference is a test-only alias for the syncing sub-package function.
func syncAgainstReference(ctx context.Context, data []byte, videoPath, lang string, minConf ...float64) subsync.SyncResult {
	return syncing.SyncAgainstReference(ctx, data, videoPath, lang, nil, minConf...)
}
