package scanning

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"golang.org/x/sync/errgroup"
)

// RunFullScan iterates all wanted episodes and movies, searching for missing
// subtitles. Episodes and movies are sorted alphabetically by title.
func RunFullScan(ctx context.Context, deps *Deps, ls *LiveState) {
	const action = "Full Scan"
	const detail = "Searching library for missing subtitles"
	source := activity.SourceScheduled
	actID := deps.Activity.Start(action, detail, source)
	deps.Events.PublishScanStart(action, detail, source)
	defer func() {
		deps.Activity.End(actID)
		deps.Events.PublishScanDone(action, detail, source, true)
	}()
	start := time.Now()

	var stats api.ScanStats
	searchCfg := ls.Cfg.Search()
	scanDelay := searchCfg.ScanDelay

	slog.Info("full scan starting", "scan_delay", scanDelay.String())

	// Resolve exclude tag names to IDs.
	var sonarrExclude, radarrExclude map[int]struct{}
	if len(searchCfg.ExcludeArrTags) > 0 {
		if ls.Sonarr != nil {
			sonarrExclude = ls.Sonarr.ResolveExcludeTagIDs(ctx, searchCfg.ExcludeArrTags, true)
		}
		if ls.Radarr != nil {
			radarrExclude = ls.Radarr.ResolveExcludeTagIDs(ctx, searchCfg.ExcludeArrTags, true)
		}
	}

	// Collect episodes and movies concurrently.
	var episodes, movies []ScanItem
	g, gctx := errgroup.WithContext(ctx)
	if ls.Sonarr != nil {
		g.Go(func() error {
			episodes = collectEpisodes(gctx, ls, deps.Alerts, sonarrExclude)
			return nil
		})
	}
	if ls.Radarr != nil {
		g.Go(func() error {
			movies = collectMovies(gctx, ls, deps.Alerts, radarrExclude)
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		slog.Error("fetch media failed", "error", err)
		return
	}

	queue := SortByTitle(episodes, movies)
	slog.Info("scan queue built",
		"episodes", len(episodes), "movies", len(movies),
		"total", len(queue))

	// Resume support: skip items already scanned within this scan interval.
	scanInterval := searchCfg.ScanInterval
	recentlyScanned := loadRecentScans(ctx, deps.DB, scanInterval)

	resumed := processItems(ctx, deps, ls, queue, recentlyScanned, &stats, actID, scanDelay)

	dur := time.Since(start).Round(time.Second)
	totalFound := stats.EpisodesFound + stats.MoviesFound

	if ctx.Err() != nil {
		slog.Warn("full scan interrupted by shutdown",
			"episodes_searched", stats.EpisodesSearched,
			"movies_searched", stats.MoviesSearched,
			"duration", dur.String())
		return
	}

	slog.Info("full scan complete",
		"episodes", stats.EpisodesSearched, "movies", stats.MoviesSearched,
		"found", totalFound, "resumed", resumed,
		"duration", dur.String())
	deps.Alerts.RecordInfo(
		fmt.Sprintf("Scan complete: %d found, %d searched in %s",
			totalFound,
			stats.EpisodesSearched+stats.MoviesSearched,
			dur.String()))
	slog.Info("scan results: episodes",
		"searched", stats.EpisodesSearched, "found", stats.EpisodesFound,
		"skipped", stats.EpisodesSkipped, "no_result", stats.EpisodesNoResult,
		"series_skipped", stats.SeriesSkipped)
	slog.Info("scan results: movies",
		"searched", stats.MoviesSearched, "found", stats.MoviesFound,
		"skipped", stats.MoviesSkipped, "no_result", stats.MoviesNoResult)
	deps.Metrics.RecordScan(
		stats.EpisodesSearched+stats.MoviesSearched,
		totalFound, time.Since(start))

	// Clear provider download caches to free memory.
	deps.ClearCaches(ls.Providers)
}

// processItems iterates the sorted scan queue, processing each item.
// Returns the number of items skipped due to recent scanning.
func processItems(ctx context.Context, deps *Deps, ls *LiveState,
	queue []ScanItem, recentlyScanned map[string]bool,
	stats *api.ScanStats, actID string, scanDelay time.Duration,
) int {
	tracker := newSeasonTracker(ls.ShowCounter, deps.ShowSkipCache)
	langs := ls.Cfg.LanguageCodes()
	skippedSeries := make(map[string]struct{})
	resumed := 0

	for _, item := range queue {
		if ctx.Err() != nil {
			break
		}

		if SkipResumed(item, recentlyScanned, stats) {
			resumed++
			continue
		}

		if item.Ep != nil {
			scanFullEpisode(ctx, deps, ls, item.Series, item.Ep,
				tracker, langs, skippedSeries, stats, actID)
		} else {
			scanFullMovie(ctx, deps, ls, item.Movie, stats, actID)
		}
		if err := deps.SleepCtx(ctx, scanDelay); err != nil {
			break
		}
	}
	return resumed
}

func scanFullEpisode(ctx context.Context, deps *Deps, ls *LiveState,
	series *arrapi.Series, ep *arrapi.Episode,
	tracker *seasonTracker, langs []string,
	skippedSeries map[string]struct{}, stats *api.ScanStats, actID string,
) {
	epCount := 0
	if series.Statistics != nil {
		epCount = series.Statistics.EpisodeFileCount
	}
	if tracker.shouldSkipShow(ctx, series.ImdbID, epCount, langs) {
		if _, seen := skippedSeries[series.ImdbID]; !seen {
			skippedSeries[series.ImdbID] = struct{}{}
			stats.SeriesSkipped++
		}
		stats.EpisodesSkipped++
		stats.EpisodesSearched++
		return
	}

	if tracker.shouldSkipEpisode(series.ImdbID, ep.SeasonNumber, langs) {
		stats.EpisodesSkipped++
		stats.EpisodesSearched++
		return
	}

	outcome, foundLangs := ScanEpisode(ctx, deps, ls, series, ep)

	seasonEpCount := api.SeasonEpisodeFileCount(series, ep.SeasonNumber)
	recordEpisodeOutcomes(tracker, series.ImdbID, ep.SeasonNumber,
		langs, foundLangs, outcome, seasonEpCount)

	switch outcome {
	case ScanFound:
		stats.EpisodesFound++
	case ScanSkipped:
		stats.EpisodesSkipped++
	default:
		stats.EpisodesNoResult++
	}
	stats.EpisodesSearched++
	total := stats.EpisodesSearched + stats.MoviesSearched
	deps.Activity.Progress(actID, total, 0,
		fmt.Sprintf("%d episodes, %d movies",
			stats.EpisodesSearched, stats.MoviesSearched))
}

// recordEpisodeOutcomes records the per-language scan result for an episode's
// season. When foundLangs is non-empty, each target language is recorded as
// ScanFound when a subtitle was downloaded for it and ScanNoResult otherwise;
// when no language had a subtitle, the raw scan outcome is recorded for every
// target language.
func recordEpisodeOutcomes(tracker *seasonTracker, imdbID string, season int,
	langs, foundLangs []string, outcome ScanOutcome, seasonEpCount int,
) {
	if len(foundLangs) == 0 {
		for _, lang := range langs {
			tracker.recordOutcome(imdbID, season, lang, outcome, seasonEpCount)
		}
		return
	}
	foundSet := make(map[string]struct{}, len(foundLangs))
	for _, l := range foundLangs {
		foundSet[l] = struct{}{}
	}
	for _, lang := range langs {
		if _, ok := foundSet[lang]; ok {
			tracker.recordOutcome(imdbID, season, lang, ScanFound, seasonEpCount)
		} else {
			tracker.recordOutcome(imdbID, season, lang, ScanNoResult, seasonEpCount)
		}
	}
}

func scanFullMovie(ctx context.Context, deps *Deps, ls *LiveState,
	m *arrapi.Movie, stats *api.ScanStats, actID string,
) {
	outcome := ScanMovie(ctx, deps, ls, m)
	switch outcome {
	case ScanFound:
		stats.MoviesFound++
	case ScanSkipped:
		stats.MoviesSkipped++
	default:
		stats.MoviesNoResult++
	}
	stats.MoviesSearched++
	total := stats.EpisodesSearched + stats.MoviesSearched
	deps.Activity.Progress(actID, total, 0,
		fmt.Sprintf("%d episodes, %d movies",
			stats.EpisodesSearched, stats.MoviesSearched))
}

func loadRecentScans(ctx context.Context, db ScanStore, scanInterval time.Duration) map[string]bool {
	cutoff := time.Now().Add(-scanInterval)
	recent, err := db.RecentlyScanned(ctx, cutoff)
	if err != nil {
		slog.Warn("failed to load recent scan state, scanning all", "error", err)
		return nil
	}
	if len(recent) > 0 {
		slog.Info("scan resume: skipping recently scanned items",
			"recent", len(recent),
			"cutoff", cutoff.UTC().Format(time.RFC3339))
	}
	return recent
}
