package scanning

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/errgroup"
)

// FullScanResult is how a full scan ended. MediaUnwritable reports that an
// unwritable media folder refused the scan or stopped it, so the scheduler
// retries sooner than scan_interval.
type FullScanResult struct {
	Outcome         activity.Outcome
	MediaUnwritable bool
}

// RunFullScan searches every wanted episode and movie, sorted by title, after
// write-testing the media roots: a refusal fails it before any arr or
// provider request. The caller started actID's activity and applies the
// outcome via FinishScanActivity. stop is the graceful cancel, checked
// between items; cancelling ctx is a hard kill reserved for shutdown.
func RunFullScan(ctx context.Context, stop <-chan struct{}, deps *Deps, ls *LiveState, actID string) FullScanResult {
	start := time.Now()

	if err := deps.Media.Preflight(ctx, mediawrite.PreflightRequest{Roots: true, RecheckBad: true, Raise: true}); err != nil {
		if ctx.Err() != nil {
			return FullScanResult{Outcome: activity.OutcomeShutdown}
		}
		slog.Warn("full scan not started", "error", err)
		deps.Activity.Progress(actID, 0, 0, "Not started: "+err.Error())
		return FullScanResult{Outcome: activity.OutcomeFailed, MediaUnwritable: true}
	}

	var stats subflux.ScanStats
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
		return FullScanResult{Outcome: activity.OutcomeFailed}
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

	resumed, loopOutcome, writeFailure := processItems(ctx, stop, deps, ls, queue, recentlyScanned, &stats, actID, scanDelay)

	dur := time.Since(start).Round(time.Second)
	totalFound := stats.EpisodesFound + stats.MoviesFound

	if ctx.Err() != nil {
		slog.Warn("full scan interrupted by shutdown",
			"episodes_searched", stats.EpisodesSearched,
			"movies_searched", stats.MoviesSearched,
			"duration", dur.String())
		return FullScanResult{Outcome: activity.OutcomeShutdown}
	}
	if writeFailure != nil {
		// Like a user stop, the cycle mark stays dangling so the retry
		// resumes past the items already scanned.
		slog.Warn("full scan stopped: media folder not writable",
			"folder", writeFailure.Folder,
			"episodes_searched", stats.EpisodesSearched,
			"movies_searched", stats.MoviesSearched,
			"found", totalFound,
			"duration", dur.String())
		deps.Activity.Progress(actID, stats.EpisodesSearched+stats.MoviesSearched, 0, "Stopped: "+writeFailure.Error())
		return FullScanResult{Outcome: activity.OutcomeFailed, MediaUnwritable: true}
	}
	if loopOutcome == activity.OutcomeCancelled {
		// A user-stopped scan is an interrupted cycle: the cycle mark stays
		// dangling so the next pass resumes past the already-scanned items.
		slog.Info("full scan stopped by user",
			"episodes_searched", stats.EpisodesSearched,
			"movies_searched", stats.MoviesSearched,
			"found", totalFound,
			"duration", dur.String())
		return FullScanResult{Outcome: activity.OutcomeCancelled}
	}

	completeFullScan(ctx, deps, ls, &stats, resumed, start)
	return FullScanResult{Outcome: activity.OutcomeCompleted}
}

// completeFullScan closes a full pass: it clears the cycle mark, so the next
// scan applies the plain interval cutoff, then reports the totals and frees
// the provider download caches.
func completeFullScan(ctx context.Context, deps *Deps, ls *LiveState, stats *subflux.ScanStats, resumed int, start time.Time) {
	if err := deps.DB.ClearScanCycleStart(ctx); err != nil {
		slog.Warn("failed to clear scan cycle mark", "error", err)
	}

	dur := time.Since(start).Round(time.Second)
	totalFound := stats.EpisodesFound + stats.MoviesFound
	slog.Info("full scan complete",
		"episodes", stats.EpisodesSearched, "movies", stats.MoviesSearched,
		"found", totalFound, "resumed", resumed,
		"duration", dur.String())
	summary := fmt.Sprintf("Scan complete: %d found, %d searched in %s",
		totalFound,
		stats.EpisodesSearched+stats.MoviesSearched,
		dur.String())
	if backedOff := stats.EpisodesBackedOff + stats.MoviesBackedOff; backedOff > 0 {
		summary += fmt.Sprintf(", %d backed off", backedOff)
	}
	if failed := stats.EpisodesDownloadFailed + stats.MoviesDownloadFailed; failed > 0 {
		summary += fmt.Sprintf(", %d download failed", failed)
	}
	if blocked := stats.EpisodesWriteBlocked + stats.MoviesWriteBlocked; blocked > 0 {
		summary += fmt.Sprintf(", %d in unwritable folders", blocked)
	}
	deps.Alerts.RecordInfo(summary)
	slog.Info("scan results: episodes",
		"searched", stats.EpisodesSearched, "found", stats.EpisodesFound,
		"skipped", stats.EpisodesSkipped, "no_result", stats.EpisodesNoResult,
		"backed_off", stats.EpisodesBackedOff, "download_failed", stats.EpisodesDownloadFailed,
		"write_blocked", stats.EpisodesWriteBlocked, "series_skipped", stats.SeriesSkipped)
	slog.Info("scan results: movies",
		"searched", stats.MoviesSearched, "found", stats.MoviesFound,
		"skipped", stats.MoviesSkipped, "no_result", stats.MoviesNoResult,
		"backed_off", stats.MoviesBackedOff, "download_failed", stats.MoviesDownloadFailed,
		"write_blocked", stats.MoviesWriteBlocked)
	deps.Metrics.RecordScan(
		stats.EpisodesSearched+stats.MoviesSearched,
		totalFound, time.Since(start),
	)

	deps.ClearCaches(ls.Providers)
}

