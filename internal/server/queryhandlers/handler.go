// Package queryhandlers provides read-only HTTP query handlers: subtitle
// state, backoff, manual locks, providers, parsed config, score simulation,
// and dashboard stats.
package queryhandlers

import (
	"context"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/coverage"
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

// queryCfg is the configuration surface the introspection endpoints render:
// /api/config/parsed echoes the parsed settings back to the UI, /api/state/stats
// reports the scan interval, and the per-item state view resolves targets and
// the embedded-codec policy. 11 of the 28 values the config offers — wide for a
// handler package because reporting the configuration IS this family's job, and
// still short of the whole by the auth, logging, port and media-path halves it
// never reads.
//
// It re-lists ResolveTargetsWithFallback and EmbeddedPolicy rather than
// embedding coverage.CountCfg: this is what the handlers read for themselves,
// and a consumer surface that borrows another package's interface stops
// recording its own dependency.
type queryCfg interface {
	Providers() map[api.ProviderID]api.ProviderCfg
	LanguageCodes() []string
	LanguageRulesForUI() api.LanguageRulesJSON
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []api.SubtitleTarget
	EmbeddedPolicy() api.EmbeddedPolicy
	Scores() api.Scores
	Search() api.SearchConfig
	Adaptive() api.AdaptiveConfig
	PostProcess() api.PostProcessConfig
	Sonarr() api.ArrConfig
	Radarr() api.ArrConfig
}

// LiveState holds the hot-reloadable runtime state needed by query handlers.
type LiveState struct {
	Cfg       queryCfg
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
	// each request's live snapshot — 2 of the 28 values the config offers, named
	// from coverage for the same reason the store half named FileReader there:
	// the contract belongs to the function, and re-listing it here is how the
	// two halves of one signature drift apart.
	CountMissing func(ctx context.Context, cfg coverage.CountCfg, series []arrapi.Series, movies []arrapi.Movie) int
}

// Handler holds all dependencies for the query handler family.
type Handler struct {
	queryDB      QueryStore
	covDB        StatsStore
	metrics      MetricsReader
	state        func() *LiveState
	configured   func() bool
	countMissing func(ctx context.Context, cfg coverage.CountCfg, series []arrapi.Series, movies []arrapi.Movie) int
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
