package server

import (
	"subflux/internal/api"
	"subflux/internal/server/coverage"
)

// Type aliases for test readability — these were previously in coverage_calc.go.
type covKey = coverage.Key
type covStatus = coverage.Status

// Test-only aliases for coverage constants.
const ruleDefault = coverage.RuleDefault
const ruleNoTargets = coverage.RuleNoTargets

// Test-only function aliases for coverage package functions.
var indexSubStatus = coverage.IndexSubStatus
var resolveRuleName = coverage.ResolveRuleName
var extractSeriesPrefix = coverage.ExtractSeriesPrefix

func countEpisodeCoverageGrouped(episodes []map[coverage.Key]*coverage.Status, targets []api.SubtitleTarget, total int) []coverage.TargetCoverage {
	return coverage.CountEpisodeCoverageGrouped(episodes, targets, total)
}

func countMovieCoverage(subs map[coverage.Key]*coverage.Status, targets []api.SubtitleTarget) []coverage.TargetCoverage {
	return coverage.CountMovieCoverage(subs, targets)
}

func deduplicateFileRows(rows []api.SubtitleFileRow) []api.SubtitleFileRow {
	return coverage.DeduplicateFileRows(rows)
}
