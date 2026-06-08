package scanning

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

// scanSeries scans all episodes in a series.
func (h *Handler) scanSeries(ctx context.Context, seriesID int) int {
	return h.scanEpisodes(ctx, seriesID, "Series",
		func(_ *api.Episode) bool { return true })
}

// scanSeason scans all episodes in a specific season.
func (h *Handler) scanSeason(ctx context.Context, seriesID, seasonNum int) int {
	return h.scanEpisodes(ctx, seriesID,
		fmt.Sprintf("Season S%02d", seasonNum),
		func(ep *api.Episode) bool { return ep.SeasonNumber == seasonNum })
}

// scanEpisodes fetches episodes for a series and scans those matching the filter.
func (h *Handler) scanEpisodes(ctx context.Context, seriesID int, label string,
	filterEp func(*api.Episode) bool,
) int {
	st := h.deps.StateFunc()
	if st.Sonarr == nil {
		return 0
	}

	series, err := st.Sonarr.GetSeriesByID(ctx, seriesID)
	if err != nil {
		slog.Error("scan: failed to fetch series",
			"id", seriesID, "error", err)
		h.deps.Alerts.Record("scan", label+" scan failed: "+err.Error())
		return 0
	}
	if series == nil {
		slog.Warn("scan: series not found", "id", seriesID)
		return 0
	}

	episodes, err := st.Sonarr.GetEpisodes(ctx, seriesID)
	if err != nil {
		slog.Error("scan: failed to fetch episodes",
			"series", series.Title, "error", err)
		h.deps.Alerts.Record("scan", label+" scan failed: "+err.Error())
		return 0
	}

	var withFiles []*api.Episode
	for i := range episodes {
		if filterEp(&episodes[i]) && episodes[i].HasFile && episodes[i].EpisodeFile != nil {
			withFiles = append(withFiles, &episodes[i])
		}
	}
	if len(withFiles) == 0 {
		slog.Debug("scan: no episodes with files", "series", series.Title)
		return 0
	}

	action := label + " Search"
	detail := fmt.Sprintf("%s (%d episodes)", series.Title, len(withFiles))
	actID := h.startScanActivity(action, detail)
	ok := true
	defer func() { h.endScanActivity(actID, action, detail, ok) }()

	h.deps.Activity.SetQueued(actID, true)
	h.deps.ScanGuard.Lock()
	defer h.deps.ScanGuard.Unlock()
	h.deps.Activity.SetQueued(actID, false)

	if h.deps.Activity.IsCancelled(actID) {
		slog.Debug("scan cancelled while queued",
			"series_id", seriesID, "label", label)
		return 0
	}

	scanDelay := st.Cfg.Search().ScanDelay
	deps := h.deps.ScanDeps()
	sls := h.deps.ScanLiveStateFunc()
	var found, searched int
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

	slog.Info(label+" scan complete",
		"series", series.Title, "searched", searched, "found", found)
	h.deps.Activity.Progress(actID, len(withFiles), len(withFiles),
		fmt.Sprintf("%s: %d/%d found", series.Title, found, searched))
	return found
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
	if series == nil {
		slog.Warn("scan episode: series not found", "id", seriesID)
		return
	}

	episodes, err := st.Sonarr.GetEpisodes(ctx, seriesID)
	if err != nil {
		slog.Error("scan episode: failed to fetch episodes",
			"series", series.Title, "error", err)
		h.deps.Alerts.Record("scan", "Episode scan failed: "+err.Error())
		return
	}

	var ep *api.Episode
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
	outcome, _ := ScanEpisode(ctx, deps, sls, series, ep, true)
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
	if movie == nil || movie.MovieFile == nil {
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
	outcome := ScanMovie(ctx, deps, sls, movie, true)
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
	if movie == nil || movie.MovieFile == nil {
		slog.Warn("scan movie: movie not found or no file", "id", movieID)
		return 0, 0
	}

	label := fmt.Sprintf("%s (%d)", movie.Title, movie.Year)
	const action = "Movie Search"
	actID := h.startScanActivity(action, label)
	defer h.endScanActivity(actID, action, label, true)

	h.deps.Activity.SetQueued(actID, true)
	h.deps.ScanGuard.Lock()
	defer h.deps.ScanGuard.Unlock()
	h.deps.Activity.SetQueued(actID, false)

	if h.deps.Activity.IsCancelled(actID) {
		slog.Debug("movie scan cancelled while queued", "movie_id", movieID)
		return 0, 0
	}

	origLang := movie.OriginalLangCode()
	audioLangs := movie.MovieFile.AudioLanguages()
	targets := st.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)
	total = len(targets)

	deps := h.deps.ScanDeps()
	sls := h.deps.ScanLiveStateFunc()
	outcome := ScanMovie(ctx, deps, sls, movie, true)
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
