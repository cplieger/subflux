package search

import (
	"cmp"
	"slices"

	"github.com/cplieger/subflux/internal/search/release"
	"github.com/cplieger/subflux/internal/search/scoring"
	"github.com/cplieger/subflux/internal/subflux"
)

type scoredSub struct {
	sub     subflux.Subtitle
	score   int
	matches subflux.MatchSet
}

// defaultMatchDeps is the singleton MatchDeps wired to this package's release parsing.
// Hoisted to package level to eliminate per-call closure allocation in the scoring hot path.
var defaultMatchDeps = scoring.MatchDeps{
	ParseRelease: func(name string) scoring.ReleaseInfo {
		r := release.ParseName(name)
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

func scoreResults(sc Scorer, video *subflux.VideoInfo, subs []subflux.Subtitle, provPriority func(subflux.ProviderID) int) []scoredSub {
	scored := make([]scoredSub, len(subs))
	for i := range subs {
		matches := scoring.BuildMatches(video, &subs[i], defaultMatchDeps)
		score, _ := sc.Score(subflux.SubtitleInfo{
			HashVerifiable: subs[i].MatchedBy == subflux.MatchByHash,
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

func buildMatches(video *subflux.VideoInfo, sub *subflux.Subtitle) subflux.MatchSet {
	return scoring.BuildMatches(video, sub, defaultMatchDeps)
}

func matchBreakdown(scores *subflux.Scores, matches subflux.MatchSet) map[string]int {
	return scoring.MatchBreakdown(scores, matches)
}

func videoInfoFromRequest(req *subflux.SearchRequest) subflux.VideoInfo {
	return subflux.VideoInfo{
		MediaType:    req.MediaType,
		ReleaseGroup: req.ReleaseName,
	}
}