// processItems iterates the sorted scan queue, processing each item. The
// stop signal is honoured between items (and during the inter-item delay);
// the item in flight always completes. Returns the number of items skipped
// due to recent scanning, the loop outcome ("" for a full pass,
// cancelled/shutdown/failed when ended early) and, for a failed loop, the
// folder fault that stopped it after the item in flight.
func processItems(ctx context.Context, stop <-chan struct{}, deps *Deps, ls *LiveState,
	queue []ScanItem, recentlyScanned map[string]bool,
	stats *subflux.ScanStats, actID string, scanDelay time.Duration,
) (resumed int, outcome activity.Outcome, writeFailure *mediawrite.UnwritableError) {
	tracker := newSeasonTracker(ls.ShowCounter, deps.ShowSkipCache, buildSeedDeps(deps, ls))
	langs := ls.Cfg.LanguageCodes()
	skippedSeries := make(map[string]struct{})

	for _, item := range queue {
		if err := ctx.Err(); err != nil {
			return resumed, activity.OutcomeShutdown, nil
		}
		if stopRequested(stop) {
			return resumed, activity.OutcomeCancelled, nil
		}

		if SkipResumed(item, recentlyScanned, stats) {
			resumed++
			continue
		}

		queried, wf := scanQueueItem(ctx, deps, ls, item, tracker, langs, skippedSeries, stats, actID)
		if wf != nil {
			return resumed, activity.OutcomeFailed, wf
		}
		if !queried {
			// The item generated no provider traffic (tracker skip, all
			// targets covered on disk, manually locked, in adaptive
			// backoff, or every eligible provider health-timed-out): the
			// inter-item delay exists to pace providers, so it is skipped
			// the same way resume skips are.
			continue
		}
		if o := waitOrStop(ctx, stop, scanDelay); o != "" {
			return resumed, o, nil
		}
	}
	// The final item has no next-iteration boundary check: shutdown FIRST
	// (never reported as a user cancellation), stop SECOND, so a stop
	// landing during the last in-flight item terminates the scan as
	// cancelled rather than publishing a false success.
	if ctx.Err() != nil {
		return resumed, activity.OutcomeShutdown, nil
	}
	if stopRequested(stop) {
		return resumed, activity.OutcomeCancelled, nil
	}
	return resumed, "", nil
}

// scanQueueItem processes one scan-queue item (episode or movie) and reports
// whether it actually queried any provider (the caller keys the inter-item
// pacing delay on that) and the folder fault its save learned.
func scanQueueItem(ctx context.Context, deps *Deps, ls *LiveState, item ScanItem,
	tracker *seasonTracker, langs []string,
	skippedSeries map[string]struct{}, stats *subflux.ScanStats, actID string,
) (queried bool, writeFailure *mediawrite.UnwritableError) {
	if item.Ep == nil {
		return scanFullMovie(ctx, deps, ls, item.Movie, stats, actID)
	}
	trackerSkipped, queried, writeFailure := scanFullEpisode(ctx, deps, ls,
		item.Series, item.Ep, tracker, langs, skippedSeries, stats, actID)
	if trackerSkipped {
		// Tracker skip: zero provider work was done.
		return false, nil
	}
	return queried, writeFailure
}

