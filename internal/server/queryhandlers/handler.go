// Package queryhandlers provides read-only HTTP query handlers: subtitle
// state, backoff, manual locks, providers, parsed config, score simulation,
// and dashboard stats.
package queryhandlers

import (
	"context"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
)

// QueryStore documents the api.Store methods used by query handlers.
type QueryStore interface {
	GetState(ctx context.Context, q *api.StateQuery) ([]api.StateEntry, error)
	GetBackoffItems(ctx context.Context) ([]api.BackoffEntry, error)
	GetBackoffByPrefix(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.BackoffEntry, error)
	GetManualLocks(ctx context.Context) ([]api.ManualLockEntry, error)
	Stats(ctx context.Context) (downloads, attempts int, err error)
}

// Compile-time assertion: api.Store satisfies QueryStore.
var _ QueryStore = api.Store(nil)

// StatsSonarrClient is the Sonarr surface the stats handler uses.
type StatsSonarrClient interface {
	GetSeries(ctx context.Context) ([]arrapi.Series, error)
}

// StatsRadarrClient is the Radarr surface the stats handler uses.
type StatsRadarrClient interface {
	GetMovies(ctx context.Context) ([]arrapi.Movie, error)
}

// Compile-time assertions: the arrapi-backed role clients satisfy the stats
// surfaces.
var (
	_ StatsSonarrClient = api.SonarrClient(nil)
	_ StatsRadarrClient = api.RadarrClient(nil)
)

// MetricsReader is the narrow interface for reading search metrics.
type MetricsReader interface {
	TotalSearches() int64
}

// LiveState holds the hot-reloadable runtime state needed by query handlers.
type LiveState struct {
	Cfg       api.ConfigProvider
	Engine    api.SearchEngine
	Sonarr    StatsSonarrClient
	Radarr    StatsRadarrClient
	Providers []api.Provider
}

// Deps holds all dependencies for the query handler family.
type Deps struct {
	QueryDB      QueryStore
	CovDB        api.CoverageStore
	Metrics      MetricsReader
	State        func() *LiveState
	Configured   func() bool
	CountMissing func(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, series []arrapi.Series, movies []arrapi.Movie) int
}

// Handler holds all dependencies for the query handler family.
type Handler struct {
	queryDB      QueryStore
	covDB        api.CoverageStore
	metrics      MetricsReader
	state        func() *LiveState
	configured   func() bool
	countMissing func(ctx context.Context, cfg api.ConfigProvider, db api.CoverageStore, series []arrapi.Series, movies []arrapi.Movie) int
	statsCache   statsCache
}

// New creates a Handler with the given dependencies.
func New(d Deps) *Handler {
	return &Handler{
		queryDB:      d.QueryDB,
		covDB:        d.CovDB,
		metrics:      d.Metrics,
		state:        d.State,
		configured:   d.Configured,
		countMissing: d.CountMissing,
	}
}

// InvalidateStats marks the stats cache stale.
func (h *Handler) InvalidateStats() { h.statsCache.invalidate() }

// StatsInvalidator returns the statsCache as a StatsCacheInvalidator for
// use by the polling subsystem.
func (h *Handler) StatsInvalidator() StatsCacheInvalidator { return &h.statsCache }

// StatsCacheInvalidator is the narrow interface for stats cache invalidation.
type StatsCacheInvalidator interface {
	Invalidate()
}

// --- Shared helpers (delegated to httphelpers package) ---
