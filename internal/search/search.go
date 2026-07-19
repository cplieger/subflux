package search

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search/scoring"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/search/timeout"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

// SearchMetrics is the narrow observability interface consumed by the search
// engine. Only the 4 methods actually called are required; the concrete
// *metrics.Metrics satisfies this via structural typing.
//
//nolint:revive // name is established API; renaming would break consumers
type SearchMetrics interface {
	RecordSearch(provider api.ProviderID, dur time.Duration, err error)
	RecordDownload(provider api.ProviderID, err error)
	AdaptiveSkip()
	// RecordEmbeddedDetectorError counts a failed embedded track probe
	// (subflux_embedded_detector_errors_total). Context cancellation is
	// excluded by the caller.
	RecordEmbeddedDetectorError()
}

// FileWriter abstracts atomic file writes, decoupling the search engine from
// the concrete atomicfile implementation. The default wraps atomicfile.WriteFile;
// tests can inject a stub that records writes without touching disk.
type FileWriter interface {
	WriteFile(ctx context.Context, path string, data []byte) error
}

// SubtitleSyncer synchronizes subtitle timing and applies post-processing.
type SubtitleSyncer interface {
	// Sync adjusts subtitle timing against a reference (embedded, external, or audio).
	// Returns the (possibly modified) data and the applied offset in milliseconds
	// (0 if no sync was applied or confidence was too low).
	Sync(ctx context.Context, data []byte, videoPath, lang string) (synced []byte, offsetMs int64)

	// PostProcess applies encoding normalization, HI removal, tag stripping, etc.
	PostProcess(data []byte, pp api.PostProcessConfig) []byte
}

// syncSkipThreshold computes the minimum subtitle score at which timing sync
// is skipped. When both source family and release group match, the subtitle
// is from the same encode and timing sync would be unnecessary.
// The -1 accounts for partial source matches (e.g. WEB-DL vs WEB) which
// score Source-1. At this threshold the subtitle is close enough that sync
// could introduce drift rather than fix it.
func syncSkipThreshold(scores api.Scores) int {
	return scores.Source + scores.ReleaseGroup - 1
}

// Compile-time check: *Engine implements api.SearchEngine.
var _ api.SearchEngine = (*Engine)(nil)

// Engine coordinates subtitle searches.
type Engine struct {
	store           SearchStore
	cfg             SearchCfg
	metrics         SearchMetrics
	scorer          api.Scorer
	syncer          SubtitleSyncer
	tracks          TrackDetector
	fileWriter      FileWriter
	timeout         timeout.ProviderHealth
	gate            *mediaGate
	syncExec        syncing.SyncExec
	searchGroup     singleflight.Group
	hashGroup       singleflight.Group
	providersByName map[api.ProviderID]api.Provider
	providers       []api.Provider
}

// Option configures the search Engine.
type Option func(*Engine)

// WithStore sets the search store.
func WithStore(s SearchStore) Option { return func(e *Engine) { e.store = s } }

// WithConfig sets the search configuration.
func WithConfig(c SearchCfg) Option { return func(e *Engine) { e.cfg = c } }

// WithMetrics sets the metrics recorder.
func WithMetrics(m SearchMetrics) Option { return func(e *Engine) { e.metrics = m } }

// WithScorer sets the subtitle scorer.
func WithScorer(s api.Scorer) Option { return func(e *Engine) { e.scorer = s } }

// WithSyncer sets the subtitle syncer.
func WithSyncer(s SubtitleSyncer) Option { return func(e *Engine) { e.syncer = s } }

// WithSyncExec sets the executor for the engine's own heavy sync calls (the
// audio fallback). Defaults to in-process; server mode installs the
// sync-worker client so alignment memory lives in a disposable child (P13).
func WithSyncExec(x syncing.SyncExec) Option { return func(e *Engine) { e.syncExec = x } }

// WithTracks sets the track detector.
func WithTracks(t TrackDetector) Option { return func(e *Engine) { e.tracks = t } }

// WithTimeout sets the provider health tracker. When not set, the engine
// constructs one from config (or uses noopHealth if disabled).
func WithTimeout(h timeout.ProviderHealth) Option { return func(e *Engine) { e.timeout = h } }

// noopHealth is a no-op implementation used when timeouts are disabled.
type noopHealth struct{}

