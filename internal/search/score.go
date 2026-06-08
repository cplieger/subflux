// score.go provides scoring and identity validation for subtitle search results.
// scoreResults ranks subtitles by release attribute matching.

package search

import (
	"cmp"
	"slices"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search/release"
	"github.com/cplieger/subflux/internal/search/scoring"
)

// scoredSub pairs a subtitle with its computed score and match breakdown.
type scoredSub struct {
	sub     api.Subtitle
	score   int
	matches api.MatchSet
}

// defaultMatchDeps is the singleton MatchDeps wired to this package's release parsing.
// Hoisted to package level to eliminate per-call closure allocation in the scoring hot path.
var defaultMatchDeps = scoring.MatchDeps{
	ParseRelease: func(name string) scoring.ReleaseInfo {
		r := release.ParseReleaseName(name)
		return scoring.ReleaseInfo{
			Source:           r.Source,
			VideoCodec:       r.VideoCodec,
			ReleaseGroup:     r.ReleaseGroup,
			StreamingService: r.StreamingService,
			Edition:          r.Edition,
			HDR:              r.HDR,
		}
	},
	CompareSource: release.CompareSource,
	IsSeasonPack:  scoring.IsSeasonPack,
}

// scoreResults scores each subtitle against the video and returns them
// sorted by descending score, with provider priority as tiebreaker.
func scoreResults(sc api.Scorer, video *api.VideoInfo, subs []api.Subtitle, provPriority func(api.ProviderID) int) []scoredSub {
	scored := make([]scoredSub, len(subs))
	for i := range subs {
		matches := scoring.BuildMatches(video, &subs[i], defaultMatchDeps)
		score, _ := sc.Score(video, api.SubtitleInfo{
			HashVerifiable: subs[i].MatchedBy == api.MatchByHash,
		}, matches)
		scored[i] = scoredSub{sub: subs[i], score: score, matches: matches}
	}

	slices.SortFunc(scored, func(a, b scoredSub) int {
		if c := cmp.Compare(b.score, a.score); c != 0 {
			return c
		}
		return cmp.Compare(provPriority(a.sub.Provider), provPriority(b.sub.Provider))
	})
	return scored
}

// buildMatches compares video and subtitle release attributes, returning
// a set of matched attribute keys used by the scorer.
func buildMatches(video *api.VideoInfo, sub *api.Subtitle) api.MatchSet {
	return scoring.BuildMatches(video, sub, defaultMatchDeps)
}

// matchBreakdown returns the per-category score contributions for a match set.
func matchBreakdown(scores *api.Scores, matches api.MatchSet) map[string]int {
	return scoring.MatchBreakdown(scores, matches)
}

// videoInfoFromRequest extracts the video metadata needed for scoring.
func videoInfoFromRequest(req *api.SearchRequest) api.VideoInfo {
	return api.VideoInfo{
		MediaType:    req.MediaType,
		ReleaseGroup: req.ReleaseName,
	}
}
