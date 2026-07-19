package scanning

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/showskip"
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
	seed    seedDeps
	seasons map[seasonKey]*seasonState
	mu      sync.Mutex
}

type seasonState struct {
	noResults int
	earlyStop bool
}

// BackoffPrefixReader is the narrow store surface earlyStop seeding needs:
// the season's existing adaptive-backoff rows by media-id prefix.
type BackoffPrefixReader interface {
	GetBackoffByPrefix(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.BackoffEntry, error)
}

// seedDeps carries what earlyStop seeding reads. The seed is recomputed from
// live store rows at each season's first recorded outcome — nothing new is
// persisted, so every existing freshness mechanism (ladder expiry, success
// clears, drift cleanup, reconcile, new episodes having no rows) deflates it
// automatically. A zero seedDeps (nil Backoff) disables seeding.
type seedDeps struct {
	Backoff     BackoffPrefixReader
	Enabled     map[api.ProviderID]struct{} // live enabled provider set
	MaxAttempts int                         // adaptive max attempts (0 = time-window only)
	Now         func() time.Time
}

func newSeasonTracker(counter api.ShowSubtitleCounter, cache *showskip.Cache, seed seedDeps) *seasonTracker {
	if seed.Now == nil {
		seed.Now = time.Now
	}
	return &seasonTracker{
		counter: counter,
		cache:   cache,
		seed:    seed,
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

func (st *seasonTracker) recordOutcome(ctx context.Context, imdbID string, season int, lang string, seasonIDPrefix string, outcome ScanOutcome, seasonEpCount int) {
	if imdbID == "" {
		return
	}
	key := seasonKey{ImdbID: imdbID, Season: season, Lang: lang}
	st.mu.Lock()
	defer st.mu.Unlock()
	s, ok := st.seasons[key]
	if !ok {
		// First outcome for this (season, lang): pre-fill the counter from
		// the store's existing active-backoff evidence, so a season already
		// saturated with recorded misses trips after its FIRST fresh
		// no-result instead of re-paying a full threshold of provider
		// queries every scan (the drain phase). Seeding fills the counter
		// only — earlyStop is never seeded as a fact, and a found result
		// still resets everything below.
		s = &seasonState{noResults: st.seedCount(ctx, key, seasonIDPrefix)}
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
	threshold := max(3, seasonEpCount*seasonEarlyStopPct/100)
	if s.noResults >= threshold {
		s.earlyStop = true
		slog.Info("season early termination: too many consecutive no-results",
			"imdb", imdbID, "season", season, "lang", lang,
			"no_results", s.noResults, "threshold", threshold,
			"season_episodes", seasonEpCount)
	}
}

// seedCount counts the season's episodes that are FULLY suppressed by
// adaptive backoff for this language: every currently-enabled provider has
// an active backoff row for the episode. Two binding constraints keep this
// safe: (1) an episode counts only when it has ZERO currently-eligible
// providers under the live provider set — a newly added provider has no rows
// anywhere, so its presence instantly zeroes every seed; (2) the value only
// pre-fills the counter — the caller's found-reset and threshold logic stay
// authoritative. Rows stop counting the moment they expire (NextRetry
// passes), a download succeeds (SaveDownload clears the triple), config
// drift clears them, or reconcile wipes the item, so the re-check cadence is
// bounded by the ladder's max delay. Callers hold st.mu.
func (st *seasonTracker) seedCount(ctx context.Context, key seasonKey, seasonIDPrefix string) int {
	if st.seed.Backoff == nil || seasonIDPrefix == "" || len(st.seed.Enabled) == 0 {
		return 0
	}
	entries, err := st.seed.Backoff.GetBackoffByPrefix(ctx, api.MediaTypeEpisode, seasonIDPrefix)
	if err != nil {
		slog.Warn("season tracker seed: backoff read failed, seeding 0",
			"imdb", key.ImdbID, "season", key.Season, "lang", key.Lang, "error", err)
		return 0
	}
	now := st.seed.Now()
	activeByEpisode := make(map[string]map[api.ProviderID]struct{})
	for i := range entries {
		en := &entries[i]
		if en.Language != key.Lang {
			continue
		}
		if _, enabled := st.seed.Enabled[en.Provider]; !enabled {
			continue
		}
		if !backoffActive(en, st.seed.MaxAttempts, now) {
			continue
		}
		m := activeByEpisode[en.MediaID]
		if m == nil {
			m = make(map[api.ProviderID]struct{})
			activeByEpisode[en.MediaID] = m
		}
		m[en.Provider] = struct{}{}
	}
	count := 0
	for _, provs := range activeByEpisode {
		if len(provs) == len(st.seed.Enabled) {
			count++
		}
	}
	if count > 0 {
		slog.Debug("season tracker seeded from active backoff",
			"imdb", key.ImdbID, "season", key.Season, "lang", key.Lang,
			"suppressed_episodes", count)
	}
	return count
}

// backoffActive reports whether a backoff row currently suppresses its
// provider: the failure count reached the adaptive max-attempts ceiling
// (permanent until cleared) or the retry window has not yet passed.
func backoffActive(en *api.BackoffEntry, maxAttempts int, now time.Time) bool {
	if maxAttempts > 0 && en.Failures >= maxAttempts {
		return true
	}
	return en.NextRetry.After(now)
}
