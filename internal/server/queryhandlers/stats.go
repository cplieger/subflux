package queryhandlers

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/errgroup"
)

// HandleStateStats handles GET /api/state/stats.
func (h *Handler) HandleStateStats(w http.ResponseWriter, r *http.Request) {
	if !httpapi.RequireGET(w, r) {
		return
	}
	resp := h.statsCache.get(r.Context(), h.computeStateStats)
	httpapi.WriteJSON(w, resp)
}

func (h *Handler) computeStateStats(ctx context.Context) subflux.Stats {
	downloads, dbAttempts, err := h.queryDB.Stats(ctx)
	if err != nil {
		slog.Warn("Stats query failed", "error", err)
	}
	lastScan, err := h.covDB.LastScanTime(ctx)
	if err != nil {
		slog.Warn("LastScanTime query failed", "error", err)
	}
	totalSubs, err := h.covDB.TotalSubtitleFiles(ctx)
	if err != nil {
		slog.Warn("TotalSubtitleFiles query failed", "error", err)
	}

	searches := h.metrics.TotalSearches()
	if searches == 0 {
		searches = int64(dbAttempts)
	}

	ls := h.state()
	allSeries, allMovies, partial := fetchMediaCountsParallel(ctx, ls)

	missing := h.countMissing(ctx, ls.Cfg, allSeries, allMovies)

	slog.Debug("handleStateStats",
		"downloads", downloads, "searches", searches,
		"total_series", len(allSeries), "total_movies", len(allMovies),
		"missing_subs", missing, "total_subs", totalSubs)

	return subflux.Stats{
		Downloads:           downloads,
		Attempts:            searches,
		LastScan:            lastScan,
		ScanIntervalSeconds: int(ls.Cfg.Search().ScanInterval.Seconds()),
		TotalSubs:           totalSubs,
		TotalSeries:         len(allSeries),
		TotalMovies:         len(allMovies),
		MissingSubs:         missing,
		Partial:             partial,
	}
}

func fetchMediaCountsParallel(ctx context.Context, ls *LiveState) (series []arrapi.Series, movies []arrapi.Movie, partial bool) {
	g, gctx := errgroup.WithContext(ctx)

	if ls.Sonarr != nil {
		g.Go(func() error {
			if got, err := ls.Sonarr.Series(gctx); err == nil {
				series = got
			} else {
				slog.Warn("stats: sonarr unreachable, series counts will be zero", "error", err)
				partial = true
			}
			return nil
		})
	}
	if ls.Radarr != nil {
		g.Go(func() error {
			if got, err := ls.Radarr.Movies(gctx); err == nil {
				movies = got
			} else {
				slog.Warn("stats: radarr unreachable, movie counts will be zero", "error", err)
				partial = true
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		slog.Warn("stats: fetch error", "error", err)
	}
	return series, movies, partial
}
