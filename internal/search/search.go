package search

import (
	"cmp"
	"context"
	"errors"
	"log/slog"
	"time"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/logsafe"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/required"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/search/providerhealth"
	"github.com/cplieger/subflux/internal/search/scoring"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

// Metrics is the narrow observability interface consumed by the search
// engine; the concrete *obs.Metrics satisfies it via structural typing.
type Metrics interface {
	RecordSearch(provider subflux.ProviderID, dur time.Duration, err error)
	RecordDownload(provider subflux.ProviderID, err error)
	AdaptiveSkip()
	// RecordEmbeddedDetectorError counts a failed embedded track probe
	// (subflux_embedded_detector_errors_total). Context cancellation is
	// excluded by the caller.
	RecordEmbeddedDetectorError()
	// RecordSubtitleSaved counts a subtitle file written next to the media.
	RecordSubtitleSaved(provider subflux.ProviderID)
}

// MediaWriter writes subtitles into the media tree and knows which folders
// refuse writes; *mediawrite.Writer is the implementation.
type MediaWriter interface {
	// WriteFile returns an error matching mediawrite.ErrUnwritable when the
	// subtitle's folder refuses writes; any other error is about the target.
	WriteFile(ctx context.Context, path string, data []byte) error
	// Blocked reports the unwritable folder at or above path's folder.
	Blocked(path string) (folder string, blocked bool)
}

// SubtitleSyncer synchronizes subtitle timing and applies post-processing.
type SubtitleSyncer interface {
	// Sync adjusts subtitle timing against a reference (embedded, external, or audio).
	// Returns the (possibly modified) data and the applied offset in milliseconds
	// (0 if no sync was applied or confidence was too low).
	Sync(ctx context.Context, data []byte, videoPath, lang string) (synced []byte, offsetMs int64)

	// PostProcess applies encoding normalization, HI removal, tag stripping, etc.
	PostProcess(data []byte, pp subflux.PostProcessConfig) []byte
}

// syncSkipThreshold computes the minimum subtitle score at which timing sync
// is skipped. When both source family and release group match, the subtitle
// is from the same encode and timing sync would be unnecessary.
// The -1 accounts for partial source matches (e.g. WEB-DL vs WEB) which
// score Source-1. At this threshold the subtitle is close enough that sync
// could introduce drift rather than fix it.
func syncSkipThreshold(scores subflux.Scores) int {
	return scores.Source + scores.ReleaseGroup - 1
}

// Scorer turns a subtitle's release-attribute match set into a quality score.
// Both methods, because the engine uses both: it scores every candidate and
// labels the winner's tier. Declared here because this is the package that does
// the scoring work; the manual path takes only the labelling half at its own
// site.
type Scorer interface {
	// Score computes a quality score for a subtitle match set. Returns the
	// full score (including the hash bonus) and the release-attribute-only
	// score. A verifiable hash match short-circuits to the hash weight alone.
	Score(sub subflux.SubtitleInfo, matches subflux.MatchSet) (score, scoreNoHash int)
	// ScoreToTier maps a numeric score to a human-readable tier label via one
	// global threshold table: excellent >= 80, good >= 50, acceptable >= 20,
	// minimal >= 1, else none. Thresholds do not vary by media type.
	ScoreToTier(score int) subflux.ScoreTier
}

// Engine coordinates subtitle searches.
type Engine struct {
	store           Store
	cfg             Cfg
	metrics         Metrics
	scorer          Scorer
	syncer          SubtitleSyncer
	tracks          TrackDetector
	media           MediaWriter
	timeout         providerHealth
	providerGate    *providergate.Binding
	showCounter     provider.ShowSubtitleCounter
	showCounterID   subflux.ProviderID
	gate            *mediaGate
	syncExec        syncing.SyncExec
	searchGroup     singleflight.Group
	hashGroup       singleflight.Group
	providersByName map[subflux.ProviderID]provider.Provider
	providers       []provider.Provider
}

// Option configures the search Engine.
type Option func(*Engine)

// WithStore sets the engine's persistence backend.
func WithStore(s Store) Option { return func(e *Engine) { e.store = s } }

// WithConfig sets the engine's configuration source.
func WithConfig(c Cfg) Option { return func(e *Engine) { e.cfg = c } }

// WithMetrics sets the engine's metrics sink.
func WithMetrics(m Metrics) Option { return func(e *Engine) { e.metrics = m } }

// WithScorer sets the engine's release scorer.
func WithScorer(s Scorer) Option { return func(e *Engine) { e.scorer = s } }

// WithSyncer sets the engine's subtitle syncer.
func WithSyncer(s SubtitleSyncer) Option { return func(e *Engine) { e.syncer = s } }

// WithSyncExec sets the executor for the engine's own heavy sync calls (the
// audio fallback). Defaults to in-process; server mode installs the
// sync-worker client so alignment memory lives in a disposable child.
func WithSyncExec(x syncing.SyncExec) Option { return func(e *Engine) { e.syncExec = x } }

// WithTracks sets the engine's embedded-track detector.
func WithTracks(t TrackDetector) Option { return func(e *Engine) { e.tracks = t } }

// WithProviderGate sets the provider gate binding every provider call goes
// through. Required.
func WithProviderGate(b *providergate.Binding) Option { return func(e *Engine) { e.providerGate = b } }

// WithMediaWriter sets the writer every subtitle save goes through. Required.
func WithMediaWriter(w MediaWriter) Option { return func(e *Engine) { e.media = w } }

// WithTimeout sets the provider health tracker. When not set, the engine
// constructs one from config (or uses noopHealth if disabled).
func WithTimeout(h providerHealth) Option { return func(e *Engine) { e.timeout = h } }

// providerHealth is what the engine asks of a provider-health tracker, declared
// here because this is the package that consumes it and the package that holds
// the second implementation: providerhealth.Tracker counts real failures, and
// noopHealth below answers for a configuration with timeouts disabled.
type providerHealth interface {
	IsTimedOut(provider subflux.ProviderID) bool
	RecordSuccess(provider subflux.ProviderID)
	RecordFailure(provider subflux.ProviderID, err error)
	Status() map[subflux.ProviderID]subflux.ProviderStatus
	Reset()
	SetOnChange(fn providerhealth.OnChange)
}

type noopHealth struct{}

func (noopHealth) IsTimedOut(subflux.ProviderID) bool                    { return false }
func (noopHealth) RecordSuccess(subflux.ProviderID)                      {}
func (noopHealth) RecordFailure(subflux.ProviderID, error)               {}
func (noopHealth) Status() map[subflux.ProviderID]subflux.ProviderStatus { return nil }
func (noopHealth) Reset()                                                {}
func (noopHealth) SetOnChange(providerhealth.OnChange)                   {}

// New creates a search engine. The providers slice is required; all other
// dependencies are supplied via functional options.
func New(providers []provider.Provider, opts ...Option) *Engine {
	e := &Engine{providers: providers, gate: newMediaGate(), syncExec: syncing.InProcessExec{}}
	for _, o := range opts {
		o(e)
	}
	e.providersByName = make(map[subflux.ProviderID]provider.Provider, len(providers))
	for _, p := range providers {
		e.providersByName[p.Name()] = p
		if c, ok := p.(provider.ShowSubtitleCounter); ok && e.showCounter == nil {
			e.showCounter, e.showCounterID = c, p.Name()
		}
	}
	e.requireDeps()
	if e.timeout == nil {
		cooldown := e.cfg.Search().ProviderTimeout
		if cooldown > 0 {
			e.timeout = providerhealth.New(providerhealth.Config{
				Cooldown: cooldown,
			})
		}
	}
	if e.timeout == nil {
		e.timeout = noopHealth{}
	}
	return e
}

// requireDeps panics naming the first required option New was not given, or
// was given a nil pointer.
func (e *Engine) requireDeps() {
	for _, d := range []struct {
		v      any
		option string
	}{
		{v: e.store, option: "WithStore"},
		{v: e.cfg, option: "WithConfig"},
		{v: e.scorer, option: "WithScorer"},
		{v: e.syncer, option: "WithSyncer"},
		{v: e.tracks, option: "WithTracks (use embedded.Detector{} or search.NoopDetector{})"},
		{v: e.providerGate, option: "WithProviderGate"},
		{v: e.media, option: "WithMediaWriter"},
	} {
		if required.Missing(d.v) {
			panic("search.New: " + d.option + " is required")
		}
	}
}

// ScoreSubtitles filters results by identity and returns them scored against req.
func (e *Engine) ScoreSubtitles(req *subflux.SearchRequest, results []subflux.Subtitle) []subflux.ScoredResult {
	results, _ = scoring.FilterByIdentity(results, req)
	video := videoInfoFromRequest(req)
	scores := e.cfg.Scores()
	scored := scoreResults(e.scorer, &video, results, e.cfg.ProviderPriority)
	out := make([]subflux.ScoredResult, len(scored))
	for i := range scored {
		out[i] = subflux.ScoredResult{
			Sub:     scored[i].sub,
			Score:   scored[i].score,
			Matches: matchBreakdown(&scores, scored[i].matches),
		}
	}
	return out
}

// HashFile deduplicates concurrent calls for the same path via singleflight.
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

// SetProviderHealthHook installs the observer for provider timeout raise and
// clear transitions (a no-op when timeouts are disabled). The server installs
// the SSE publisher here after each activation — the tracker is rebuilt with
// the engine on a config reload, so the hook is re-installed with it.
func (e *Engine) SetProviderHealthHook(fn providerhealth.OnChange) {
	e.timeout.SetOnChange(fn)
}

// SimulateScore returns the score a subtitle release would get against a video release.
func (e *Engine) SimulateScore(mediaType subflux.MediaType, videoRelease, subRelease string, matchedBy subflux.MatchMethod) subflux.ScoreResult {
	video := videoInfoFromRequest(&subflux.SearchRequest{
		MediaType:   mediaType,
		ReleaseName: videoRelease,
	})
	matches := buildMatches(&video, &subflux.Subtitle{
		ReleaseName: subRelease,
		MatchedBy:   matchedBy,
	})
	score, scoreNoHash := e.scorer.Score(subflux.SubtitleInfo{
		HashVerifiable: matchedBy == subflux.MatchByHash,
	}, matches)
	return subflux.ScoreResult{
		Score:       score,
		ScoreNoHash: scoreNoHash,
		Tier:        e.scorer.ScoreToTier(score),
	}
}

func groupTargetsByLang(targets []subflux.SubtitleTarget) (groups map[string][]subflux.SubtitleTarget, order []string) {
	groups = make(map[string][]subflux.SubtitleTarget)
	for _, t := range targets {
		if _, ok := groups[t.Code]; !ok {
			order = append(order, t.Code)
		}
		groups[t.Code] = append(groups[t.Code], t)
	}
	return groups, order
}

// detectExistingObserved probes local subtitles and applies the engine's
// detector-error policy: a failed probe is WARN-logged with a bounded path
// attribute and counted in
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

// boundLogPath caps a path for use as a log attribute, keeping the tail (the
// identifying filename end) when truncation is needed. The cut advances to the
// next UTF-8 rune start, so a multi-byte rune is never split into invalid UTF-8
// in the log, and advancing FORWARD keeps the 256-byte cap a cap. It is local
// because runesafe.CapBytes, the shared rune-safe cut, keeps the HEAD and
// runesafe exports no tail variant. It bounds length only: a path already
// invalid UTF-8 on disk is not sanitized.
func boundLogPath(p string) string {
	if len(p) <= maxLogPathLen {
		return p
	}
	cut := len(p) - maxLogPathLen
	for cut < len(p) && !utf8.RuneStart(p[cut]) {
		cut++
	}
	return "..." + p[cut:]
}

// recordCoverageInventory records the subtitle files discovered on disk for
// coverage tracking, returning whether coverage changed. This is deliberately
// PRE-work state: the inventory describes what the visit observed on disk,
// which is correct however the search itself ends. The scanned_at stamp is
// the post-work half (stampScanState) — splitting the two is what keeps the
// resume stamp honest. No-op (returns false) for unidentified media.
func (e *Engine) recordCoverageInventory(ctx context.Context, mediaType subflux.MediaType,
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
func (e *Engine) stampScanState(ctx context.Context, mediaType subflux.MediaType,
	mediaID string, req *subflux.SearchRequest, searched bool,
) {
	if mediaID == "" {
		return
	}
	if err := e.store.RecordScanState(ctx, &subflux.ScanRecord{
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

// InventoryCoverage is the local-only half of the scan: it refreshes the
// on-disk/embedded subtitle inventory for a media item and
// stamps its scan state as inventoried-not-searched, with zero provider
// work. Scan skip paths (season early stop, show-level skip) call this so
// coverage badges stay truthful for items the scanner deliberately does not
// search: "skip" means skip PROVIDER work, not local bookkeeping.
func (e *Engine) InventoryCoverage(ctx context.Context, req *subflux.SearchRequest, videoPath string) bool {
	mediaType := req.MediaType
	mediaID := mediaid.Build(req)
	if mediaID == "" {
		return false
	}
	unlock := e.gate.lock(gateKey(mediaType, mediaID))
	defer unlock()

	req.VideoPath = videoPath
	existing, probeOK := e.detectExistingObserved(ctx, videoPath)
	var changed bool
	if probeOK {
		changed = e.recordCoverageInventory(ctx, mediaType, mediaID, existing)
	}
	if ctx.Err() == nil {
		e.stampScanState(ctx, mediaType, mediaID, req, false)
	}
	return changed
}

func gateKey(mediaType subflux.MediaType, mediaID string) string {
	return string(mediaType) + "\x00" + mediaID
}

// SearchTargets always searches for regular (non-HI, non-forced) subs, with
// HI as fallback. Its error is the context's, or the first
// *mediawrite.UnwritableError a save produced (also in WriteFailure), which
// is the caller's signal to stop.
func (e *Engine) SearchTargets(ctx context.Context, req *subflux.SearchRequest,
	videoPath string, targets []subflux.SubtitleTarget,
) (subflux.SearchResult, error) {
	slog.Debug("SearchTargets entry",
		"media", logsafe.Field(req.MediaLabel()), "media_type", req.MediaType,
		"imdb", req.ImdbID, "targets", len(targets),
		"video_path", videoPath)

	req.VideoPath = videoPath

	mediaType := req.MediaType
	mediaID := mediaid.Build(req)

	// Serializes work on the same media item across the scheduled scan, the
	// history poller, and manual scans. Unidentified media has no stable
	// identity to key on and skips the gate.
	if mediaID != "" {
		unlock := e.gate.lock(gateKey(mediaType, mediaID))
		defer unlock()
	}

	if req.VideoHash == "" && videoPath != "" {
		e.hashVideo(ctx, req, videoPath)
	}

	existing, probeOK := e.detectExistingObserved(ctx, videoPath)

	var result subflux.SearchResult

	if probeOK {
		result.CoverageChanged = e.recordCoverageInventory(ctx, mediaType, mediaID, existing)
	}

	searchCfg := e.cfg.Search()
	upgradeCutoff := time.Now().AddDate(0, 0, -searchCfg.UpgradeWindowDays)

	// Providers return all variants in one response, so grouping by language
	// avoids duplicate queries; variant filtering happens client-side.
	groups, langOrder := groupTargetsByLang(targets)

	if err := ctx.Err(); err != nil {
		return result, err
	}

	// singleflight deduplicates identical provider queries across languages
	// processed concurrently here.
	result.Langs = make([]subflux.LangOutcome, len(langOrder))
	writeFailures := make([]*mediawrite.UnwritableError, len(langOrder))

	g := new(errgroup.Group)
	g.SetLimit(4)
	for idx, lang := range langOrder {
		langTargets := groups[lang]
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return nil
			}
			result.Langs[idx], writeFailures[idx] = e.searchLangGroup(ctx, req, langTargets,
				videoPath, mediaType, mediaID, &existing, &searchCfg, upgradeCutoff)
			return nil
		})
	}
	_ = g.Wait()
	if wf := cmp.Or(writeFailures...); wf != nil {
		result.WriteFailure = wf
	}

	// A cancellation mid-item must not mark the item recently-scanned, or a
	// restart would resume-skip unfinished work for a full cycle.
	if ctx.Err() == nil && !unfinished(&result) {
		e.stampScanState(ctx, mediaType, mediaID, req, true)
	}

	if result.WriteFailure != nil {
		return result, result.WriteFailure
	}
	return result, nil
}

// hashVideo fills the request's hash and size from the video file; a file
// that cannot be hashed is searched without them.
func (e *Engine) hashVideo(ctx context.Context, req *subflux.SearchRequest, videoPath string) {
	hash, size, err := e.HashFile(ctx, videoPath)
	if err != nil {
		slog.Debug("video hash failed, searching without hash",
			"path", videoPath, "error", err)
		return
	}
	req.VideoHash = hash
	req.VideoSize = size
}

// unfinished reports whether a search left work the next pass must redo: a
// target had candidates and saved none, a searched language got no answer
// from any provider, or a target's folder refused writes. Such an item is
// not stamped, so the resume set does not skip it.
func unfinished(result *subflux.SearchResult) bool {
	for i := range result.Langs {
		l := &result.Langs[i]
		if l.Failed > 0 || l.WriteBlocked > 0 || (l.Kind == subflux.LangSearched && l.Answered == 0) {
			return true
		}
	}
	return false
}
