package polling

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/httpx/v3"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/cache"
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
	deps          Deps
	stateFunc     StateFunc
	tagCache      *cache.Cache[map[int]struct{}]
	importRetries map[string]int
	work          chan sourceBatch
	detectHigh    map[api.PollKey]time.Time
	retryMu       sync.Mutex
	detectMu      sync.Mutex
}

// sourceBatch is one detection fetch handed to the executor: the entries a
// single GetHistorySince returned, plus the cursor that fetch used (the base
// advanceWatermark compares against after execution).
type sourceBatch struct {
	source  PollSource
	key     api.PollKey
	since   time.Time
	entries []arrapi.HistoryRecord
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
		work:          make(chan sourceBatch, 8),
		detectHigh:    make(map[api.PollKey]time.Time),
	}
}

// Adaptive-poll burst window. When a poll cycle observes activity (any
// imported-history entries), subsequent cycles fire at burstPollInterval
// instead of the configured PollInterval until burstPollWindow has passed
// without further activity. Captures most user imports inside 5s with no
// configuration, while keeping the steady-state load at the configured
// 30s interval: imports cluster (a download batch lands over minutes), so
// one observed import predicts more shortly after.
const (
	burstPollInterval = 5 * time.Second
	burstPollWindow   = 2 * time.Minute
)

// Run polls on a timer, re-reading the interval from live config after each
// poll so hot-reloaded interval changes take effect immediately. When
// PollOnce reports activity, the next interval is shortened to
// burstPollInterval and stays there until burstPollWindow passes idle.
//
// Detection and execution are decoupled (P12): the timer loop only FETCHES
// history and enqueues batches, so new imports are observed on schedule even
// while a large batch is still being worked through; the single-worker
// executor goroutine drains the queue with the existing pacing and watermark
// semantics.
func (p *Poller) Run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() {
		p.runExecutor(ctx)
	})
	defer wg.Wait()

	var lastActivity time.Time
	pollTimer := time.NewTimer(p.stateFunc().Cfg.PollInterval())
	defer pollTimer.Stop()

	for {
		select {
		case <-pollTimer.C:
			// Heal a dirty durable cursor on the heartbeat (S13).
			p.deps.PollCache.RetryDirty(ctx)
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

// PollOnce checks both Sonarr and Radarr for new import events and enqueues
// what it finds for the executor; it performs NO import processing itself.
// Returns the number of imported-history entries observed across both arr
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
			sonarrCount.Store(int32(p.detectSonarr(gCtx, ls))) //nolint:gosec // G115: poll count fits int32
			return nil
		})
	}
	if ls.Radarr != nil {
		if p.deps.PollCache.Get(ctx, api.PollKeyRadarr).IsZero() {
			p.deps.PollCache.Set(ctx, api.PollKeyRadarr, time.Now().UTC())
		}
		g.Go(func() error {
			radarrCount.Store(int32(p.detectRadarr(gCtx, ls))) //nolint:gosec // G115: poll count fits int32
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		slog.Warn("poll cycle error", "error", err)
	}

	// Detection-only timing: with execution moved to the queue, this WARN is
	// reachable only through genuinely slow arr history fetches, never
	// through an execution backlog.
	if dur := time.Since(start); dur > ls.Cfg.PollInterval() {
		slog.Warn("poll cycle exceeded interval",
			"duration", dur.String(),
			"interval", ls.Cfg.PollInterval().String())
	}

	return int(sonarrCount.Load()) + int(radarrCount.Load())
}

// detectSince returns the cursor a detection fetch should use: the durable
// watermark, or the in-memory fetched-through position when it is ahead
// (entries between the two are already queued for execution).
func (p *Poller) detectSince(ctx context.Context, key api.PollKey) time.Time {
	since := p.deps.PollCache.Get(ctx, key)
	p.detectMu.Lock()
	if h, ok := p.detectHigh[key]; ok && h.After(since) {
		since = h
	}
	p.detectMu.Unlock()
	return since
}

// enqueue hands a detected batch to the executor and advances the in-memory
// fetched-through cursor past it. When the queue is full the batch is
// deferred instead: the cursor stays put and the same entries are re-fetched
// next cycle — bounded backpressure with no loss.
func (p *Poller) enqueue(b *sourceBatch) {
	select {
	case p.work <- *b:
		var latest time.Time
		for i := range b.entries {
			if b.entries[i].Date.After(latest) {
				latest = b.entries[i].Date
			}
		}
		if !latest.IsZero() {
			p.detectMu.Lock()
			p.detectHigh[b.key] = latest.Add(time.Millisecond)
			p.detectMu.Unlock()
		}
	default:
		slog.Warn("poll: executor queue full, batch deferred to next cycle",
			"source", b.source, "entries", len(b.entries))
	}
}

// rewindDetection pulls the fetched-through cursor back to the durable
// watermark so the next detection re-fetches from it (the retry transport
// for transiently-failed entries, and the recovery path for dropped batches).
func (p *Poller) rewindDetection(ctx context.Context, key api.PollKey) {
	durable := p.deps.PollCache.Get(ctx, key)
	p.detectMu.Lock()
	p.detectHigh[key] = durable
	p.detectMu.Unlock()
}

// runExecutor is the single worker draining detected batches. One batch
// executes at a time (imports are paced by scan_delay anyway), so provider
// work never runs concurrently for poll-driven imports; same-item collisions
// with scans are handled by the engine's per-media gate.
func (p *Poller) runExecutor(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case b := <-p.work:
			p.executeBatch(ctx, &b)
		}
	}
}

