// Package scorer implements subtitle scoring based on release matching.
package scorer

import (
	"context"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search/scoring"
)

// Compile-time interface assertion.
var _ api.Scorer = (*Engine)(nil)

// Engine is a configured scorer.
type Engine struct {
	scores api.Scores
}

// New creates a scorer engine with the given weights.
func New(scores *api.Scores) *Engine {
	return &Engine{scores: *scores}
}

// Score calculates the score for a subtitle's match set.
// The input matches struct is not modified.
//
// Verifiable hash match returns the hash weight directly (typically 100).
// Otherwise, only release attribute keys contribute to the score.
// Non-verifiable hash adds the hash weight on top of release attributes.
func (e *Engine) Score(sub api.SubtitleInfo, matches api.MatchSet) (score, scoreNoHash int) {
	if matches.Hash && sub.HashVerifiable {
		slog.Debug("computed score", "score", e.scores.Hash, "hash_match", true)
		return e.scores.Hash, 0
	}

	score = sumScores(&e.scores, matches)
	scoreNoHash = score

	if matches.Hash {
		score += e.scores.Hash
	}

	if slog.Default().Handler().Enabled(context.Background(), slog.LevelDebug) {
		slog.Debug("computed score",
			"score", score,
			"matches", matches.Keys())
	}
	return score, scoreNoHash
}

// tierThreshold pairs a minimum score with its tier label.
type tierThreshold struct {
	Tier api.ScoreTier
	Min  int
}

// tierThresholds defines the score-to-tier mapping in descending order.
var tierThresholds = []tierThreshold{
	{Tier: api.TierExcellent, Min: 80},
	{Tier: api.TierGood, Min: 50},
	{Tier: api.TierAcceptable, Min: 20},
	{Tier: api.TierMinimal, Min: 1},
}

// ScoreToTier returns the named tier for a given score.
func (e *Engine) ScoreToTier(score int) api.ScoreTier {
	for _, t := range tierThresholds {
		if score >= t.Min {
			return t.Tier
		}
	}
	return api.TierNone
}

// sumScores totals the weights for matched release attributes, driven by the
// shared category table in internal/search/scoring.
func sumScores(s *api.Scores, matches api.MatchSet) int {
	total := 0
	for _, c := range scoring.Categories {
		if c.Match(matches) {
			total += c.Weight(s)
		}
	}
	return total
}
