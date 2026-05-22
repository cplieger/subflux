package polling

import (
	"context"
	"time"

	"subflux/internal/api"
)

// HistoryPoller is the narrow interface consumed by the poller subsystem.
// The full api.ArrClient satisfies it via structural typing.
type HistoryPoller interface {
	GetHistorySince(ctx context.Context, since time.Time, eventType api.HistoryEventType) ([]api.HistoryEntry, error)
	GetSeriesByID(ctx context.Context, id int) (*api.Series, error)
	GetEpisodeByID(ctx context.Context, id int) (*api.Episode, error)
	GetMovieByID(ctx context.Context, id int) (*api.Movie, error)
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RefreshSeries(ctx context.Context, seriesID int) error
	RefreshMovie(ctx context.Context, movieID int) error
}

// Compile-time assertion: api.ArrClient satisfies HistoryPoller.
var _ HistoryPoller = api.ArrClient(nil)

// PollerStore is the narrow store interface consumed by poll-import processing.
// Only DeleteStateByPaths is needed to clean up stale entries when a video
// file disappears between poll cycles.
type PollerStore interface {
	DeleteStateByPaths(ctx context.Context, paths []string) (api.CleanupResult, error)
}

// Compile-time assertion: api.Store satisfies PollerStore.
var _ PollerStore = api.Store(nil)

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

type ImportResult struct {
	Req       *api.SearchRequest
	Source    PollSource
	Label     string
	Targets   []api.SubtitleTarget
	RefreshID int
}