// executeBatch processes one detected history batch: per-entry import
// handling with scan_delay pacing, retry accounting, and the durable
// watermark advancement — exactly the semantics the pre-P12 inline loop had.
func (p *Poller) executeBatch(ctx context.Context, b *sourceBatch) {
	ls := p.stateFunc()

	var resolver tagResolver
	var process func(context.Context, *LiveState, *arrapi.HistoryRecord, map[int]struct{}) (bool, bool)
	switch b.source {
	case PollSourceSonarr:
		if ls.Sonarr == nil {
			p.rewindDetection(ctx, b.key)
			return
		}
		resolver = ls.Sonarr
		process = p.processSonarrImport
	case PollSourceRadarr:
		if ls.Radarr == nil {
			p.rewindDetection(ctx, b.key)
			return
		}
		resolver = ls.Radarr
		process = p.processRadarrImport
	default:
		return
	}

	searchCfg := ls.Cfg.Search()
	scanDelay := searchCfg.ScanDelay
	excludeIDs := p.getExcludeTagIDs(ctx, resolver, string(b.source),
		searchCfg.ExcludeArrTags, ls.Cfg.PollInterval())

	latest, oldestFailed, completed := p.runBatchEntries(ctx, ls, b, process, excludeIDs, scanDelay)
	if !completed {
		// Cancelled mid-batch: leave the durable cursor untouched so a
		// restart replays the whole batch (at-least-once, as before).
		return
	}

	p.advanceWatermark(ctx, b.key, b.since, latest, oldestFailed)
	if !oldestFailed.IsZero() {
		// A transiently-failed entry holds the durable watermark below
		// itself; rewind detection to it so the next cycle re-fetches the
		// entry (today's retry spacing: one attempt per poll cycle).
		p.rewindDetection(ctx, b.key)
	}
}

// runBatchEntries iterates a batch's deduplicated entries through the given
// import processor, pacing only BETWEEN entries that actually queried
// providers: the delay spaces provider traffic, and skip paths (gone file,
// tag-excluded, metadata-fetch retries) issue none — their 1-2 arr metadata
// calls are local-service traffic the delay was never for. Sleeping before
// the next working entry rather than after every entry also removes the dead
// sleep between the batch's last entry and the watermark advance, narrowing
// the cancel-replay window. Reports the newest entry date seen, the oldest
// transiently-failed entry, and whether the batch ran to completion (false =
// cancelled mid-batch; the caller must leave the durable cursor untouched).
func (p *Poller) runBatchEntries(ctx context.Context, ls *LiveState, b *sourceBatch,
	process func(context.Context, *LiveState, *arrapi.HistoryRecord, map[int]struct{}) (bool, bool),
	excludeIDs map[int]struct{}, scanDelay time.Duration,
) (latest, oldestFailed time.Time, completed bool) {
	seen := make(map[string]bool)
	needPace := false

	for i := range b.entries {
		entry := b.entries[i]
		if entry.Date.After(latest) {
			latest = entry.Date
		}
		path := entry.ImportedPath()
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true

		if needPace {
			if err := httpx.SleepCtx(ctx, scanDelay); err != nil {
				return latest, oldestFailed, false
			}
		}

		retryable, queried := process(ctx, ls, &entry, excludeIDs)
		needPace = queried
		p.trackImportOutcome(b.source, entry.ID, entry.Date, path, retryable, &oldestFailed)
	}
	return latest, oldestFailed, true
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

// detectSonarr fetches new Sonarr import events and enqueues them for the
// executor. Returns the number of imported-history entries observed (used by
// PollOnce to drive adaptive-burst polling).
func (p *Poller) detectSonarr(ctx context.Context, ls *LiveState) int {
	since := p.detectSince(ctx, api.PollKeySonarr)
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
	p.enqueue(&sourceBatch{
		source: PollSourceSonarr, key: api.PollKeySonarr,
		since: since, entries: entries,
	})
	return len(entries)
}

// detectRadarr fetches new Radarr import events and enqueues them for the
// executor. Returns the number of imported-history entries observed (used by
// PollOnce to drive adaptive-burst polling).
func (p *Poller) detectRadarr(ctx context.Context, ls *LiveState) int {
	since := p.detectSince(ctx, api.PollKeyRadarr)
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
	p.enqueue(&sourceBatch{
		source: PollSourceRadarr, key: api.PollKeyRadarr,
		since: since, entries: entries,
	})
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