func (noopHealth) IsTimedOut(api.ProviderID) bool                { return false }
func (noopHealth) RecordSuccess(api.ProviderID)                  {}
func (noopHealth) RecordFailure(api.ProviderID, error)           {}
func (noopHealth) Status() map[api.ProviderID]api.ProviderStatus { return nil }
func (noopHealth) Reset()                                        {}

// atomicWriter is the default FileWriter that delegates to atomicfile.WriteFile.
type atomicWriter struct{}

func (atomicWriter) WriteFile(ctx context.Context, path string, data []byte) error {
	_, err := atomicfile.WriteFile(ctx, path, data)
	return err
}

// New creates a search engine. The providers slice is required; all other
// dependencies are supplied via functional options.
func New(providers []api.Provider, opts ...Option) *Engine {
	e := &Engine{providers: providers, gate: newMediaGate(), syncExec: syncing.InProcessExec{}}
	for _, o := range opts {
		o(e)
	}
	// Build O(1) lookup map for downloadFromProvider.
	e.providersByName = make(map[api.ProviderID]api.Provider, len(providers))
	for _, p := range providers {
		e.providersByName[p.Name()] = p
	}
	if e.store == nil {
		panic("search.New: WithStore is required")
	}
	if e.cfg == nil {
		panic("search.New: WithConfig is required")
	}
	if e.scorer == nil {
		panic("search.New: WithScorer is required")
	}
	if e.syncer == nil {
		panic("search.New: WithSyncer is required")
	}
	if e.tracks == nil {
		panic("search.New: WithTracks is required (use embedded.Detector{} or search.NoopDetector{})")
	}
	if e.timeout == nil {
		cooldown := e.cfg.Search().ProviderTimeout
		if cooldown > 0 {
			e.timeout = timeout.New(timeout.Config{
				Cooldown: cooldown,
			})
		}
	}
	if e.timeout == nil {
		e.timeout = noopHealth{}
	}
	if e.fileWriter == nil {
		e.fileWriter = atomicWriter{}
	}
	return e
}

// ScoreSubtitles scores and ranks subtitles for a search request.
func (e *Engine) ScoreSubtitles(req *api.SearchRequest, results []api.Subtitle) []api.ScoredResult {
	results, _ = scoring.FilterByIdentity(results, req)
	video := videoInfoFromRequest(req)
	scores := e.cfg.Scores()
	scored := scoreResults(e.scorer, &video, results, e.cfg.ProviderPriority)
	out := make([]api.ScoredResult, len(scored))
	for i := range scored {
		out[i] = api.ScoredResult{
			Sub:     scored[i].sub,
			Score:   scored[i].score,
			Matches: matchBreakdown(&scores, scored[i].matches),
		}
	}
	return out
}

// HashFile computes the OpenSubtitles hash and file size for a video file.
// Concurrent calls for the same path are deduplicated via singleflight.
func (e *Engine) HashFile(ctx context.Context, path string) (hash string, size int64, err error) {
	type hashResult struct {
		hash string
		size int64
	}
	v, err, _ := e.hashGroup.Do(path, func() (any, error) {
		h, s, hErr := hashFile(ctx, path)
		if hErr != nil {
			return nil, hErr
		}
		return hashResult{h, s}, nil
	})
	if err != nil {
		return "", 0, err
	}
	r, ok := v.(hashResult)
	if !ok {
		return "", 0, errors.New("unexpected singleflight result type")
	}
	return r.hash, r.size, nil
}

// ProviderTimeouts returns a snapshot of all provider timeout states.
// Returns (status, true) if timeouts are enabled, (nil, false) otherwise.
func (e *Engine) ProviderTimeouts() (map[api.ProviderID]api.ProviderStatus, bool) {
	s := e.timeout.Status()
	if s == nil {
		return nil, false
	}
	return s, true
}

// ResetTimeouts clears all provider timeout state and re-enables all providers.
func (e *Engine) ResetTimeouts() {
	e.timeout.Reset()
}

// SimulateScore simulates scoring a subtitle against a video using release names.
func (e *Engine) SimulateScore(mediaType api.MediaType, videoRelease, subRelease string, matchedBy api.MatchMethod) api.ScoreResult {
	video := videoInfoFromRequest(&api.SearchRequest{
		MediaType:   mediaType,
		ReleaseName: videoRelease,
	})
	matches := buildMatches(&video, &api.Subtitle{
		ReleaseName: subRelease,
		MatchedBy:   matchedBy,
	})
	score, scoreNoHash := e.scorer.Score(api.SubtitleInfo{
		HashVerifiable: matchedBy == api.MatchByHash,
	}, matches)
	return api.ScoreResult{
		Score:       score,
		ScoreNoHash: scoreNoHash,
		Tier:        e.scorer.ScoreToTier(score),
	}
}

