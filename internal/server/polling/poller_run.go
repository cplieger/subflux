package polling

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/cache"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"golang.org/x/sync/errgroup"
)

// PollerMetrics is the narrow metrics interface consumed by the poller.
type PollerMetrics interface {
	RecordImport(source api.PollKey)
}

// PollerEvents is the narrow events interface consumed by the poller.
type PollerEvents interface {
	Publish(e events.Event)
}

// StatsCacheInvalidator is the narrow interface for stats cache invalidation.
type StatsCacheInvalidator interface {
	Invalidate()
}

// Deps holds all dependencies for the Poller.
type Deps struct {
	PollCache  *PollCache
	Store      PollerStore
	Metrics    PollerMetrics
	Alerts     activity.WarnRecorder
	Events     PollerEvents
	StatsCache StatsCacheInvalidator
}

// LiveState holds the hot-reloadable runtime state the poller reads each cycle.
type LiveState struct {
	Cfg    PollerCfg
	Engine api.SearchEngine
	Sonarr PollSonarrClient
	Radarr PollRadarrClient
}

// StateFunc returns the current live state. Called each poll cycle to pick
// up hot-reloaded config/clients.
type StateFunc func() *LiveState

// Poller polls Sonarr/Radarr history APIs for new import events and
// processes each through the search engine.
type Poller struct {
	deps      Deps
	stateFunc StateFunc

	tagCache *cache.Cache[map[int]struct{}]

	// importRetries counts consecutive transient failures per history entry
	// (key "source:entryID"), so the poll watermark is held back for a
	// bounded number of cycles instead of either dropping a failed import
	// permanently or retrying it forever. Guarded by retryMu: PollOnce polls
	// Sonarr and Radarr concurrently. Entries are deleted on success or
	// give-up, so the map only ever holds currently-failing items.
	importRetries map[string]int
	retryMu       sync.Mutex
}

// maxImportRetries is how many poll cycles a transiently-failing import
// (arr metadata fetch error, e.g. Sonarr restarting mid-poll) holds the
// watermark back before the poller gives up on it. Three retries at the
// default 30s interval rides out a typical arr restart; anything longer is
// left to the next scheduled full scan, which covers the item anyway.
const maxImportRetries = 3

// NewPoller creates a Poller with the given dependencies. In
// unconfigured mode (server.New called without WithConfig) stateFunc
// may return a LiveState with a nil Cfg; we fall back to a sane
// default TTL so construction does not panic. Run() reads the live
// PollInterval per cycle and the per-entry expiry is governed by the
// cache TTL set here, so the first poll after configuration uses the
// configured interval naturally; the tag cache lifetime is the only
// thing tied to this initial value.
func NewPoller(deps Deps, stateFunc StateFunc) *Poller { //nolint:gocritic // hugeParam: callers pass by value
	const defaultPollInterval = 2 * time.Minute
	ttl := 2 * defaultPollInterval
	if ls := stateFunc(); ls != nil && ls.Cfg != nil {
		ttl = 2 * ls.Cfg.PollInterval()
	}
	return &Poller{
		deps:          deps,
		stateFunc:     stateFunc,
		tagCache:      cache.New[map[int]struct{}](ttl),
		importRetries: make(map[string]int),
	}
}

// Adaptive-poll burst window. When a poll cycle observes activity (any
// imported-history entries), subsequent cycles fire at burstPollInterval
// instead of the configured PollInterval until burstPollWindow has passed
// without further activity. Captures most user imports inside 5s with no
// configuration, while keeping the steady-state load at the configured
// 30s interval. Constants per `_architecture.md` companion notes.
const (
	burstPollInterval = 5 * time.Second
	burstPollWindow   = 2 * time.Minute
)

// Run polls on a timer, re-reading the interval from live config after each
// poll so hot-reloaded interval changes take effect immediately. When
// PollOnce reports activity, the next interval is shortened to
// burstPollInterval and stays there until burstPollWindow passes idle.
func (p *Poller) Run(ctx context.Context) {
	var lastActivity time.Time
	pollTimer := time.NewTimer(p.stateFunc().Cfg.PollInterval())
	defer pollTimer.Stop()

	for {
		select {
		case <-pollTimer.C:
			if n := p.PollOnce(ctx); n > 0 {
				lastActivity = time.Now()
			}
			interval := p.stateFunc().Cfg.PollInterval()
			if !lastActivity.IsZero() &&
				time.Since(lastActivity) < burstPollWindow &&
				burstPollInterval < interval {
				interval = burstPollInterval
			}
			pollTimer.Reset(interval)
		case <-ctx.Done():
			return
		}
	}
}

