package scanning

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// scanSeries scans all episodes in a series.
func (h *Handler) scanSeries(ctx context.Context, seriesID int) int {
	return h.scanEpisodes(ctx, seriesID, "Series",
		func(_ *arrapi.Episode) bool { return true })
}

// scanSeason scans all episodes in a specific season.
func (h *Handler) scanSeason(ctx context.Context, seriesID, seasonNum int) int {
	return h.scanEpisodes(ctx, seriesID,
		fmt.Sprintf("Season S%02d", seasonNum),
		func(ep *arrapi.Episode) bool { return ep.SeasonNumber == seasonNum })
}

// scanEpisodes fetches episodes for a series and scans those matching the filter.
func (h *Handler) scanEpisodes(ctx context.Context, seriesID int, label string,
	filterEp func(*arrapi.Episode) bool,
) int {
	st := h.deps.StateFunc()
	if st.Sonarr == nil {
		return 0
	}

	series, withFiles, ok := h.collectSeriesEpisodes(ctx, st, seriesID, label, filterEp)
	if !ok {
		return 0
	}

	action := label + " Search"
	detail := fmt.Sprintf("%s (%d episodes)", series.Title, len(withFiles))
	actID := h.startScanActivity(action, detail)
	activityOK := true
	defer func() { h.endScanActivity(actID, action, detail, activityOK) }()

	if !h.acquireScanSlot(actID) {
		slog.Debug("scan cancelled while queued",
			"series_id", seriesID, "label", label)
		return 0
	}
	defer h.deps.ScanGuard.Unlock()

	scanDelay := st.Cfg.Search().ScanDelay
	found, searched := h.runEpisodeScans(ctx, series, withFiles, actID, scanDelay)

	slog.Info(label+" scan complete",
		"series", series.Title, "searched", searched, "found", found)
	h.deps.Activity.Progress(actID, len(withFiles), len(withFiles),
		fmt.Sprintf("%s: %d/%d found", series.Title, found, searched))
	return found
}

// collectSeriesEpisodes fetches the series and the episodes-with-files that
// match filterEp. It returns ok=false (after logging, and recording an alert
// where appropriate) when the scan should abort early: the series fetch
// failed, the series was not found, the episode fetch failed, or no matching
// episode has a file.
func (h *Handler) collectSeriesEpisodes(ctx context.Context, st *HandlerState,
	seriesID int, label string, filterEp func(*arrapi.Episode) bool,
) (series *arrapi.Series, withFiles []*arrapi.Episode, ok bool) {
	ser, err := st.Sonarr.GetSeriesByID(ctx, seriesID)
	if err != nil {
		slog.Error("scan: failed to fetch series",
			"id", seriesID, "error", err)
		h.deps.Alerts.Record("scan", label+" scan failed: "+err.Error())
		return nil, nil, false
	}

	episodes, err := st.Sonarr.GetEpisodes(ctx, seriesID)
	if err != nil {
		slog.Error("scan: failed to fetch episodes",
			"series", ser.Title, "error", err)
		h.deps.Alerts.Record("scan", label+" scan failed: "+err.Error())
		return nil, nil, false
	}

	withFiles = filterEpisodesWithFiles(episodes, filterEp)
	if len(withFiles) == 0 {
		slog.Debug("scan: no episodes with files", "series", ser.Title)
		return nil, nil, false
	}
	return &ser, withFiles, true
}

// filterEpisodesWithFiles returns pointers to the episodes that match filterEp
// and have a downloaded file.
func filterEpisodesWithFiles(episodes []arrapi.Episode,
	filterEp func(*arrapi.Episode) bool,
) []*arrapi.Episode {
	var withFiles []*arrapi.Episode
	for i := range episodes {
		if filterEp(&episodes[i]) && episodes[i].HasFile && episodes[i].EpisodeFile != nil {
			withFiles = append(withFiles, &episodes[i])
		}
	}
	return withFiles
}

// acquireScanSlot marks the activity queued, blocks on the scan guard so only
// one manual scan runs at a time, then clears the queued flag. It returns
// false when the activity was cancelled while queued; in that case the guard
// has already been released and the caller must not proceed. On a true return
// the caller owns the guard and must Unlock it.
func (h *Handler) acquireScanSlot(actID string) bool {
	h.deps.Activity.SetQueued(actID, true)
	h.deps.ScanGuard.Lock()
	h.deps.Activity.SetQueued(actID, false)
	if h.deps.Activity.IsCancelled(actID) {
		h.deps.ScanGuard.Unlock()
		return false
	}
	return true
}

// runEpisodeScans scans each episode in order, reporting progress and
// honouring context cancellation and the inter-item scan delay. It returns
// the number of episodes for which a subtitle was found and the number
// searched.
func (h *Handler) runEpisodeScans(ctx context.Context, series *arrapi.Series,
	withFiles []*arrapi.Episode, actID string, scanDelay time.Duration,
) (found, searched int) {
	deps := h.deps.ScanDeps()
	sls := h.deps.ScanLiveStateFunc()
	for i, ep := range withFiles {
		if ctx.Err() != nil {
			break
		}
		h.deps.Activity.Progress(actID, i+1, len(withFiles),
			fmt.Sprintf("%s S%02dE%02d (%d/%d)",
				series.Title, ep.SeasonNumber, ep.EpisodeNumber,
				i+1, len(withFiles)))
		outcome, _ := ScanEpisode(ctx, deps, sls, series, ep, true)
		searched++
		if outcome == ScanFound {
			found++
		}
		if i < len(withFiles)-1 {
			if httputil.SleepCtx(ctx, scanDelay) != nil {
				break
			}
		}
	}
	return found, searched
}

