// Package scoring holds the pure matching and weighting rules the scan engine
// applies to a candidate: which subtitles identify as the requested item, which
// weight categories a release hits, and the title and episode comparisons under
// both.
//
// Its heaviest consumer by far is internal/search, which is why it sits here
// rather than at the top level. internal/scorer is the configured engine over
// these rules and uses exactly one symbol from this package; see its doc for
// why the two stay apart despite the near-identical names.
package scoring

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cplieger/subflux/internal/subflux"
)

// ReleaseInfo holds metadata extracted from a release/scene name.
type ReleaseInfo struct {
	Source           string
	VideoCodec       string
	ReleaseGroup     string
	StreamingService string
	Edition          string
	HDR              string
}

// MatchDeps provides release-parsing dependencies to BuildMatches.
type MatchDeps struct {
	ParseRelease  func(string) ReleaseInfo
	CompareSource func(*subflux.MatchSet, string, string)
	IsSeasonPack  func(string) bool
}

// BuildMatches compares video and subtitle release attributes, returning
// a set of matched attribute keys used by the scorer.
func BuildMatches(video *subflux.VideoInfo, sub *subflux.Subtitle, deps MatchDeps) subflux.MatchSet {
	var matches subflux.MatchSet

	if sub.MatchedBy == subflux.MatchByHash {
		matches.Hash = true
	}
	if sub.MatchedBy == subflux.MatchByIMDB {
		if video.MediaType == subflux.MediaTypeEpisode {
			matches.SeriesIMDB = true
		} else {
			matches.IMDB = true
		}
	}

	videoRelease := deps.ParseRelease(video.ReleaseGroup)
	subRelease := deps.ParseRelease(sub.ReleaseName)

	for _, c := range Categories {
		if c.Extract == nil {
			continue // source and season_pack match via bespoke logic below
		}
		videoVal, subVal := c.Extract(videoRelease), c.Extract(subRelease)
		if videoVal != "" && subVal != "" && strings.EqualFold(videoVal, subVal) {
			c.SetMatch(&matches)
		}
	}
	deps.CompareSource(&matches, videoRelease.Source, subRelease.Source)

	if video.MediaType == subflux.MediaTypeEpisode && deps.IsSeasonPack(sub.ReleaseName) {
		matches.SeasonPack = true
	}

	if slog.Default().Enabled(context.TODO(), slog.LevelDebug) {
		slog.Debug("buildMatches result",
			"sub_release", sub.ReleaseName,
			"matched_by", sub.MatchedBy,
			"match_count", len(matches.Keys()),
			"video_release", video.ReleaseGroup,
			"video_source", videoRelease.Source,
			"video_codec", videoRelease.VideoCodec,
			"video_group", videoRelease.ReleaseGroup)
	}

	return matches
}

// Category is one row of the canonical scoring-category table: the
// breakdown key, the weight accessor into subflux.Scores, the match-bit getter
// and setter on subflux.MatchSet, and — for categories matched by simple
// case-insensitive equality of parsed release attributes — the ReleaseInfo
// extractor that drives BuildMatches. Categories with bespoke match logic
// (source-family comparison, season-pack detection) leave Extract nil and
// are handled explicitly in BuildMatches.
type Category struct {
	Weight   func(*subflux.Scores) int
	Match    func(subflux.MatchSet) bool
	SetMatch func(*subflux.MatchSet)
	Extract  func(ReleaseInfo) string
	Key      string
}

// Categories is the canonical table of scored release-attribute categories,
// shared by the match builder (BuildMatches), the score breakdown
// (MatchBreakdown), and the scorer (internal/scorer). Hash and identity
// (IMDB) matching are handled separately by each consumer. Adding a scoring
// category is a one-entry change here, plus the subflux.Scores/subflux.MatchSet
// fields it references.
var Categories = []Category{
	{
		Key:      "source",
		Weight:   func(s *subflux.Scores) int { return s.Source },
		Match:    func(m subflux.MatchSet) bool { return m.Source },
		SetMatch: func(m *subflux.MatchSet) { m.Source = true },
		// Matched via MatchDeps.CompareSource, not generic equality; Extract stays nil.
	},
	{
		Key:      "release_group",
		Weight:   func(s *subflux.Scores) int { return s.ReleaseGroup },
		Match:    func(m subflux.MatchSet) bool { return m.ReleaseGroup },
		SetMatch: func(m *subflux.MatchSet) { m.ReleaseGroup = true },
		Extract:  func(r ReleaseInfo) string { return r.ReleaseGroup },
	},
	{
		Key:      "streaming_service",
		Weight:   func(s *subflux.Scores) int { return s.StreamingService },
		Match:    func(m subflux.MatchSet) bool { return m.StreamingService },
		SetMatch: func(m *subflux.MatchSet) { m.StreamingService = true },
		Extract:  func(r ReleaseInfo) string { return r.StreamingService },
	},
	{
		Key:      "video_codec",
		Weight:   func(s *subflux.Scores) int { return s.VideoCodec },
		Match:    func(m subflux.MatchSet) bool { return m.VideoCodec },
		SetMatch: func(m *subflux.MatchSet) { m.VideoCodec = true },
		Extract:  func(r ReleaseInfo) string { return r.VideoCodec },
	},
	{
		Key:      "hdr",
		Weight:   func(s *subflux.Scores) int { return s.HDR },
		Match:    func(m subflux.MatchSet) bool { return m.HDR },
		SetMatch: func(m *subflux.MatchSet) { m.HDR = true },
		Extract:  func(r ReleaseInfo) string { return r.HDR },
	},
	{
		Key:      "edition",
		Weight:   func(s *subflux.Scores) int { return s.Edition },
		Match:    func(m subflux.MatchSet) bool { return m.Edition },
		SetMatch: func(m *subflux.MatchSet) { m.Edition = true },
		Extract:  func(r ReleaseInfo) string { return r.Edition },
	},
	{
		Key:      "season_pack",
		Weight:   func(s *subflux.Scores) int { return s.SeasonPack },
		Match:    func(m subflux.MatchSet) bool { return m.SeasonPack },
		SetMatch: func(m *subflux.MatchSet) { m.SeasonPack = true },
		// Matched via MatchDeps.IsSeasonPack, not generic equality; Extract stays nil.
	},
}

// MatchBreakdown returns the per-category score contributions for a match set.
func MatchBreakdown(scores *subflux.Scores, matches subflux.MatchSet) map[string]int {
	out := make(map[string]int)
	if matches.Hash {
		out["hash"] = scores.Hash
	}
	for _, c := range Categories {
		if w := c.Weight(scores); c.Match(matches) && w > 0 {
			out[c.Key] = w
		}
	}
	return out
}
