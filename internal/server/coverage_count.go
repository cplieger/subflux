package server

import (
	"context"

	"subflux/internal/api"
	"subflux/internal/server/coverage"
)

// countMissing delegates to coverage.CountMissing.
func countMissing(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allSeries []api.Series, allMovies []api.Movie) int {
	return coverage.CountMissing(ctx, cfg, db, allSeries, allMovies)
}

// countMissingSeries is a package-level wrapper for test access.
func countMissingSeries(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allSeries []api.Series, ignoredCodecs map[string]bool) int {
	return coverage.CountMissingSeries(ctx, cfg, db, allSeries, ignoredCodecs)
}

// countMissingMovies is a package-level wrapper for test access.
func countMissingMovies(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allMovies []api.Movie, ignoredCodecs map[string]bool) int {
	return coverage.CountMissingMovies(ctx, cfg, db, allMovies, ignoredCodecs)
}