// PollOnce checks both Sonarr and Radarr for new import events. Returns
// the number of imported-history entries observed across both arr
// clients (used by Run to decide whether to enter adaptive-burst mode).
func (p *Poller) PollOnce(ctx context.Context) int {
	start := time.Now()
	ls := p.stateFunc()

	var sonarrCount, radarrCount atomic.Int32

	g, gCtx := errgroup.WithContext(ctx)
	if ls.Sonarr != nil {
		if p.deps.PollCache.Get(ctx, api.PollKeySonarr).IsZero() {
			p.deps.PollCache.Set(ctx, api.PollKeySonarr, time.Now().UTC())
		}
		g.Go(func() error {
			sonarrCount.Store(int32(p.pollSonarr(gCtx, ls))) //nolint:gosec // G115: poll count fits int32
			return nil
		})
	}
	if ls.Radarr != nil {
		if p.deps.PollCache.Get(ctx, api.PollKeyRadarr).IsZero() {
			p.deps.PollCache.Set(ctx, api.PollKeyRadarr, time.Now().UTC())
		}
		g.Go(func() error {
			radarrCount.Store(int32(p.pollRadarr(gCtx, ls))) //nolint:gosec // G115: poll count fits int32
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		slog.Warn("poll cycle error", "error", err)
	}

	if dur := time.Since(start); dur > ls.Cfg.PollInterval() {
		slog.Warn("poll cycle exceeded interval",
			"duration", dur.String(),
			"interval", ls.Cfg.PollInterval().String())
	}

	return int(sonarrCount.Load()) + int(radarrCount.Load())
}

// getExcludeTagIDs returns cached tag IDs if still valid, otherwise resolves
// them from the arr client and caches with singleflight deduplication.
func (p *Poller) getExcludeTagIDs(ctx context.Context, client tagResolver, cacheKey string,
	tags []string, _ time.Duration,
) map[int]struct{} {
	ids, err := p.tagCache.GetOrFetchCtx(ctx, cacheKey, func(ctx context.Context) (map[int]struct{}, error) {
		return client.ResolveExcludeTagIDs(ctx, tags, false), nil
	})
	if err != nil {
		return nil
	}
	return ids
}

// pollSonarr fetches new Sonarr import events and processes them.
// Returns the number of imported-history entries observed (used by
// PollOnce to drive adaptive-burst polling).
func (p *Poller) pollSonarr(ctx context.Context, ls *LiveState) int {
	since := p.deps.PollCache.Get(ctx, api.PollKeySonarr)
	entries, err := ls.Sonarr.GetHistorySince(ctx, since, arrapi.EventDownloadImported)
	if err != nil {
		slog.Warn("sonarr poll failed", "since", since.UTC().Format(time.RFC3339), "error", err)
		return 0
	}
	if len(entries) == 0 {
		slog.Debug("sonarr poll: no new events")
		return 0
	}

	slog.Info("sonarr poll: new events", "count", len(entries))
	searchCfg := ls.Cfg.Search()
	scanDelay := searchCfg.ScanDelay

	excludeIDs := p.getExcludeTagIDs(ctx, ls.Sonarr, string(PollSourceSonarr),
		searchCfg.ExcludeArrTags, ls.Cfg.PollInterval())

	seen := make(map[string]bool)
	var latest, oldestFailed time.Time

	for _, entry := range entries {
		if entry.Date.After(latest) {
			latest = entry.Date
		}
		path := entry.ImportedPath()
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true

		retryable := p.processSonarrImport(ctx, ls, &entry, excludeIDs)
		p.trackImportOutcome(PollSourceSonarr, entry.ID, entry.Date, path, retryable, &oldestFailed)

		if err := httputil.SleepCtx(ctx, scanDelay); err != nil {
			return len(entries)
		}
	}

	p.advanceWatermark(ctx, api.PollKeySonarr, since, latest, oldestFailed)
	return len(entries)
}

// pollRadarr fetches new Radarr import events and processes them.
// Returns the number of imported-history entries observed (used by
// PollOnce to drive adaptive-burst polling).
func (p *Poller) pollRadarr(ctx context.Context, ls *LiveState) int {
	since := p.deps.PollCache.Get(ctx, api.PollKeyRadarr)
	entries, err := ls.Radarr.GetHistorySince(ctx, since, arrapi.EventDownloadImported)
	if err != nil {
		slog.Warn("radarr poll failed", "since", since.UTC().Format(time.RFC3339), "error", err)
		return 0
	}
	if len(entries) == 0 {
		slog.Debug("radarr poll: no new events")
		return 0
	}

	slog.Info("radarr poll: new events", "count", len(entries))
	searchCfg := ls.Cfg.Search()
	scanDelay := searchCfg.ScanDelay

	excludeIDs := p.getExcludeTagIDs(ctx, ls.Radarr, string(PollSourceRadarr),
		searchCfg.ExcludeArrTags, ls.Cfg.PollInterval())

	var latest, oldestFailed time.Time

	for _, entry := range entries {
		if entry.Date.After(latest) {
			latest = entry.Date
		}
		path := entry.ImportedPath()
		if path == "" {
			continue
		}

		retryable := p.processRadarrImport(ctx, ls, &entry, excludeIDs)
		p.trackImportOutcome(PollSourceRadarr, entry.ID, entry.Date, path, retryable, &oldestFailed)

		if err := httputil.SleepCtx(ctx, scanDelay); err != nil {
			return len(entries)
		}
	}

	p.advanceWatermark(ctx, api.PollKeyRadarr, since, latest, oldestFailed)
	return len(entries)
}

// retryKey is the importRetries map key for one history entry.
func retryKey(source PollSource, entryID int) string {
	return fmt.Sprintf("%s:%d", source, entryID)
}

// trackImportOutcome records the retry outcome for one processed history
// entry. A retryable failure notes it (advancing oldestFailed to the earliest
// failed entry date so the watermark holds there); success — or a failure
// that exhausted its retry budget inside noteImportFailure — clears the
// entry's retry counter so the watermark can move past it.
func (p *Poller) trackImportOutcome(source PollSource, entryID int, entryDate time.Time, path string, retryable bool, oldestFailed *time.Time) {
	if retryable && p.noteImportFailure(retryKey(source, entryID), path) {
		if oldestFailed.IsZero() || entryDate.Before(*oldestFailed) {
			*oldestFailed = entryDate
		}
		return
	}
	p.clearImportRetry(retryKey(source, entryID))
}

// noteImportFailure records one transient failure for the entry and reports
// whether it should be retried (hold the watermark) or given up. After
// maxImportRetries consecutive failures the entry is abandoned with a WARN —
// the next scheduled full scan covers the item — and its counter is cleared.
func (p *Poller) noteImportFailure(key, path string) bool {
	p.retryMu.Lock()
	p.importRetries[key]++
	attempt := p.importRetries[key]
	if attempt >= maxImportRetries {
		delete(p.importRetries, key)
	}
	p.retryMu.Unlock()

	if attempt >= maxImportRetries {
		slog.Warn("poll: giving up on import after repeated arr metadata failures; the next full scan covers it",
			"path", path, "attempts", maxImportRetries)
		return false
	}
	slog.Debug("poll: import failed transiently, will retry next cycle",
		"path", path, "attempt", attempt)
	return true
}

// clearImportRetry drops the retry counter for an entry that succeeded (or
// was permanently skipped).
func (p *Poller) clearImportRetry(key string) {
	p.retryMu.Lock()
	delete(p.importRetries, key)
	p.retryMu.Unlock()
}

// advanceWatermark persists the poll cursor after a pass. Normally it moves
// just past the newest entry; while a transiently-failed entry is being
// retried it is held just BEFORE that entry so the next GetHistorySince
// re-fetches it. Entries after the failed one are re-fetched too — that is
// cheap and bounded: re-processing an already-handled import finds its
// targets covered and skips, and the hold lasts at most maxImportRetries
// cycles. The cursor never moves backward past `since`.
func (p *Poller) advanceWatermark(ctx context.Context, key api.PollKey, since, latest, oldestFailed time.Time) {
	target := latest
	if !oldestFailed.IsZero() {
		target = oldestFailed.Add(-time.Millisecond)
	}
	if target.IsZero() || !target.After(since) {
		return
	}
	p.deps.PollCache.Set(ctx, key, target.Add(time.Millisecond))
}
