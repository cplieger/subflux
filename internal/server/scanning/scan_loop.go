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
//
// The activity entry is started by the CALLER (HTTP handler or scheduler) at
// the accept boundary — actID identifies it; the returned outcome is applied
// by the caller via FinishScanActivity. The stop channel is the graceful
// cancel signal, checked between items only: the context stays server-derived
// and reaches the current item's provider calls, so cancelling it is a hard
// kill reserved for process shutdown.
func RunFullScan(ctx context.Context, stop <-chan struct{}, deps *Deps, ls *LiveState, actID string) activity.Outcome {
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
		return activity.OutcomeFailed
	}

	queue := SortByTitle(episodes, movies)
	slog.Info("scan queue built",
		"episodes", len(episodes), "movies", len(movies),
		"total", len(queue))

	// Resume support: skip items already scanned within this scan interval.
	// The persisted cycle mark makes the cutoff duration-aware: a pass longer
	// than scan_interval keeps its early segment in the resume set after a
	// restart (the mark survives until normal completion clears it).
	scanInterval := searchCfg.ScanInterval
	cycleStart := resumeCycleStart(ctx, deps.DB)
	recentlyScanned := loadRecentScans(ctx, deps.DB, scanInterval, cycleStart)

	resumed, loopOutcome := processItems(ctx, stop, deps, ls, queue, recentlyScanned, &stats, actID, scanDelay)

	dur := time.Since(start).Round(time.Second)
	totalFound := stats.EpisodesFound + stats.MoviesFound

	if ctx.Err() != nil {
		slog.Warn("full scan interrupted by shutdown",
			"episodes_searched", stats.EpisodesSearched,
			"movies_searched", stats.MoviesSearched,
			"duration", dur.String())
		return activity.OutcomeShutdown
	}
	if loopOutcome == activity.OutcomeCancelled {
		// A user-stopped scan is an interrupted cycle: the cycle mark stays
		// dangling so the next pass resumes past the already-scanned items.
		slog.Info("full scan stopped by user",
			"episodes_searched", stats.EpisodesSearched,
			"movies_searched", stats.MoviesSearched,
			"found", totalFound,
			"duration", dur.String())
		return activity.OutcomeCancelled
	}

	// Normal completion closes the resume window: clear the cycle mark so
	// the next scan applies the plain interval cutoff. An interrupted scan
	// leaves the mark dangling, which is exactly the resume signal.
	if err := deps.DB.ClearScanCycleStart(ctx); err != nil {
		slog.Warn("failed to clear scan cycle mark", "error", err)
	}

	slog.Info("full scan complete",
		"episodes", stats.EpisodesSearched, "movies", stats.MoviesSearched,
		"found", totalFound, "resumed", resumed,
		"duration", dur.String())
	summary := fmt.Sprintf("Scan complete: %d found, %d searched in %s",
		totalFound,
		stats.EpisodesSearched+stats.MoviesSearched,
		dur.String())
	if backedOff := stats.EpisodesBackedOff + stats.MoviesBackedOff; backedOff > 0 {
		summary += fmt.Sprintf(" (%d backed off)", backedOff)
	}
	deps.Alerts.RecordInfo(summary)
	slog.Info("scan results: episodes",
		"searched", stats.EpisodesSearched, "found", stats.EpisodesFound,
		"skipped", stats.EpisodesSkipped, "no_result", stats.EpisodesNoResult,
		"backed_off", stats.EpisodesBackedOff,
		"series_skipped", stats.SeriesSkipped)
	slog.Info("scan results: movies",
		"searched", stats.MoviesSearched, "found", stats.MoviesFound,
		"skipped", stats.MoviesSkipped, "no_result", stats.MoviesNoResult,
		"backed_off", stats.MoviesBackedOff)
	deps.Metrics.RecordScan(
		stats.EpisodesSearched+stats.MoviesSearched,
		totalFound, time.Since(start))

	// Clear provider download caches to free memory.
	deps.ClearCaches(ls.Providers)
	return activity.OutcomeCompleted
}

