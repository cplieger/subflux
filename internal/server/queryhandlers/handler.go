// Package queryhandlers provides read-only HTTP query handlers: subtitle
// state, backoff, manual locks, providers, parsed config, score simulation,
// and dashboard stats.
package queryhandlers

import (
	"context"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/api"
)

// QueryStore is the read-only introspection surface behind /api/state,
// /api/backoff and /api/locks: 5 of the 36 methods the store offers, all of
// them reads. Nothing on this path writes, which is why no write appears here.
type QueryStore interface {
	GetState(ctx context.Context, q *api.StateQuery) ([]api.StateEntry, error)
	GetBackoffItems(ctx context.Context) ([]api.BackoffEntry, error)
	GetBackoffByPrefix(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.BackoffEntry, error)
	GetManualLocks(ctx context.Context) ([]api.ManualLockEntry, error)
	Stats(ctx context.Context) (downloads, attempts int, err error)
}

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

// StatsStore is the two aggregate reads the stats endpoint reports: how many
// subtitle files are tracked, and when the last scan finished. 2 of the twelve
// methods the coverage surface offers — /api/state/stats renders a summary, so
// it never touches a per-item row.
type StatsStore interface {
	TotalSubtitleFiles(ctx context.Context) (int, error)
	LastScanTime(ctx context.Context) (string, error)
}

// MetricsReader is the narrow interface for reading search metrics.
type MetricsReader interface {
	TotalSearches() int64
}

// queryEngine is the read-and-reset surface the introspection endpoints use:
// simulate a score for POST /api/score, report provider-timeout state, and
// clear it. Three of the engine's eight methods — these handlers never search,
// never download and never post-process, so the other five stay out of reach.
type queryEngine interface {
	SimulateScore(mediaType api.MediaType, videoRelease, subRelease string, matchedBy api.MatchMethod) api.ScoreResult
	ProviderTimeouts() (status map[api.ProviderID]api.ProviderStatus, enabled bool)
	ResetTimeouts()
}

// LiveState holds the hot-reloadable runtime state needed by query handlers.
type LiveState struct {
	Cfg       api.ConfigProvider
	Engine    queryEngine
	Sonarr    StatsSonarrClient
	Radarr    StatsRadarrClient
	Providers []api.Provider
}

// Deps holds all dependencies for the query handler family.
type Deps struct {
	QueryDB    QueryStore
	CovDB      StatsStore
	Metrics    MetricsReader
	State      func() *LiveState
	Configured func() bool
	// CountMissing counts missing subtitle targets. It is handed to this package
	// ALREADY BOUND to the store: the count reads subtitle-file rows, this
	// package never does, and taking the store as a parameter just to forward it
	// is what made these handlers look like a store consumer twelve methods
	// wide. cfg stays a parameter because it is hot-reloadable and arrives with
	// each request's live snapshot.
	CountMissing func(ctx context.Context, cfg api.ConfigProvider, series []arrapi.Series, movies []arrapi.Movie) int
}

// Handler holds all dependencies for the query handler family.
type Handler struct {
	queryDB      QueryStore
	covDB        StatsStore
	metrics      MetricsReader
	state        func() *LiveState
	configured   func() bool
	countMissing func(ctx context.Context, cfg api.ConfigProvider, series []arrapi.Series, movies []arrapi.Movie) int
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
