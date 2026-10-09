package polling

import (
	"context"
	"log/slog"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/keyenc"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/errgroup"
)

// PollerMetrics is the metrics surface the poller consumes.
type PollerMetrics interface {
	RecordImport(source subflux.PollKey)
}

// pollerEvents is the events surface the poller consumes.
type pollerEvents interface {
	Publish(e events.Event)
}

// statsCacheInvalidator is the narrow interface for stats cache invalidation.
type statsCacheInvalidator interface {
	Invalidate()
}

// warnRecorder records an actionable warning for the UI's alert list.
// activity.AlertLog satisfies it structurally.
type warnRecorder interface {
	RecordWarn(source, msg string)
}

// Deps holds all dependencies for the Poller.
type Deps struct {
	PollCache  *PollCache
	Store      PollerStore
	Metrics    PollerMetrics
	Alerts     warnRecorder
	Events     pollerEvents
	StatsCache statsCacheInvalidator
	Media      mediaGuard
	Presence   presenceGuard
}

// presenceGuard decides whether an imported video is gone or its media root
// unreadable; *mediapresence.Checker satisfies it.
type presenceGuard interface {
	Gone(ctx context.Context, path string) (bool, error)
	Unavailable(path string) (root string, unavailable bool)
}

// mediaGuard is the media writer surface the poller consumes;
// *mediawrite.Writer satisfies it.
type mediaGuard interface {
	scanning.MediaGuard
	RecheckInterval() time.Duration
}

// importSearcher is the one thing the poller asks of the search engine: search
// the language targets of an item an arr just imported. One of the engine's
// eight methods — an import is a search and nothing else, so the poller never
// sees score simulation, timeout state, or post-download processing.
type importSearcher interface {
	SearchTargets(ctx context.Context, req *subflux.SearchRequest, videoPath string, targets []subflux.SubtitleTarget) (subflux.SearchResult, error)
}

// LiveState holds the hot-reloadable runtime state the poller reads each cycle.
type LiveState struct {
	Cfg    pollerCfg
	Engine importSearcher
	Sonarr PollSonarrClient
	Radarr PollRadarrClient
}

// StateFunc returns the current live state, called each poll cycle to pick
// up hot-reloaded config/clients.
type StateFunc func() *LiveState

// Poller polls Sonarr/Radarr history APIs for new import events and
// processes each through the search engine.
type Poller struct {
	deps          Deps
	stateFunc     StateFunc
	importRetries map[string]int
	work          chan sourceBatch
	// detectHigh, detectGen, behind, heldOn and observed are guarded by
	// detectMu.
	detectHigh map[subflux.PollKey]time.Time
	// detectGen counts the writes of detectHigh, so a discarded batch rewinds
	// only while its own write is still the newest.
	detectGen map[subflux.PollKey]uint64
	// behind marks a source whose durable cursor is deliberately held below
	// entries already detected: until a batch from that cursor completes,
	// a batch detected past it is discarded rather than run.
	behind map[subflux.PollKey]bool
	heldOn map[subflux.PollKey]heldFolder
	// observed is the newest entry date detection has returned per source,
	// so a re-fetch of held entries is not counted as new activity.
	observed map[subflux.PollKey]time.Time
	retryMu  sync.Mutex
	detectMu sync.Mutex
}

// heldFolder is the folder whose refusal holds a behind source, and when the
// hold was last decided from a fresh read of the arr.
type heldFolder struct {
	at     time.Time
	folder string
}

// sourceBatch is one detection fetch handed to the executor: the entries a
// single HistorySince returned, plus the cursor that fetch used (the base
// advanceWatermark compares against after execution). mark is the detectGen
// value its enqueue wrote, zero for a batch that wrote nothing.
type sourceBatch struct {
	source  pollSource
	key     subflux.PollKey
	since   time.Time
	entries []arrapi.HistoryRecord
	mark    uint64
}

type batchOutcome int

const (
	batchCompleted batchOutcome = iota
	batchCancelled
	// batchHeld: an entry's folder refuses writes; the batch stopped before
	// that entry, or before any entry when the batch's write test refused.
	batchHeld
)

// maxImportRetries is how many poll cycles a transiently-failing import
// (arr metadata fetch error, e.g. Sonarr restarting mid-poll) holds the
// watermark back before the poller gives up on it. Three retries at the
// default 30s interval rides out a typical arr restart; anything longer is
// left to the next scheduled full scan, which covers the item anyway.
const maxImportRetries = 3