// scanSingleEpisode scans a single episode asynchronously.
func (h *Handler) scanSingleEpisode(ctx context.Context,
	seriesID, seasonNum, episodeNum int,
) {
	st := h.deps.StateFunc()
	if st.Sonarr == nil {
		return
	}

	series, err := st.Sonarr.GetSeriesByID(ctx, seriesID)
	if err != nil {
		slog.Error("scan episode: failed to fetch series",
			"id", seriesID, "error", err)
		h.deps.Alerts.Record("scan", "Episode scan failed: "+err.Error())
		return
	}

	episodes, err := st.Sonarr.GetEpisodes(ctx, seriesID)
	if err != nil {
		slog.Error("scan episode: failed to fetch episodes",
			"series", series.Title, "error", err)
		h.deps.Alerts.Record("scan", "Episode scan failed: "+err.Error())
		return
	}

	var ep *arrapi.Episode
	for i := range episodes {
		if episodes[i].SeasonNumber == seasonNum &&
			episodes[i].EpisodeNumber == episodeNum &&
			episodes[i].HasFile && episodes[i].EpisodeFile != nil {
			ep = &episodes[i]
			break
		}
	}
	if ep == nil {
		slog.Warn("scan episode: episode not found or no file",
			"series", series.Title, "season", seasonNum,
			"episode", episodeNum)
		return
	}

	label := fmt.Sprintf("%s S%02dE%02d",
		series.Title, seasonNum, episodeNum)
	const action = "Episode Search"
	actID := h.startScanActivity(action, label)
	defer h.endScanActivity(actID, action, label, true)

	deps := h.deps.ScanDeps()
	sls := h.deps.ScanLiveStateFunc()
	outcome, _ := ScanEpisode(ctx, deps, sls, &series, ep, true)
	slog.Info("episode scan complete",
		"media", label, "outcome", outcome)
}

// scanSingleMovie scans a single movie asynchronously.
func (h *Handler) scanSingleMovie(ctx context.Context, movieID int) {
	st := h.deps.StateFunc()
	if st.Radarr == nil {
		return
	}

	movie, err := st.Radarr.GetMovieByID(ctx, movieID)
	if err != nil {
		slog.Error("scan movie: failed to fetch movie",
			"id", movieID, "error", err)
		h.deps.Alerts.Record("scan", "Movie scan failed: "+err.Error())
		return
	}
	if movie.MovieFile == nil {
		slog.Warn("scan movie: movie not found or no file",
			"id", movieID)
		return
	}

	label := fmt.Sprintf("%s (%d)", movie.Title, movie.Year)
	const action = "Movie Search"
	actID := h.startScanActivity(action, label)
	defer h.endScanActivity(actID, action, label, true)

	deps := h.deps.ScanDeps()
	sls := h.deps.ScanLiveStateFunc()
	outcome := ScanMovie(ctx, deps, sls, &movie, true)
	slog.Info("movie scan complete",
		"media", label, "outcome", outcome)
}

// scanMovieSync scans a movie synchronously and returns found/total counts.
func (h *Handler) scanMovieSync(ctx context.Context, movieID int) (found, total int) {
	st := h.deps.StateFunc()
	if st.Radarr == nil {
		return 0, 0
	}

	movie, err := st.Radarr.GetMovieByID(ctx, movieID)
	if err != nil {
		slog.Error("scan movie: failed to fetch movie",
			"id", movieID, "error", err)
		return 0, 0
	}
	if movie.MovieFile == nil {
		slog.Warn("scan movie: movie not found or no file", "id", movieID)
		return 0, 0
	}

	label := fmt.Sprintf("%s (%d)", movie.Title, movie.Year)
	const action = "Movie Search"
	actID := h.startScanActivity(action, label)
	defer h.endScanActivity(actID, action, label, true)

	if !h.acquireScanSlot(actID) {
		slog.Debug("movie scan cancelled while queued", "movie_id", movieID)
		return 0, 0
	}
	defer h.deps.ScanGuard.Unlock()

	origLang := api.OriginalLangCode(movie.OriginalLanguage)
	audioLangs := api.AudioLanguages(movie.MovieFile.MediaInfo)
	targets := st.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)
	total = len(targets)

	deps := h.deps.ScanDeps()
	sls := h.deps.ScanLiveStateFunc()
	outcome := ScanMovie(ctx, deps, sls, &movie, true)
	if outcome == ScanFound {
		found = 1
	}
	slog.Info("movie scan complete",
		"media", label, "found", found, "targets", total)
	h.deps.Activity.Progress(actID, total, total,
		fmt.Sprintf("%s: %d/%d found",
			label, found, total))
	return found, total
}
