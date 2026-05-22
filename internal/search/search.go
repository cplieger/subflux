package search

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"subflux/internal/api"
	"subflux/internal/fsutil"
	"subflux/internal/search/scoring"
	"subflux/internal/search/timeout"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/singleflight"
)

// SearchMetrics is the narrow observability interface consumed by the search
// engine. Only the 3 methods actually called are required; the concrete
// *metrics.Metrics satisfies this via structural typing.
//
//nolint:revive // name is established API; renaming would break consumers
type SearchMetrics interface {
	RecordSearch(provider api.ProviderID, dur time.Duration, err error)
	RecordDownload(provider api.ProviderID, err error)
	AdaptiveSkip()
}

// FileWriter abstracts atomic file writes, decoupling the search engine from
// the concrete fsutil implementation. The default wraps fsutil.AtomicWriteFile;
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

// WithTracks sets the track detector.
func WithTracks(t TrackDetector) Option { return func(e *Engine) { e.tracks = t } }

// WithTimeout sets the provider health tracker. When not set, the engine
// constructs one from config (or uses noopHealth if disabled).
func WithTimeout(h timeout.ProviderHealth) Option { return func(e *Engine) { e.timeout = h } }

// WithFileWriter sets the file writer used for saving downloaded subtitles.
// When not set, the engine uses fsutil.AtomicWriteFile.
func WithFileWriter(w FileWriter) Option { return func(e *Engine) { e.fileWriter = w } }

// noopHealth is a no-op implementation used when timeouts are disabled.
type noopHealth struct{}

func (noopHealth) IsTimedOut(api.ProviderID) bool               { return false }
func (noopHealth) RecordSuccess(api.ProviderID)                 {}
func (noopHealth) RecordFailure(api.ProviderID, error)          {}
func (noopHealth) Status() map[api.ProviderID]api.TimeoutStatus { return nil }
func (noopHealth) Reset()                                       {}

// fsutilWriter is the default FileWriter that delegates to fsutil.AtomicWriteFile.
type fsutilWriter struct{}

func (fsutilWriter) WriteFile(ctx context.Context, path string, data []byte) error {
	return fsutil.AtomicWriteFile(ctx, path, data)
}

// New creates a search engine. The providers slice is required; all other
// dependencies are supplied via functional options.
func New(providers []api.Provider, opts ...Option) *Engine {
	e := &Engine{providers: providers}
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
		e.fileWriter = fsutilWriter{}
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
func (e *Engine) ProviderTimeouts() (map[api.ProviderID]api.TimeoutStatus, bool) {
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
	score, scoreNoHash := e.scorer.Score(&video, api.SubtitleInfo{
		HashVerifiable: matchedBy == api.MatchByHash,
	}, matches)
	return api.ScoreResult{
		Score:       score,
		ScoreNoHash: scoreNoHash,
		Tier:        e.scorer.ScoreToTier(score, mediaType),
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

// SearchTargets searches for subtitles using resolved SubtitleTargets.
// Always searches for regular (non-HI, non-forced) subs, with HI as fallback.
// Respects per-target provider filtering and min scores.
func (e *Engine) SearchTargets(ctx context.Context, req *api.SearchRequest,
	videoPath string, targets []api.SubtitleTarget) (api.SearchResult, error) {

	slog.Debug("SearchTargets entry",
		"media", req.MediaLabel(), "media_type", req.MediaType,
		"imdb", req.ImdbID, "targets", len(targets),
		"video_path", videoPath)

	// Store the video path on the request so providers (especially
	// embedded) can access the actual file for ffprobe.
	req.VideoPath = videoPath

	mediaType := req.MediaType
	mediaID := api.BuildMediaID(req)

	if req.VideoHash == "" && videoPath != "" {
		if hash, size, err := e.HashFile(ctx, videoPath); err == nil {
			req.VideoHash = hash
			req.VideoSize = size
		} else {
			slog.Debug("video hash failed, searching without hash",
				"path", videoPath, "error", err)
		}
	}

	existing := detectExisting(ctx, videoPath, e.tracks, IgnoredCodecsFromConfig(e.cfg))

	var result api.SearchResult

	// Record discovered subtitle files for coverage tracking.
	if mediaID != "" {
		files := existingToSubtitleFiles(existing)
		changed, err := e.store.RecordSubtitleFiles(ctx,
			mediaType, mediaID, files)
		if err != nil {
			slog.Warn("failed to record subtitle files",
				"media_id", mediaID, "error", err)
		}
		result.CoverageChanged = changed
		if err := e.store.RecordScanState(ctx, &api.ScanRecord{
			MediaType: mediaType,
			MediaID:   mediaID,
			Title:     req.Title,
			AudioLang: req.AudioLang,
			Season:    req.Season,
			Episode:   req.Episode,
		}); err != nil {
			slog.Warn("failed to record scan state",
				"media_id", mediaID, "error", err)
		}
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
	// identical provider queries across languages.
	type langResult struct {
		lang     string
		paths    []string
		searched int
		skipped  int
	}
	langResults := make([]langResult, len(langOrder))

	g := new(errgroup.Group)
	g.SetLimit(4) // Cap concurrent language groups.
	for idx, lang := range langOrder {
		langTargets := groups[lang]
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return nil
			}
			paths, searched, skipped := e.searchLangGroup(ctx, req, langTargets,
				videoPath, mediaType, mediaID, &existing, &searchCfg, upgradeCutoff)
			langResults[idx] = langResult{paths: paths, searched: searched, skipped: skipped, lang: lang}
			return nil
		})
	}
	_ = g.Wait()

	for _, lr := range langResults {
		result.Searched += lr.searched
		result.Skipped += lr.skipped
		result.Paths = append(result.Paths, lr.paths...)
		if len(lr.paths) > 0 {
			result.FoundLangs = append(result.FoundLangs, lr.lang)
		}
	}

	return result, nil
}
