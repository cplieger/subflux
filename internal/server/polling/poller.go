package polling

import (
	"context"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/api"
)

// PollSonarrClient is the Sonarr surface the history poller uses: import-event
// polling, per-item lookups, exclude-tag resolution, and a post-import rescan.
type PollSonarrClient interface {
	GetHistorySince(ctx context.Context, since time.Time, eventTypes ...arrapi.EventType) ([]arrapi.HistoryRecord, error)
	GetSeriesByID(ctx context.Context, id int) (arrapi.Series, error)
	GetEpisodeByID(ctx context.Context, id int) (arrapi.Episode, error)
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RescanSeries(ctx context.Context, seriesID int) error
}

// PollRadarrClient is the Radarr surface the history poller uses.
type PollRadarrClient interface {
	GetHistorySince(ctx context.Context, since time.Time, eventTypes ...arrapi.EventType) ([]arrapi.HistoryRecord, error)
	GetMovieByID(ctx context.Context, id int) (arrapi.Movie, error)
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RescanMovie(ctx context.Context, movieID int) error
}

// tagResolver is the minimal surface getExcludeTagIDs needs, shared by both
// role clients.
type tagResolver interface {
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
}

// Compile-time assertions: the arrapi-backed role clients satisfy the poller
// surfaces.
var (
	_ PollSonarrClient = api.SonarrClient(nil)
	_ PollRadarrClient = api.RadarrClient(nil)
)

// PollerStore is ONE of the 36 methods the store offers. An import cleans up
// the rows of a video that disappeared between poll cycles and does nothing
// else with the store; the poll watermark itself goes through PollCache, which
// the server builds over the store on the poller's behalf.
type PollerStore interface {
	DeleteStateByPaths(ctx context.Context, paths []string) (api.CleanupResult, error)
}

// PollerCfg is the narrow configuration interface consumed by the poller
// subsystem. The full api.ConfigProvider satisfies it via structural typing.
// Declaring it here documents the poller's actual dependency surface.
type PollerCfg interface {
	PollInterval() time.Duration
	Search() api.SearchConfig
	ValidatePath(ctx context.Context, path string) error
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []api.SubtitleTarget
	LanguageCodes() []string
}

// Compile-time assertion: api.ConfigProvider satisfies PollerCfg.
var _ PollerCfg = api.ConfigProvider(nil)

// PollSource identifies the arr system that produced an import event.
type PollSource string

// Poll source constants.
const (
	PollSourceSonarr PollSource = "sonarr"
	PollSourceRadarr PollSource = "radarr"
)

// ImportResult holds the resolved search parameters for a single arr import event.
type ImportResult struct {
	Req       *api.SearchRequest
	Source    PollSource
	Label     string
	Targets   []api.SubtitleTarget
	RefreshID int
}
