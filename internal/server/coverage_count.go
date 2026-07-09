package server

import (
	"context"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/coverage"
)

// countMissing delegates to coverage.CountMissing.
func countMissing(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allSeries []arrapi.Series, allMovies []arrapi.Movie) int {
	return coverage.CountMissing(ctx, cfg, db, allSeries, allMovies)
}

// countMissingSeries is a package-level wrapper for test access.
func countMissingSeries(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allSeries []arrapi.Series, ignoredCodecs map[string]bool) int {
	return coverage.CountMissingSeries(ctx, cfg, db, allSeries, ignoredCodecs)
}

// countMissingMovies is a package-level wrapper for test access.
func countMissingMovies(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, allMovies []arrapi.Movie, ignoredCodecs map[string]bool) int {
	return coverage.CountMissingMovies(ctx, cfg, db, allMovies, ignoredCodecs)
}