// processItems iterates the sorted scan queue, processing each item. The
// stop signal is honoured between items (and during the inter-item delay);
// the item in flight always completes. Returns the number of items skipped
// due to recent scanning and the loop outcome ("" for a full pass,
// cancelled/shutdown when ended early).
func processItems(ctx context.Context, stop <-chan struct{}, deps *Deps, ls *LiveState,
	queue []ScanItem, recentlyScanned map[string]bool,
	stats *api.ScanStats, actID string, scanDelay time.Duration,
) (resumed int, outcome activity.Outcome) {
	tracker := newSeasonTracker(ls.ShowCounter, deps.ShowSkipCache, buildSeedDeps(deps, ls))
	langs := ls.Cfg.LanguageCodes()
	skippedSeries := make(map[string]struct{})

	for _, item := range queue {
		if err := ctx.Err(); err != nil {
			return resumed, activity.OutcomeShutdown
		}
		if stopRequested(stop) {
			return resumed, activity.OutcomeCancelled
		}

		if SkipResumed(item, recentlyScanned, stats) {
			resumed++
			continue
		}

		if !scanQueueItem(ctx, deps, ls, item, tracker, langs, skippedSeries, stats, actID) {
			// The item generated no provider traffic (tracker skip, all
			// targets covered on disk, manually locked, in adaptive
			// backoff, or every eligible provider health-timed-out): the
			// inter-item delay exists to pace providers, so it is skipped
			// the same way resume skips are.
			continue
		}
		if o := waitOrStop(ctx, stop, scanDelay); o != "" {
			return resumed, o
		}
	}
	// The final item has no next-iteration boundary check: shutdown FIRST
	// (never reported as a user cancellation), stop SECOND, so a stop
	// landing during the last in-flight item terminates the scan as
	// cancelled rather than publishing a false success.
	if ctx.Err() != nil {
		return resumed, activity.OutcomeShutdown
	}
	if stopRequested(stop) {
		return resumed, activity.OutcomeCancelled
	}
	return resumed, ""
}

// scanQueueItem processes one scan-queue item (episode or movie) and reports
// whether it actually queried any provider — the caller keys the inter-item
// pacing delay on that.
func scanQueueItem(ctx context.Context, deps *Deps, ls *LiveState, item ScanItem,
	tracker *seasonTracker, langs []string,
	skippedSeries map[string]struct{}, stats *api.ScanStats, actID string,
) (queried bool) {
	if item.Ep == nil {
		return scanFullMovie(ctx, deps, ls, item.Movie, stats, actID)
	}
	trackerSkipped, queried := scanFullEpisode(ctx, deps, ls,
		item.Series, item.Ep, tracker, langs, skippedSeries, stats, actID)
	if trackerSkipped {
		// Tracker skip: zero provider work was done.
		return false
	}
	return queried
}

// scanFullEpisode scans one episode within the full-scan loop. It reports
// whether the season tracker skipped the episode outright (show-level or
// season-level skip) and, when it did run, whether any provider was actually
// queried: both feed the caller's decision to bypass the inter-item scan
// delay for items that generated no provider traffic.
func scanFullEpisode(ctx context.Context, deps *Deps, ls *LiveState,
	series *arrapi.Series, ep *arrapi.Episode,
	tracker *seasonTracker, langs []string,
	skippedSeries map[string]struct{}, stats *api.ScanStats, actID string,
) (trackerSkipped, queried bool) {
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
		inventorySkipped(ctx, deps, ls, series, ep)
		return true, false
	}

	if tracker.shouldSkipEpisode(series.ImdbID, ep.SeasonNumber, langs) {
		stats.EpisodesSkipped++
		stats.EpisodesSearched++
		inventorySkipped(ctx, deps, ls, series, ep)
		return true, false
	}

	outcome, langOutcomes, queried := ScanEpisode(ctx, deps, ls, series, ep)

	seasonEpCount := api.SeasonEpisodeFileCount(series, ep.SeasonNumber)
	recordEpisodeOutcomes(ctx, tracker, series, ep.SeasonNumber,
		langOutcomes, seasonEpCount)

	switch outcome {
	case ScanFound:
		stats.EpisodesFound++
	case ScanSkipped:
		stats.EpisodesSkipped++
	case ScanBackedOff:
		stats.EpisodesBackedOff++
	default:
		stats.EpisodesNoResult++
	}
	stats.EpisodesSearched++
	total := stats.EpisodesSearched + stats.MoviesSearched
	deps.Activity.Progress(actID, total, 0,
		fmt.Sprintf("%d episodes, %d movies",
			stats.EpisodesSearched, stats.MoviesSearched))
	return false, queried
}

