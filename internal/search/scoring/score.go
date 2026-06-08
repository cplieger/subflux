// Package scoring provides pure subtitle scoring and identity filtering.
package scoring

import (
	"context"
	"log/slog"
	"strings"

	"github.com/cplieger/subflux/internal/api"
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
	CompareSource func(*api.MatchSet, string, string)
	IsSeasonPack  func(string) bool
}

// BuildMatches compares video and subtitle release attributes, returning
// a set of matched attribute keys used by the scorer.
func BuildMatches(video *api.VideoInfo, sub *api.Subtitle, deps MatchDeps) api.MatchSet {
	var matches api.MatchSet

	if sub.MatchedBy == api.MatchByHash {
		matches.Hash = true
	}
	if sub.MatchedBy == api.MatchByIMDB {
		if video.MediaType == api.MediaTypeEpisode {
			matches.SeriesIMDB = true
		} else {
			matches.IMDB = true
		}
	}

	videoRelease := deps.ParseRelease(video.ReleaseGroup)
	subRelease := deps.ParseRelease(sub.ReleaseName)

	// matchRule pairs a field setter with the video and subtitle field values.
	type matchRule struct {
		set      func(*api.MatchSet)
		videoVal string
		subVal   string
	}
	rules := []matchRule{
		{func(m *api.MatchSet) { m.ReleaseGroup = true }, videoRelease.ReleaseGroup, subRelease.ReleaseGroup},
		{func(m *api.MatchSet) { m.VideoCodec = true }, videoRelease.VideoCodec, subRelease.VideoCodec},
		{func(m *api.MatchSet) { m.StreamingService = true }, videoRelease.StreamingService, subRelease.StreamingService},
		{func(m *api.MatchSet) { m.Edition = true }, videoRelease.Edition, subRelease.Edition},
		{func(m *api.MatchSet) { m.HDR = true }, videoRelease.HDR, subRelease.HDR},
	}
	for _, r := range rules {
		if r.videoVal != "" && r.subVal != "" && strings.EqualFold(r.videoVal, r.subVal) {
			r.set(&matches)
		}
	}
	deps.CompareSource(&matches, videoRelease.Source, subRelease.Source)

	if video.MediaType == api.MediaTypeEpisode && deps.IsSeasonPack(sub.ReleaseName) {
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

// matchKeyWeight pairs a match-field accessor with a score-field accessor and key name.
type matchKeyWeight struct {
	weight func(*api.Scores) int
	match  func(api.MatchSet) bool
	key    string
}

// matchKeyWeights is the data-driven table mapping match fields to score fields.
var matchKeyWeights = []matchKeyWeight{
	{weight: func(s *api.Scores) int { return s.Source }, match: func(m api.MatchSet) bool { return m.Source }, key: "source"},
	{weight: func(s *api.Scores) int { return s.ReleaseGroup }, match: func(m api.MatchSet) bool { return m.ReleaseGroup }, key: "release_group"},
	{weight: func(s *api.Scores) int { return s.StreamingService }, match: func(m api.MatchSet) bool { return m.StreamingService }, key: "streaming_service"},
	{weight: func(s *api.Scores) int { return s.VideoCodec }, match: func(m api.MatchSet) bool { return m.VideoCodec }, key: "video_codec"},
	{weight: func(s *api.Scores) int { return s.HDR }, match: func(m api.MatchSet) bool { return m.HDR }, key: "hdr"},
	{weight: func(s *api.Scores) int { return s.Edition }, match: func(m api.MatchSet) bool { return m.Edition }, key: "edition"},
	{weight: func(s *api.Scores) int { return s.SeasonPack }, match: func(m api.MatchSet) bool { return m.SeasonPack }, key: "season_pack"},
}

// MatchBreakdown returns the per-category score contributions for a match set.
func MatchBreakdown(scores *api.Scores, matches api.MatchSet) map[string]int {
	out := make(map[string]int)
	if matches.Hash {
		out["hash"] = scores.Hash
	}
	for _, sw := range matchKeyWeights {
		if w := sw.weight(scores); sw.match(matches) && w > 0 {
			out[sw.key] = w
		}
	}
	return out
}