// NewPoller creates a Poller with the given dependencies. Nothing is captured
// from the live state here: Run reads the PollInterval per cycle and the
// exclude-tag read goes to the arr-read wrapper, which owns its own TTL, so a
// hot-reloaded config takes effect on the next wake.
func NewPoller(deps Deps, stateFunc StateFunc) *Poller { //nolint:gocritic // hugeParam: callers pass by value
	return &Poller{
		deps:          deps,
		stateFunc:     stateFunc,
		importRetries: make(map[string]int),
		work:          make(chan sourceBatch, 8),
		detectHigh:    make(map[subflux.PollKey]time.Time),
		detectGen:     make(map[subflux.PollKey]uint64),
		behind:        make(map[subflux.PollKey]bool),
		heldOn:        make(map[subflux.PollKey]heldFolder),
		observed:      make(map[subflux.PollKey]time.Time),
	}
}

// Adaptive-poll burst window. When a poll cycle observes activity (any
// imported-history entries not seen before), subsequent cycles fire at burstPollInterval
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
// pollOnce reports activity, the next interval is shortened to
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
			p.deps.PollCache.retryDirty(ctx)
			if n := p.pollOnce(ctx); n > 0 {
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

// pollOnce checks both Sonarr and Radarr for new import events and enqueues
// what it finds for the executor; it performs NO import processing itself.
// Returns the number of imported-history entries not seen before across both
// arr clients (used by Run to decide whether to enter adaptive-burst mode).
func (p *Poller) pollOnce(ctx context.Context) int {
	start := time.Now()
	ls := p.stateFunc()

	var sonarrCount, radarrCount atomic.Int32

	g, gCtx := errgroup.WithContext(ctx)
	if ls.Sonarr != nil {
		if p.deps.PollCache.get(ctx, subflux.PollKeySonarr).IsZero() {
			p.deps.PollCache.set(ctx, subflux.PollKeySonarr, time.Now().UTC())
		}
		g.Go(func() error {
			sonarrCount.Store(int32(p.detectSonarr(gCtx, ls))) //nolint:gosec // G115: poll count fits int32
			return nil
		})
	}
	if ls.Radarr != nil {
		if p.deps.PollCache.get(ctx, subflux.PollKeyRadarr).IsZero() {
			p.deps.PollCache.set(ctx, subflux.PollKeyRadarr, time.Now().UTC())
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
func (p *Poller) detectSince(ctx context.Context, key subflux.PollKey) time.Time {
	since := p.deps.PollCache.get(ctx, key)
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
	var latest time.Time
	for i := range b.entries {
		if b.entries[i].Date.After(latest) {
			latest = b.entries[i].Date
		}
	}
	p.detectMu.Lock()
	defer p.detectMu.Unlock()
	b.mark = 0
	if !latest.IsZero() {
		b.mark = p.detectGen[b.key] + 1
	}
	select {
	case p.work <- *b:
		if b.mark != 0 {
			p.detectHigh[b.key] = latest.Add(time.Millisecond)
			p.detectGen[b.key] = b.mark
		}
	default:
		slog.Warn("poll: executor queue full, batch deferred to next cycle",
			"source", b.source, "entries", len(b.entries))
	}
}

// rewindDetection pulls the fetched-through cursor back to the durable
// watermark so the next detection re-fetches from it (the retry transport
// for transiently-failed entries and held batches, and the recovery path for
// dropped batches).
func (p *Poller) rewindDetection(ctx context.Context, key subflux.PollKey) {
	durable := p.deps.PollCache.get(ctx, key)
	p.detectMu.Lock()
	p.detectHigh[key] = durable
	p.detectGen[key]++
	p.detectMu.Unlock()
}

// rewindDetectionIf rewinds only while mark is still the newest write of the
// fetched-through cursor, so a discarded batch cannot erase the mark of a
// valid detection made after it.
func (p *Poller) rewindDetectionIf(ctx context.Context, key subflux.PollKey, mark uint64) {
	durable := p.deps.PollCache.get(ctx, key)
	p.detectMu.Lock()
	defer p.detectMu.Unlock()
	if mark == 0 || p.detectGen[key] != mark {
		return
	}
	p.detectHigh[key] = durable
	p.detectGen[key]++
}

func (p *Poller) isBehind(key subflux.PollKey) bool {
	p.detectMu.Lock()
	defer p.detectMu.Unlock()
	return p.behind[key]
}

// release ends a source's hold once a batch from its durable cursor completes.
func (p *Poller) release(key subflux.PollKey) {
	p.detectMu.Lock()
	defer p.detectMu.Unlock()
	p.behind[key] = false
	delete(p.heldOn, key)
}

// hold keeps a batch's entries for a later re-fetch: detection rewinds to
// the durable cursor and batches detected past it are discarded until a
// batch from it completes. folder is the folder that refused, "" for a
// transient arr failure.
func (p *Poller) hold(ctx context.Context, key subflux.PollKey, folder string) {
	p.rewindDetection(ctx, key)
	p.detectMu.Lock()
	defer p.detectMu.Unlock()
	p.behind[key] = true
	p.heldOn[key] = heldFolder{folder: folder, at: time.Now()}
}

// skipHeld reports the folder a held source waits on when detection should
// not fetch it: the media writer still marks the folder, or Presence the root,
// and the hold was decided less than a recheck interval ago. The bounded
// re-read is what lets a held entry that stopped asking for a subtitle there
// (an exclude tag, a rule with no subtitles, a deleted video) release the
// source.
func (p *Poller) skipHeld(key subflux.PollKey) (string, bool) {
	p.detectMu.Lock()
	h := p.heldOn[key]
	p.detectMu.Unlock()
	if h.folder == "" || time.Since(h.at) >= p.deps.Media.RecheckInterval() {
		return "", false
	}
	// Blocked takes a file path and starts its walk at that file's folder.
	held := filepath.Join(h.folder, "held")
	_, blocked := p.deps.Media.Blocked(held)
	if !blocked {
		_, blocked = p.deps.Presence.Unavailable(held)
	}
	return h.folder, blocked
}

// noteObserved records a fetch's entries and returns how many are newer than
// every entry the source returned before.
func (p *Poller) noteObserved(key subflux.PollKey, entries []arrapi.HistoryRecord) int {
	p.detectMu.Lock()
	defer p.detectMu.Unlock()
	seen := p.observed[key]
	n := 0
	for i := range entries {
		if entries[i].Date.After(seen) {
			n++
		}
	}
	if latest := latestDate(entries); latest.After(seen) {
		p.observed[key] = latest
	}
	return n
}

// runExecutor is the single worker draining detected batches: one batch at
// a time, so provider work never runs concurrently for poll-driven imports;
// same-item collisions with scans are handled by the engine's per-media gate.
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
// watermark advancement. The durable cursor never moves past an entry whose
// folder refuses writes, whether the batch's write test, the entry's or a save
// found it.
func (p *Poller) executeBatch(ctx context.Context, b *sourceBatch) {
	if p.isBehind(b.key) && b.since.After(p.deps.PollCache.get(ctx, b.key)) {
		slog.Debug("poll: batch detected past a held batch, discarded", "source", b.source)
		p.rewindDetectionIf(ctx, b.key, b.mark)
		return
	}
	ls := p.stateFunc()

	var resolver tagResolver
	var resolve resolveFunc
	switch b.source {
	case pollSourceSonarr:
		if ls.Sonarr == nil {
			p.rewindDetection(ctx, b.key)
			return
		}
		resolver = ls.Sonarr
		resolve = p.resolveSonarrImport
	case pollSourceRadarr:
		if ls.Radarr == nil {
			p.rewindDetection(ctx, b.key)
			return
		}
		resolver = ls.Radarr
		resolve = p.resolveRadarrImport
	default:
		return
	}

	searchCfg := ls.Cfg.Search()
	scanDelay := searchCfg.ScanDelay
	// One resolution per batch, straight to the arr client: the arr-read
	// wrapper coalesces concurrent readers and caches the result for its own
	// TTL, and unlike a private cache here it never caches a fail-open nil, so
	// a tag endpoint that was down for one cycle is retried on the next batch
	// instead of holding "no exclusions" for a poll interval.
	excludeIDs := resolver.ResolveExcludeTagIDs(ctx, searchCfg.ExcludeArrTags, false)

	run := p.runBatchEntries(ctx, ls, b, resolve, excludeIDs, scanDelay)
	switch run.outcome {
	case batchCancelled:
		// Leave the durable cursor untouched so a restart replays the whole
		// batch (at-least-once).
		return
	case batchHeld:
		// Entries ahead of the held one are done: the cursor moves up to the
		// held entry so a hold of any length re-runs none of them.
		p.advanceWatermark(ctx, b.key, b.since, run.latest, earliest(run.oldestFailed, run.heldFrom))
		p.hold(ctx, b.key, run.heldOn)
		return
	case batchCompleted:
	}

	p.advanceWatermark(ctx, b.key, b.since, run.latest, run.oldestFailed)
	if !run.oldestFailed.IsZero() {
		// A transiently-failed entry holds the durable watermark below
		// itself: re-fetch it next cycle (one attempt per poll cycle), and
		// keep a batch queued behind this one from advancing past it.
		p.hold(ctx, b.key, "")
		return
	}
	p.release(b.key)
}

// batchRun is how runBatchEntries ended: the newest entry date, the oldest
// transiently-failed entry, and for a held batch the earliest date among the
// entries that did not run and the folder that refused, when known.
type batchRun struct {
	latest       time.Time
	oldestFailed time.Time
	heldFrom     time.Time
	heldOn       string
	outcome      batchOutcome
}

type resolveFunc func(context.Context, *LiveState, *arrapi.HistoryRecord, map[int]struct{}) pendingImport

// batchEntry is one deduplicated entry of a batch, resolved before any entry
// searches; at is its index in the batch.
type batchEntry struct {
	pendingImport
	entry arrapi.HistoryRecord
	at    int
}

// runBatchEntries resolves a batch's deduplicated entries, write-tests the
// folders of every entry that asks for a subtitle, and only then runs them,
// so a refused folder anywhere holds the whole batch before any search. The
// pacing delay falls only before a searching entry that follows one that
// queried providers, so no dead sleep sits ahead of the watermark advance.
// A held entry stops the batch there with no retry counted against it, and a
// stop stops it with the cursor left for a restart to replay.
func (p *Poller) runBatchEntries(ctx context.Context, ls *LiveState, b *sourceBatch,
	resolve resolveFunc, excludeIDs map[int]struct{}, scanDelay time.Duration,
) batchRun {
	run := batchRun{latest: latestDate(b.entries), outcome: batchCancelled}
	pending := resolveBatch(ctx, ls, b, resolve, excludeIDs)
	if ctx.Err() != nil {
		return run
	}
	if err := p.preflightBatch(ctx, pending); err != nil {
		if ctx.Err() == nil {
			run.outcome = batchHeld
			run.heldFrom = earliestDate(b.entries)
			run.heldOn = refusedFolder(err)
		}
		return run
	}
	p.runResolved(ctx, ls, b, pending, scanDelay, &run)
	return run
}

// runResolved runs a batch's resolved entries in order and sets run's
// outcome, which stays batchCancelled when ctx ends first.
func (p *Poller) runResolved(ctx context.Context, ls *LiveState, b *sourceBatch,
	pending []batchEntry, scanDelay time.Duration, run *batchRun,
) {
	needPace := false
	for i := range pending {
		be := &pending[i]
		if be.result != nil && needPace {
			if err := httpx.SleepCtx(ctx, scanDelay); err != nil {
				return
			}
		}
		res := p.runImport(ctx, ls, &be.pendingImport)
		// Whatever an entry reports once the poller is stopping was decided
		// by the cancellation, not by the entry.
		if ctx.Err() != nil {
			return
		}
		if res.held {
			run.outcome = batchHeld
			run.heldFrom = earliestDate(b.entries[be.at:])
			run.heldOn = res.heldOn
			return
		}
		if be.result != nil {
			needPace = res.queried
		}
		p.trackImportOutcome(b.source, be.entry.ID, be.entry.Date, be.path, res.retryable, &run.oldestFailed)
	}
	run.outcome = batchCompleted
}

// resolveBatch resolves each entry with a path once, in batch order.
func resolveBatch(ctx context.Context, ls *LiveState, b *sourceBatch,
	resolve resolveFunc, excludeIDs map[int]struct{},
) []batchEntry {
	var out []batchEntry
	seen := make(map[string]bool)
	for i := range b.entries {
		entry := b.entries[i]
		path := entry.ImportedPath()
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		out = append(out, batchEntry{pendingImport: resolve(ctx, ls, &entry, excludeIDs), entry: entry, at: i})
		if ctx.Err() != nil {
			return nil
		}
	}
	return out
}

// preflightBatch write-tests the folder of every entry that asks for a
// subtitle and returns the first refusal, or ctx's error.
func (p *Poller) preflightBatch(ctx context.Context, pending []batchEntry) error {
	var folders []string
	for i := range pending {
		if pending[i].asksForSubtitles() {
			folders = append(folders, filepath.Dir(pending[i].path))
		}
	}
	if len(folders) == 0 {
		return nil
	}
	return p.deps.Media.Preflight(ctx, mediawrite.PreflightRequest{Folders: folders})
}

func latestDate(entries []arrapi.HistoryRecord) time.Time {
	var out time.Time
	for i := range entries {
		if entries[i].Date.After(out) {
			out = entries[i].Date
		}
	}
	return out
}

func earliestDate(entries []arrapi.HistoryRecord) time.Time {
	var out time.Time
	for i := range entries {
		out = earliest(out, entries[i].Date)
	}
	return out
}

func earliest(a, b time.Time) time.Time {
	if a.IsZero() || (!b.IsZero() && b.Before(a)) {
		return b
	}
	return a
}

func (p *Poller) detectSonarr(ctx context.Context, ls *LiveState) int {
	return p.detect(ctx, pollSourceSonarr, subflux.PollKeySonarr, ls.Sonarr.HistorySince)
}

func (p *Poller) detectRadarr(ctx context.Context, ls *LiveState) int {
	return p.detect(ctx, pollSourceRadarr, subflux.PollKeyRadarr, ls.Radarr.HistorySince)
}

type historyFunc func(ctx context.Context, since time.Time, eventTypes ...arrapi.EventType) ([]arrapi.HistoryRecord, error)

// detect fetches a source's import events and enqueues them for the
// executor. It returns how many of them it had not seen before, which drives
// adaptive-burst polling. A source held on a folder the media writer or
// Presence still marks is fetched at most once per the writer's recheck interval.
func (p *Poller) detect(ctx context.Context, source pollSource, key subflux.PollKey, history historyFunc) int {
	if folder, skip := p.skipHeld(key); skip {
		slog.Debug(string(source)+" poll: held on an unusable media folder, not fetched", "folder", folder)
		return 0
	}
	since := p.detectSince(ctx, key)
	entries, err := history(ctx, since, arrapi.EventDownloadImported)
	if err != nil {
		slog.Warn(string(source)+" poll failed", "since", since.UTC().Format(time.RFC3339), "error", err)
		return 0
	}
	if len(entries) == 0 {
		slog.Debug(string(source) + " poll: no new events")
		return 0
	}

	n := p.noteObserved(key, entries)
	if n > 0 {
		slog.Info(string(source)+" poll: new events", "count", n)
	} else {
		slog.Debug(string(source)+" poll: re-fetched events already seen", "count", len(entries))
	}
	p.enqueue(&sourceBatch{source: source, key: key, since: since, entries: entries})
	return n
}

// retryKey is the importRetries map key for one history entry.
//
// Neither component can carry the separator today — source is one of the two
// pollSource constants and entryID is an arr row id — so the key was already
// injective and keyenc.Join reproduces the fmt.Sprintf bytes exactly. It is
// adopted because the counter this key indexes gates the poll WATERMARK: two
// entries sharing a key share one attempt count, so the pair would be
// abandoned after maxImportRetries attempts between them instead of each, and
// clearing one entry's counter would release the watermark hold the other
// still needs — the import that was still failing gets polled past and is
// picked up only by the next full scan. Nothing about that consequence depends
// on today's field alphabets, so the key should not either.
func retryKey(source pollSource, entryID int) string {
	return keyenc.Join(string(source), strconv.Itoa(entryID))
}

// trackImportOutcome records the retry outcome for one processed history
// entry. A retryable failure notes it (advancing oldestFailed to the earliest
// failed entry date so the watermark holds there); success — or a failure
// that exhausted its retry budget inside noteImportFailure — clears the
// entry's retry counter so the watermark can move past it.
func (p *Poller) trackImportOutcome(source pollSource, entryID int, entryDate time.Time, path string, retryable bool, oldestFailed *time.Time) {
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
// just past the newest entry; given a bound (the oldest transiently-failed
// entry, or the first entry a held batch did not run) it stops at that bound
// so the next HistorySince re-fetches it. Entries after a transiently-failed
// one run again with it, for at most maxImportRetries cycles. The cursor never
// moves backward past `since`.
func (p *Poller) advanceWatermark(ctx context.Context, key subflux.PollKey, since, latest, bound time.Time) {
	if latest.IsZero() && bound.IsZero() {
		return
	}
	next := latest.Add(time.Millisecond)
	if !bound.IsZero() {
		next = bound
	}
	if !next.After(since) {
		return
	}
	p.deps.PollCache.set(ctx, key, next)
}