// groupTargetsByLang groups targets by language code, preserving insertion order.
func groupTargetsByLang(targets []api.SubtitleTarget) (groups map[string][]api.SubtitleTarget, order []string) {
	groups = make(map[string][]api.SubtitleTarget)
	for _, t := range targets {
		if _, ok := groups[t.Code]; !ok {
			order = append(order, t.Code)
		}
		groups[t.Code] = append(groups[t.Code], t)
	}
	return groups, order
}

// detectExistingObserved probes local subtitles and applies the engine's
// detector-error policy (embedded-detector separation, R2.3): a failed probe
// is WARN-logged with a bounded path attribute and counted in
// subflux_embedded_detector_errors_total — context cancellation is excluded
// from both — and probeOK=false tells the caller to SKIP the coverage
// replacement for this video: RecordSubtitleFiles is a full-set replacement,
// so recording the empty embedded portion of a failed probe would delete
// valid persisted rows. The search itself continues fail-open with the
// partial in-memory result (external subs are still scanned).
func (e *Engine) detectExistingObserved(ctx context.Context, videoPath string) (existing existingSubs, probeOK bool) {
	existing, err := detectExisting(ctx, videoPath, e.tracks, IgnoredCodecsFromConfig(e.cfg))
	if err == nil {
		return existing, true
	}
	if ctx.Err() == nil && !errors.Is(err, context.Canceled) {
		slog.Warn("embedded track detection failed; keeping last coverage snapshot",
			"path", boundLogPath(videoPath), "error", err)
		if e.metrics != nil {
			e.metrics.RecordEmbeddedDetectorError()
		}
	}
	return existing, false
}

// maxLogPathLen bounds the path attribute on detector-error log lines.
const maxLogPathLen = 256

// boundLogPath caps a path for use as a log attribute, keeping the tail
// (the identifying filename end) when truncation is needed.
func boundLogPath(p string) string {
	if len(p) <= maxLogPathLen {
		return p
	}
	return "..." + p[len(p)-maxLogPathLen:]
}

// recordCoverageInventory records the subtitle files discovered on disk for
// coverage tracking, returning whether coverage changed. This is deliberately
// PRE-work state: the inventory describes what the visit observed on disk,
// which is correct however the search itself ends. The scanned_at stamp is
// the post-work half (stampScanState) — splitting the two is what keeps the
// resume stamp honest (P5). No-op (returns false) for unidentified media.
func (e *Engine) recordCoverageInventory(ctx context.Context, mediaType api.MediaType,
	mediaID string, existing existingSubs,
) bool {
	if mediaID == "" {
		return false
	}
	files := existingToSubtitleFiles(existing)
	changed, err := e.store.RecordSubtitleFiles(ctx, mediaType, mediaID, files)
	if err != nil {
		slog.Warn("failed to record subtitle files",
			"media_id", mediaID, "error", err)
	}
	return changed
}

// stampScanState upserts the scan_state row for a media item. For searches
// this runs POST-work so the scanned_at stamp attests provider work that
// actually completed: a process exit mid-item leaves no fresh stamp, and the
// next scheduled scan revisits the item instead of resume-skipping unfinished
// work. searched=false records an inventory-only visit (scan skip paths that
// refreshed coverage without querying providers). No-op for unidentified
// media.
func (e *Engine) stampScanState(ctx context.Context, mediaType api.MediaType,
	mediaID string, req *api.SearchRequest, searched bool,
) {
	if mediaID == "" {
		return
	}
	if err := e.store.RecordScanState(ctx, &api.ScanRecord{
		MediaType: mediaType,
		MediaID:   mediaID,
		Title:     req.Title,
		AudioLang: req.AudioLang,
		Season:    req.Season,
		Episode:   req.Episode,
		Searched:  searched,
	}); err != nil {
		slog.Warn("failed to record scan state",
			"media_id", mediaID, "error", err)
	}
}

