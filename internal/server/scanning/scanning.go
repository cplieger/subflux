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
	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/showskip"
)

// Deps holds the narrow dependencies the scan orchestration needs from
// the server. This avoids importing the full Server struct.
type Deps struct {
	DB ScanStore
	// Backoff feeds season-tracker earlyStop seeding from existing
	// adaptive-backoff rows. Optional: nil disables seeding.
	Backoff       BackoffPrefixReader
	Metrics       ScanMetrics
	Events        EventPublisher
	Activity      ActivityTracker
	Alerts        AlertRecorder
	ShowSkipCache *showskip.Cache
	// ClearCaches clears provider download caches after scan completion.
	ClearCaches func(providers []api.Provider)
}

// ScanStore is the narrow store interface for scan state tracking.
type ScanStore interface {
	RecentlyScanned(ctx context.Context, cutoff time.Time) (map[string]bool, error)
	RecordScanState(ctx context.Context, rec *api.ScanRecord) error
	// Scan-cycle mark (duration-aware resume): set when a full scan begins,
	// cleared on normal completion. A dangling mark at the next scan start
	// means the previous cycle was interrupted; the resume cutoff extends
	// back to that cycle's start so a pass longer than scan_interval keeps
	// its early segment in the resume set. ScanCycleStart returns the zero
	// time when no mark is stored.
	ScanCycleStart(ctx context.Context) (time.Time, error)
	SetScanCycleStart(ctx context.Context, t time.Time) error
	ClearScanCycleStart(ctx context.Context) error
}

// ScanMetrics records scan-level metrics.
type ScanMetrics interface {
	RecordScan(items, found int, dur time.Duration)
	AdaptiveSkip()
}

// EventPublisher publishes events to SSE clients. Scan events carry the
// activity id (both) and the terminal outcome (scan:done).
type EventPublisher interface {
	PublishCoverageUpdate(mediaType api.MediaType, mediaID string)
	PublishScanStart(action, detail string, source activity.ActivitySource, actID string)
	PublishScanDone(action, detail string, source activity.ActivitySource, actID string, outcome activity.Outcome)
}

// ActivityTracker manages scan activity lifecycle.
type ActivityTracker interface {
	Start(action, detail string, source activity.ActivitySource) string
	StartScan(action, detail string, source activity.ActivitySource,
		scope activity.ScanScope, role auth.Role) (id string, existing bool)
	End(id string)
	Fail(id string)
	FinishCancelled(id string)
	Progress(id string, current, total int, msg string)
	SetQueued(id string, queued bool)
	IsCancelled(id string) bool
}

// FinishScanActivity applies a runner's terminal outcome to its activity
// entry and publishes the scan:done event. This is the ONLY outcome→terminal
// mapping: completed→End, failed→Fail, cancelled→FinishCancelled (the
// terminal Done+Cancelled+EndedAt state). Shutdown performs no user-facing
// marking and publishes nothing — the process is exiting and the in-memory
// ring dies with it; collapsing shutdown into failed or cancelled would lie
// on both.
//
// unregister — the stop-registration release — runs FIRST, before the
// terminal transition: the instant Done becomes observable the entry must
// no longer report cancellable, so a cancel arriving after completion
// answers 409, never a 204 for work that is already done. Callers keep a
// deferred unregister as the panic fallback; the release is idempotent.
func FinishScanActivity(unregister func(), tracker ActivityTracker, events EventPublisher,
	actID, action, detail string, source activity.ActivitySource, outcome activity.Outcome,
) {
	unregister()
	switch outcome {
	case activity.OutcomeCompleted:
		tracker.End(actID)
	case activity.OutcomeFailed:
		tracker.Fail(actID)
	case activity.OutcomeCancelled:
		tracker.FinishCancelled(actID)
	case activity.OutcomeShutdown:
		return
	}
	events.PublishScanDone(action, detail, source, actID, outcome)
}

// stopRequested reports (without blocking) whether the stop signal fired.
// Scan loops check it between items; the current item is never interrupted.
func stopRequested(stop <-chan struct{}) bool {
	select {
	case <-stop:
		return true
	default:
		return false
	}
}

// waitOrStop pauses for the inter-item scan delay, ending early when the
// server context is cancelled (shutdown) or the stop signal fires
// (graceful cancel). It returns the outcome that should terminate the scan,
// or "" to continue with the next item.
func waitOrStop(ctx context.Context, stop <-chan struct{}, d time.Duration) activity.Outcome {
	if err := ctx.Err(); err != nil {
		return activity.OutcomeShutdown
	}
	if stopRequested(stop) {
		return activity.OutcomeCancelled
	}
	if d <= 0 {
		return ""
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return activity.OutcomeShutdown
	case <-stop:
		return activity.OutcomeCancelled
	case <-t.C:
		return ""
	}
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

// buildSeedDeps assembles the season tracker's earlyStop seeding inputs from
// the live scan state: the store's backoff reader, the enabled provider set,
// and the adaptive ceiling. Seeding is disabled (nil Backoff) when adaptive
// backoff itself is disabled — with no ladder there are no meaningful rows.
func buildSeedDeps(deps *Deps, ls *LiveState) seedDeps {
	adaptive := ls.Cfg.Adaptive()
	if !adaptive.Enabled || deps.Backoff == nil {
		return seedDeps{}
	}
	enabled := make(map[api.ProviderID]struct{}, len(ls.Providers))
	for _, p := range ls.Providers {
		enabled[p.Name()] = struct{}{}
	}
	return seedDeps{
		Backoff:     deps.Backoff,
		Enabled:     enabled,
		MaxAttempts: adaptive.MaxAttempts,
		Now:         time.Now,
	}
}

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
	ScanFound     = api.ScanFound
	ScanSkipped   = api.ScanSkipped
	ScanNoResult  = api.ScanNoResult
	ScanBackedOff = api.ScanBackedOff
)