// scanFullEpisode scans one episode within the full-scan loop. It reports
// whether the season tracker skipped the episode outright (show-level or
// season-level skip) and, when it did run, whether any provider was actually
// queried and the folder fault its save learned.
func scanFullEpisode(ctx context.Context, deps *Deps, ls *LiveState,
	series *arrapi.Series, ep *arrapi.Episode,
	tracker *seasonTracker, langs []string,
	skippedSeries map[string]struct{}, stats *subflux.ScanStats, actID string,
) (trackerSkipped, queried bool, writeFailure *mediawrite.UnwritableError) {
	if trackerSkips(ctx, deps, series, ep, tracker, langs, skippedSeries, stats) {
		inventorySkipped(ctx, deps, ls, series, ep)
		return true, false, nil
	}

	scan := scanEpisode(ctx, deps, ls, series, ep)

	seasonEpCount := arrsvc.SeasonEpisodeFileCount(series, ep.SeasonNumber)
	recordEpisodeOutcomes(ctx, tracker, series, ep.SeasonNumber,
		scan.Langs, seasonEpCount)

	switch scan.Outcome {
	case scanFound:
		stats.EpisodesFound++
	case scanSkipped:
		stats.EpisodesSkipped++
	case scanBackedOff:
		stats.EpisodesBackedOff++
	case scanDownloadFailed:
		stats.EpisodesDownloadFailed++
	case scanWriteBlocked:
		stats.EpisodesWriteBlocked++
	default:
		stats.EpisodesNoResult++
	}
	stats.EpisodesSearched++
	total := stats.EpisodesSearched + stats.MoviesSearched
	deps.Activity.Progress(actID, total, 0,
		fmt.Sprintf("%d episodes, %d movies",
			stats.EpisodesSearched, stats.MoviesSearched))
	return false, scan.Queried, scan.WriteFailure
}

// trackerSkips reports whether the season tracker writes the episode off at
// show or season level, counting the skip. An episode in a folder already
// known to refuse writes is never written off: the show-level check asks a
// provider and a skip stamps the item, where scanEpisode reports it
// write-blocked and leaves it unstamped.
func trackerSkips(ctx context.Context, deps *Deps, series *arrapi.Series, ep *arrapi.Episode,
	tracker *seasonTracker, langs []string,
	skippedSeries map[string]struct{}, stats *subflux.ScanStats,
) bool {
	if _, blocked := deps.Media.Blocked(ep.EpisodeFile.Path); blocked {
		return false
	}
	epCount := 0
	if series.Statistics != nil {
		epCount = series.Statistics.EpisodeFileCount
	}
	if tracker.shouldSkipShow(ctx, series.ImdbID, epCount, langs) {
		if _, seen := skippedSeries[series.ImdbID]; !seen {
			skippedSeries[series.ImdbID] = struct{}{}
			stats.SeriesSkipped++
		}
	} else if !tracker.shouldSkipEpisode(series.ImdbID, ep.SeasonNumber, langs) {
		return false
	}
	stats.EpisodesSkipped++
	stats.EpisodesSearched++
	return true
}

// recordEpisodeOutcomes records the per-language scan result for an episode's
// season. Only evidence counts: a found subtitle, or a no-result from a group
// that ran, saved nothing, had no failed target and got at least one
// provider answer. A group with a write-blocked target records nothing at
// all, since the folder, not the item, decided it.
func recordEpisodeOutcomes(ctx context.Context, tracker *seasonTracker,
	series *arrapi.Series, season int,
	outcomes []subflux.LangOutcome, seasonEpCount int,
) {
	seasonIDPrefix := mediaid.SeasonPrefix(series.TvdbID, series.ImdbID, season)
	for i := range outcomes {
		o := &outcomes[i]
		if o.Kind != subflux.LangSearched {
			continue
		}
		var kind scanOutcome
		switch {
		case o.WriteBlocked > 0:
			continue
		case o.Found():
			kind = scanFound
		case o.Failed > 0, o.Answered == 0:
			continue
		default:
			kind = scanNoResult
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
		deps.Events.PublishCoverageUpdate(&events.CoverageEvent{
			MediaType: subflux.MediaTypeEpisode, MediaID: mediaid.Build(&req),
		})
	}
}

// scanFullMovie scans one movie within the full-scan loop, reporting whether
// any provider was actually queried (the inter-item pacing signal) and the
// folder fault its save learned.
func scanFullMovie(ctx context.Context, deps *Deps, ls *LiveState,
	m *arrapi.Movie, stats *subflux.ScanStats, actID string,
) (queried bool, writeFailure *mediawrite.UnwritableError) {
	scan := scanMovieDetail(ctx, deps, ls, m)
	switch scan.Outcome {
	case scanFound:
		stats.MoviesFound++
	case scanSkipped:
		stats.MoviesSkipped++
	case scanBackedOff:
		stats.MoviesBackedOff++
	case scanDownloadFailed:
		stats.MoviesDownloadFailed++
	case scanWriteBlocked:
		stats.MoviesWriteBlocked++
	default:
		stats.MoviesNoResult++
	}
	stats.MoviesSearched++
	total := stats.EpisodesSearched + stats.MoviesSearched
	deps.Activity.Progress(actID, total, 0,
		fmt.Sprintf("%d episodes, %d movies",
			stats.EpisodesSearched, stats.MoviesSearched))
	return scan.Queried, scan.WriteFailure
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
