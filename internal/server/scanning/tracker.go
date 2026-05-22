package scanning

import (
	"context"
	"log/slog"
	"sync"

	"subflux/internal/api"
	"subflux/internal/server/showskip"

	"golang.org/x/sync/errgroup"
)

// showSkipThresholdPct is the percentage of episodes that must have
// subtitles on OpenSubtitles for the show-level pre-check to pass.
const showSkipThresholdPct = 20

// seasonEarlyStopPct is the percentage of a season's episodes that
// must return no results before remaining episodes are skipped.
const seasonEarlyStopPct = 20

// seasonKey is a typed composite key for the season tracker map,
// eliminating string concatenation allocations in the hot scan loop.
type seasonKey struct {
	ImdbID string
	Lang   string
	Season int
}

type seasonTracker struct {
	counter api.ShowSubtitleCounter
	cache   *showskip.Cache
	seasons map[seasonKey]*seasonState
	mu      sync.Mutex
}

type seasonState struct {
	noResults int
	earlyStop bool
}

func newSeasonTracker(counter api.ShowSubtitleCounter, cache *showskip.Cache) *seasonTracker {
	return &seasonTracker{
		counter: counter,
		cache:   cache,
		seasons: make(map[seasonKey]*seasonState),
	}
}

func (st *seasonTracker) shouldSkipShow(ctx context.Context, imdbID string, episodeCount int, langs []string) bool {
	if st.counter == nil || imdbID == "" || episodeCount <= 0 || len(langs) == 0 {
		return false
	}
	if len(langs) == 1 {
		return st.showLevelSkip(ctx, imdbID, episodeCount, langs[0])
	}

	// Check languages concurrently. Cancel on first non-skip (any language
	// passing means the show should NOT be skipped).
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(2) // respect rate limits; VIP users benefit from overlap

	type langResult struct {
		skip bool
	}
	results := make([]langResult, len(langs))

	for i, lang := range langs {
		g.Go(func() error {
			results[i] = langResult{skip: st.showLevelSkip(gctx, imdbID, episodeCount, lang)}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		slog.Warn("show skip check error", "error", err)
	}

	for _, r := range results {
		if !r.skip {
			return false
		}
	}
	return true
}

func (st *seasonTracker) showLevelSkip(ctx context.Context, imdbID string, episodeCount int, lang string) bool {
	key := imdbID + "-" + lang
	if skip, ok := st.cache.Get(key); ok {
		slog.Debug("show skip cache hit", "imdb", imdbID, "lang", lang, "skip", skip)
		return skip
	}
	count, err := st.counter.CountShowSubtitles(ctx, imdbID, lang)
	if err != nil {
		slog.Warn("show subtitle count failed, not skipping",
			"imdb", imdbID, "lang", lang, "error", err)
		st.cache.Set(key, false)
		return false
	}
	threshold := episodeCount * showSkipThresholdPct / 100
	skip := count <= threshold
	if skip {
		slog.Info("skipping series: too few subtitles on OpenSubtitles",
			"imdb", imdbID, "lang", lang,
			"subs_available", count, "episodes", episodeCount,
			"threshold_pct", showSkipThresholdPct)
	} else {
		slog.Debug("show pre-check passed",
			"imdb", imdbID, "lang", lang,
			"subs_available", count, "threshold", threshold)
	}
	st.cache.Set(key, skip)
	return skip
}

func (st *seasonTracker) shouldSkipSeason(imdbID string, season int, lang string) bool {
	key := seasonKey{ImdbID: imdbID, Season: season, Lang: lang}
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.seasons[key]
	if ok && s.earlyStop {
		slog.Debug("season early termination active",
			"imdb", imdbID, "season", season, "lang", lang)
		return true
	}
	return false
}

func (st *seasonTracker) shouldSkipEpisode(imdbID string, season int, langs []string) bool {
	if imdbID == "" || len(langs) == 0 {
		return false
	}
	for _, lang := range langs {
		if !st.shouldSkipSeason(imdbID, season, lang) {
			return false
		}
	}
	return true
}

func (st *seasonTracker) recordOutcome(imdbID string, season int, lang string, outcome ScanOutcome, seasonEpCount int) {
	if imdbID == "" {
		return
	}
	key := seasonKey{ImdbID: imdbID, Season: season, Lang: lang}
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.seasons[key]
	if !ok {
		s = &seasonState{}
		st.seasons[key] = s
	}
	switch outcome {
	case ScanNoResult:
		s.noResults++
	case ScanFound:
		s.noResults = 0
		return
	default:
		return
	}
	if s.earlyStop {
		return
	}
	threshold := 3
	if seasonEpCount > 0 {
		pct := seasonEpCount * seasonEarlyStopPct / 100
		if pct > threshold {
			threshold = pct
		}
	}
	if s.noResults >= threshold {
		s.earlyStop = true
		slog.Info("season early termination: too many consecutive no-results",
			"imdb", imdbID, "season", season, "lang", lang,
			"no_results", s.noResults, "threshold", threshold,
			"season_episodes", seasonEpCount)
	}
}
