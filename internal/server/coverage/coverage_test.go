package coverage_test

import (
	"testing"

	"subflux/internal/server/coverage"
)

// --- CountMissing ---

func TestCountMissing_EmptyInput(t *testing.T) {
	t.Skip("TODO: returns 0 when no series or movies")
}

func TestCountMissing_AllCovered(t *testing.T) {
	t.Skip("TODO: returns 0 when all targets have subtitles")
}

func TestCountMissing_SomeMissing(t *testing.T) {
	t.Skip("TODO: counts episodes + movies missing any target language")
}

// --- IndexSubStatus ---

func TestIndexSubStatus_GroupsByMediaIDAndKey(t *testing.T) {
	t.Skip("TODO: groups subtitle files by media_id then language|variant")
}

func TestIndexSubStatus_IgnoresCodecs(t *testing.T) {
	t.Skip("TODO: files with ignored codecs are excluded from status")
}

func TestIndexSubStatus_DeduplicatesFiles(t *testing.T) {
	t.Skip("TODO: duplicate file rows produce single status entry")
}

// --- CountEpisodeCoverageGrouped ---

func TestCountEpisodeCoverageGrouped_PerSeason(t *testing.T) {
	t.Skip("TODO: returns per-season coverage counts")
}

func TestCountEpisodeCoverageGrouped_SkipsSpecials(t *testing.T) {
	t.Skip("TODO: season 0 episodes excluded from count")
}

// --- CountMovieCoverage ---

func TestCountMovieCoverage_AllTargets(t *testing.T) {
	t.Skip("TODO: counts coverage per target language for a movie")
}

// --- ResolveRuleName ---

func TestResolveRuleName_DefaultWhenNoTargets(t *testing.T) {
	t.Skip("TODO: returns 'default' when targets is empty")
}

func TestResolveRuleName_MatchesAudioLang(t *testing.T) {
	t.Skip("TODO: returns rule name matching audio language")
}

// --- ExtractSeriesPrefix ---

func TestExtractSeriesPrefix(t *testing.T) {
	t.Skip("TODO: extracts 'tvdb-12345' from 'tvdb-12345-s01e01'")
}

// Ensure the package compiles with the test.
var _ = coverage.CountMissing
