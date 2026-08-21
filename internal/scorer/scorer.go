// Package scorer holds the CONFIGURED scoring engine: it carries the user's
// weight table and answers the two questions callers outside the search domain
// ask, what does this subtitle score and which tier is that score.
//
// It is deliberately separate from internal/search/scoring, which holds the pure
// matching and weighting rules, and the near-identical names were examined
// rather than left as an accident. They are two concerns, and the usage proves
// it: internal/search reaches 14 distinct scoring symbols across 23 call sites
// while this package uses exactly one, so the pure half belongs where its
// consumer is, and three packages outside the search domain (the composition
// root, internal/server, internal/wiring) hold this one. A merge in either
// direction therefore either buries search's own rules behind a facade it does
// not use or makes the server import a search subpackage. Neither name states a
// category and neither shadows anything, so the pair is a reading cost, not a
// rule violation, and renaming would churn 126 call sites to fix a confusion a
// package doc can resolve.
package scorer

import (
	"context"
	"log/slog"

	"github.com/cplieger/subflux/internal/search/scoring"
	"github.com/cplieger/subflux/internal/subflux"
)

// Engine is a configured scorer.
type Engine struct {
	scores subflux.Scores
}

// New creates a scorer engine with the given weights.
func New(scores *subflux.Scores) *Engine {
	return &Engine{scores: *scores}
}

// Score calculates the score for a subtitle's match set.
// The input matches struct is not modified.
//
// Verifiable hash match returns the hash weight directly (typically 100).
// Otherwise, only release attribute keys contribute to the score.
// Non-verifiable hash adds the hash weight on top of release attributes.
func (e *Engine) Score(sub subflux.SubtitleInfo, matches subflux.MatchSet) (score, scoreNoHash int) {
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
	Tier subflux.ScoreTier
	Min  int
}

// tierThresholds defines the score-to-tier mapping in descending order.
var tierThresholds = []tierThreshold{
	{Tier: subflux.TierExcellent, Min: 80},
	{Tier: subflux.TierGood, Min: 50},
	{Tier: subflux.TierAcceptable, Min: 20},
	{Tier: subflux.TierMinimal, Min: 1},
}

// ScoreToTier returns the named tier for a given score.
func (e *Engine) ScoreToTier(score int) subflux.ScoreTier {
	for _, t := range tierThresholds {
		if score >= t.Min {
			return t.Tier
		}
	}
	return subflux.TierNone
}

// sumScores totals the weights for matched release attributes, driven by the
// shared category table in internal/search/scoring.
func sumScores(s *subflux.Scores, matches subflux.MatchSet) int {
	total := 0
	for _, c := range scoring.Categories {
		if c.Match(matches) {
			total += c.Weight(s)
		}
	}
	return total
}
