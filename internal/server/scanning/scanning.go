// Package scanning implements the full-scan orchestration engine.
//
// It iterates all wanted episodes and movies from arr APIs, sorts them
// alphabetically, searches for missing subtitles, and records results.
// The scheduling infrastructure (timers, goroutine lifecycle) remains in
// the parent server package.
package scanning

import (
	"context"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/showskip"
)

// Deps holds the narrow dependencies the scan orchestration needs from
// the server. This avoids importing the full Server struct.
type Deps struct {
	DB            ScanStore
	Metrics       ScanMetrics
	Events        EventPublisher
	Activity      ActivityTracker
	Alerts        AlertRecorder
	ShowSkipCache *showskip.Cache
	// SleepCtx is a context-aware sleep function injected to avoid importing provider/.
	SleepCtx func(ctx context.Context, d time.Duration) error
	// ClearCaches clears provider download caches after scan completion.
	ClearCaches func(providers []api.Provider)
}

// ScanStore is the narrow store interface for scan state tracking.
type ScanStore interface {
	RecentlyScanned(ctx context.Context, cutoff time.Time) (map[string]bool, error)
	RecordScanState(ctx context.Context, rec *api.ScanRecord) error
}

// ScanMetrics records scan-level metrics.
type ScanMetrics interface {
	RecordScan(items, found int, dur time.Duration)
	AdaptiveSkip()
}

// EventPublisher publishes events to SSE clients.
type EventPublisher interface {
	PublishCoverageUpdate(mediaType api.MediaType, mediaID string)
	PublishScanStart(action, detail string, source activity.ActivitySource)
	PublishScanDone(action, detail string, source activity.ActivitySource, ok bool)
}

// ActivityTracker manages scan activity lifecycle.
type ActivityTracker interface {
	Start(action, detail string, source activity.ActivitySource) string
	End(id string)
	Fail(id string)
	Progress(id string, current, total int, msg string)
	SetQueued(id string, queued bool)
	IsCancelled(id string) bool
}

// AlertRecorder records alerts visible in the UI.
type AlertRecorder interface {
	Record(category, msg string)
	RecordInfo(msg string)
}

// ScanSonarrClient is the Sonarr surface the full-scan engine needs:
// wanted-episode iteration, exclude-tag resolution, and a post-download rescan.
type ScanSonarrClient interface {
	GetWantedEpisodes(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(arrapi.Series, arrapi.Episode) error) error
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RescanSeries(ctx context.Context, seriesID int) error
}

// ScanRadarrClient is the Radarr surface the full-scan engine needs.
type ScanRadarrClient interface {
	GetWantedMovies(ctx context.Context, excludeTagIDs map[int]struct{}, fn func(arrapi.Movie) error) error
	ResolveExcludeTagIDs(ctx context.Context, tagNames []string, logMissing bool) map[int]struct{}
	RescanMovie(ctx context.Context, movieID int) error
}

// Compile-time assertions: the arrapi-backed role clients satisfy the scan
// surfaces.
var (
	_ ScanSonarrClient = api.SonarrClient(nil)
	_ ScanRadarrClient = api.RadarrClient(nil)
)

// LiveState holds the runtime state needed for a scan pass.
type LiveState struct {
	Cfg         api.ConfigProvider
	Engine      api.SearchEngine
	Sonarr      ScanSonarrClient
	Radarr      ScanRadarrClient
	ShowCounter api.ShowSubtitleCounter
	Providers   []api.Provider
}

// ScanOutcome is a type alias for api.ScanOutcome.
type ScanOutcome = api.ScanOutcome

// Scan outcome constants re-exported from api for local use.
const (
	ScanFound    = api.ScanFound
	ScanSkipped  = api.ScanSkipped
	ScanNoResult = api.ScanNoResult
)
