package polling

import (
	"context"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/subflux"
)

// PollSonarrClient is the Sonarr surface the history poller uses: import-event
// polling, per-item lookups, exclude-tag resolution, and a post-import rescan.
type PollSonarrClient interface {
	HistorySince(ctx context.Context, since time.Time, eventTypes ...arrapi.EventType) ([]arrapi.HistoryRecord, error)
	SeriesByID(ctx context.Context, id int) (arrapi.Series, error)
	EpisodeByID(ctx context.Context, id int) (arrapi.Episode, error)
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RescanSeries(ctx context.Context, seriesID int) error
}

// PollRadarrClient is the Radarr surface the history poller uses.
type PollRadarrClient interface {
	HistorySince(ctx context.Context, since time.Time, eventTypes ...arrapi.EventType) ([]arrapi.HistoryRecord, error)
	MovieByID(ctx context.Context, id int) (arrapi.Movie, error)
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RescanMovie(ctx context.Context, movieID int) error
}

// tagResolver is the minimal surface excludeTagIDs needs, shared by both
// role clients.
type tagResolver interface {
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
}

// PollerStore is ONE of the 36 methods the store offers. An import cleans up
// the rows of a video that disappeared between poll cycles and does nothing
// else with the store; the poll watermark itself goes through PollCache, which
// the server builds over the store on the poller's behalf.
type PollerStore interface {
	DeleteStateByPaths(ctx context.Context, paths []string) (subflux.CleanupResult, error)
}

// pollerCfg is what a poll cycle reads out of the configuration: how often to
// wake, the exclude-tag list and pacing, the media-root containment check for
// the imported file, and the language targets the import earns. 5 of the 37
// values the config offers — polling reacts to an arr import, so it asks
// nothing about scoring, providers, auth or the server runtime.
type pollerCfg interface {
	PollInterval() time.Duration
	Search() subflux.SearchConfig
	ValidatePath(ctx context.Context, path string) error
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []subflux.SubtitleTarget
	LanguageCodes() []string
}

// PollSource identifies the arr system that produced an import event.
type PollSource string

// PollSourceSonarr and PollSourceRadarr are the two supported PollSource values.
const (
	PollSourceSonarr PollSource = "sonarr"
	PollSourceRadarr PollSource = "radarr"
)

// ImportResult holds the resolved search parameters for a single arr import event.
type ImportResult struct {
	Req       *subflux.SearchRequest
	Source    PollSource
	Label     string
	Targets   []subflux.SubtitleTarget
	RefreshID int
}
