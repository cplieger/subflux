package server

import (
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/subflux"
)

// Type aliases for test readability — these were previously in coverage_calc.go.
type (
	covKey    = coverage.Key
	covStatus = coverage.Status
)

// Test-only aliases for coverage constants.
const (
	ruleDefault   = coverage.RuleDefault
	ruleNoTargets = coverage.RuleNoTargets
)

// Test-only function aliases for coverage package functions.
var (
	indexSubStatus      = coverage.IndexSubStatus
	resolveRuleName     = coverage.ResolveRuleName
	extractSeriesPrefix = coverage.ExtractSeriesPrefix
)

func countEpisodesGrouped(episodes []map[coverage.Key]*coverage.Status, targets []subflux.SubtitleTarget, total int) []coverage.TargetCoverage {
	return coverage.CountEpisodesGrouped(episodes, targets, total)
}

func countMovies(subs map[coverage.Key]*coverage.Status, targets []subflux.SubtitleTarget) []coverage.TargetCoverage {
	return coverage.CountMovies(subs, targets)
}

func deduplicateFileRows(rows []subflux.SubtitleEntry) []subflux.SubtitleEntry {
	return coverage.DeduplicateFileRows(rows)
}