// recordEpisodeOutcomes records the per-language scan result for an episode's
// season, over ONLY the languages whose group actually ran (Kind ==
// api.LangSearched). A language skipped for this episode — covered on disk,
// manually locked, or not a target at all — or fully backed off is not
// recorded, so it can never accrue a false no-result streak that
// early-terminates the season for episodes that genuinely need it.
func recordEpisodeOutcomes(ctx context.Context, tracker *seasonTracker,
	series *arrapi.Series, season int,
	outcomes []api.LangOutcome, seasonEpCount int,
) {
	seasonIDPrefix := api.BuildSeasonIDPrefix(series.TvdbID, series.ImdbID, season)
	for i := range outcomes {
		o := &outcomes[i]
		if o.Kind != api.LangSearched {
			continue
		}
		kind := ScanNoResult
		if o.Found() {
			kind = ScanFound
		}
		tracker.recordOutcome(ctx, series.ImdbID, season, o.Lang,
			seasonIDPrefix, kind, seasonEpCount)
	}
}

// inventorySkipped refreshes the on-disk coverage inventory for an episode
// the tracker skipped: "skip" means skip PROVIDER work, not local
// bookkeeping. Coverage badges must reflect manual file changes even in
// seasons the scanner has written off, and the scan-state stamp records the
// visit honestly as inventoried-not-searched. Publishes a coverage update
// when the inventory changed.
func inventorySkipped(ctx context.Context, deps *Deps, ls *LiveState,
	series *arrapi.Series, ep *arrapi.Episode,
) {
	if ep.EpisodeFile == nil {
		return
	}
	req := EpisodeSearchRequest(series, ep, ls.Cfg.LanguageCodes())
	if changed := ls.Engine.InventoryCoverage(ctx, &req, ep.EpisodeFile.Path); changed {
		deps.Events.PublishCoverageUpdate(api.MediaTypeEpisode, api.BuildMediaID(&req))
	}
}

// scanFullMovie scans one movie within the full-scan loop, reporting whether
// any provider was actually queried (the inter-item pacing signal).
func scanFullMovie(ctx context.Context, deps *Deps, ls *LiveState,
	m *arrapi.Movie, stats *api.ScanStats, actID string,
) (queried bool) {
	outcome, queried := ScanMovie(ctx, deps, ls, m)
	switch outcome {
	case ScanFound:
		stats.MoviesFound++
	case ScanSkipped:
		stats.MoviesSkipped++
	case ScanBackedOff:
		stats.MoviesBackedOff++
	default:
		stats.MoviesNoResult++
	}
	stats.MoviesSearched++
	total := stats.EpisodesSearched + stats.MoviesSearched
	deps.Activity.Progress(actID, total, 0,
		fmt.Sprintf("%d episodes, %d movies",
			stats.EpisodesSearched, stats.MoviesSearched))
	return queried
}

// resumeCycleStart resolves this scan pass's logical cycle start and persists
// it. A dangling mark from a previous pass means that pass never completed:
// this pass RESUMES it, keeping the original start so the resume window
// covers everything stamped since — however long the interrupted pass ran
// (the duration-blind `now - scan_interval` cutoff used to drop the early
// segment of any pass longer than the interval). A second interruption keeps
// the same origin. Failures degrade to a fresh-cycle start with a warning.
func resumeCycleStart(ctx context.Context, db ScanStore) time.Time {
	now := time.Now()
	start := now
	prev, err := db.ScanCycleStart(ctx)
	switch {
	case err != nil:
		slog.Warn("failed to load scan cycle mark, treating as fresh cycle", "error", err)
	case !prev.IsZero():
		start = prev
		slog.Info("scan resume: continuing interrupted cycle",
			"cycle_start", start.UTC().Format(time.RFC3339))
	}
	if err := db.SetScanCycleStart(ctx, start); err != nil {
		slog.Warn("failed to persist scan cycle mark", "error", err)
	}
	return start
}

func loadRecentScans(ctx context.Context, db ScanStore, scanInterval time.Duration, cycleStart time.Time) map[string]bool {
	cutoff := time.Now().Add(-scanInterval)
	// Duration-aware resume: everything stamped since the (possibly
	// interrupted) cycle's start belongs to the resume set, even when the
	// pass has already run longer than scan_interval.
	if cycleStart.Before(cutoff) {
		cutoff = cycleStart
	}
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