// InventoryCoverage implements the local-only half of api.SubtitleSearcher:
// it refreshes the on-disk/embedded subtitle inventory for a media item and
// stamps its scan state as inventoried-not-searched, with zero provider
// work. Scan skip paths (season early stop, show-level skip) call this so
// coverage badges stay truthful for items the scanner deliberately does not
// search: "skip" means skip PROVIDER work, not local bookkeeping.
func (e *Engine) InventoryCoverage(ctx context.Context, req *api.SearchRequest, videoPath string) bool {
	mediaType := req.MediaType
	mediaID := api.BuildMediaID(req)
	if mediaID == "" {
		return false
	}
	unlock := e.gate.lock(gateKey(mediaType, mediaID))
	defer unlock()

	req.VideoPath = videoPath
	existing, probeOK := e.detectExistingObserved(ctx, videoPath)
	var changed bool
	// A failed embedded probe skips the full-set coverage replacement so
	// the last complete snapshot survives (detectExistingObserved doc).
	if probeOK {
		changed = e.recordCoverageInventory(ctx, mediaType, mediaID, existing)
	}
	if ctx.Err() == nil {
		e.stampScanState(ctx, mediaType, mediaID, req, false)
	}
	return changed
}

// gateKey builds the mediaGate key for a media item.
func gateKey(mediaType api.MediaType, mediaID string) string {
	return string(mediaType) + "\x00" + mediaID
}

// SearchTargets searches for subtitles using resolved SubtitleTargets.
// Always searches for regular (non-HI, non-forced) subs, with HI as fallback.
// Respects per-target provider filtering and min scores.
func (e *Engine) SearchTargets(ctx context.Context, req *api.SearchRequest,
	videoPath string, targets []api.SubtitleTarget,
) (api.SearchResult, error) {
	slog.Debug("SearchTargets entry",
		"media", req.MediaLabel(), "media_type", req.MediaType,
		"imdb", req.ImdbID, "targets", len(targets),
		"video_path", videoPath)

	// Store the video path on the request so providers (especially
	// embedded) can access the actual file for ffprobe.
	req.VideoPath = videoPath

	mediaType := req.MediaType
	mediaID := api.BuildMediaID(req)

	// Serialize work on the same media item across the scheduled scan, the
	// history poller, and manual scans (P4). Unidentified media (empty
	// mediaID) has no stable identity to key on and skips the gate.
	if mediaID != "" {
		unlock := e.gate.lock(gateKey(mediaType, mediaID))
		defer unlock()
	}

	if req.VideoHash == "" && videoPath != "" {
		if hash, size, err := e.HashFile(ctx, videoPath); err == nil {
			req.VideoHash = hash
			req.VideoSize = size
		} else {
			slog.Debug("video hash failed, searching without hash",
				"path", videoPath, "error", err)
		}
	}

	existing, probeOK := e.detectExistingObserved(ctx, videoPath)

	var result api.SearchResult

	// Record the discovered subtitle files for coverage tracking (pre-work:
	// the inventory is valid however the search ends; see P5 split note on
	// recordCoverageInventory). A failed embedded probe skips the full-set
	// replacement so the last complete snapshot survives; the search itself
	// continues fail-open with the partial in-memory result.
	if probeOK {
		result.CoverageChanged = e.recordCoverageInventory(ctx, mediaType, mediaID, existing)
	}

	searchCfg := e.cfg.Search()
	upgradeCutoff := time.Now().AddDate(0, 0, -searchCfg.UpgradeWindowDays)

	// Group targets by language code to avoid duplicate provider queries.
	// Providers return all variants (standard, forced, HI) in one response;
	// variant filtering is client-side.
	groups, langOrder := groupTargetsByLang(targets)

	// Check context before starting concurrent work.
	if err := ctx.Err(); err != nil {
		return result, err
	}

	// Process language groups concurrently. Each group already uses errgroup
	// internally for provider concurrency, and singleflight deduplicates
	// identical provider queries across languages. Each group produces one
	// typed api.LangOutcome; the tracker and stats consume those directly.
	result.Langs = make([]api.LangOutcome, len(langOrder))

	g := new(errgroup.Group)
	g.SetLimit(4) // Cap concurrent language groups.
	for idx, lang := range langOrder {
		langTargets := groups[lang]
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return nil
			}
			result.Langs[idx] = e.searchLangGroup(ctx, req, langTargets,
				videoPath, mediaType, mediaID, &existing, &searchCfg, upgradeCutoff)
			return nil
		})
	}
	_ = g.Wait()

	// Stamp scanned_at post-work, and only when the work ran to completion:
	// a cancellation mid-item must not mark the item recently-scanned, or a
	// restart would resume-skip unfinished work for a full cycle (P5).
	if ctx.Err() == nil {
		e.stampScanState(ctx, mediaType, mediaID, req, true)
	}

	return result, nil
}
