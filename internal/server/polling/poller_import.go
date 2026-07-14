package polling

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/scanning"
)

// precheckImportPath verifies the imported video still exists on disk and that
// its path passes config validation before any arr API calls are made. When the
// file has disappeared between poll cycles it cleans up stale DB state via
// DeleteStateByPaths. Returns false when the import should be skipped.
func (p *Poller) precheckImportPath(ctx context.Context, ls *LiveState, path string) bool {
	if _, err := os.Stat(path); err != nil {
		slog.Debug("poll: video file gone, skipping", "path", path)
		if _, delErr := p.deps.Store.DeleteStateByPaths(ctx, []string{path}); delErr != nil {
			slog.Warn("poll: cleanup failed", "path", path, "error", delErr)
		}
		return false
	}

	if err := ls.Cfg.ValidatePath(ctx, path); err != nil {
		slog.Warn("poll: path validation failed", "path", path, "error", err)
		return false
	}
	return true
}

// processPollImport is the shared logic for Sonarr/Radarr import events.
func (p *Poller) processPollImport(
	ctx context.Context, ls *LiveState, path string,
	buildFn func() (*ImportResult, error),
	refreshFn func(ctx context.Context, id int) error,
) (retryable bool) {
	if !p.precheckImportPath(ctx, ls, path) {
		return false
	}

	result, err := buildFn()
	if err != nil {
		// Transient arr failure (metadata fetch): the caller holds the poll
		// watermark back (bounded by maxImportRetries) so the entry is
		// re-fetched next cycle instead of dropped until the next full scan.
		return true
	}
	if result == nil {
		// Deliberate skip (e.g. excluded by tag): processed, never retried.
		return false
	}

	// Re-verify file exists after arr API calls (race window: 200-800ms).
	if _, err := os.Stat(path); err != nil {
		slog.Debug("poll: video file removed during metadata fetch", "path", path)
		return retryable
	}

	slog.Info("poll: import detected",
		"media", result.Label, "path", path)
	p.deps.Metrics.RecordImport(api.PollKey(result.Source))

	searchResult, searchErr := ls.Engine.SearchTargets(ctx, result.Req, path, result.Targets)
	if searchErr != nil {
		slog.Error("poll: subtitle search failed",
			"media", result.Label, "error", searchErr)
		p.deps.Alerts.RecordWarn(string(result.Source),
			fmt.Sprintf("Search failed for %s: %v", result.Label, searchErr))
		return retryable
	}
	if len(searchResult.Paths) > 0 || searchResult.CoverageChanged {
		mediaID := api.BuildMediaID(result.Req)
		p.deps.Events.Publish(events.Event{
			Type: events.CoverageUpdate,
			Data: events.CoverageEvent{
				// The event carries the MEDIA type ("episode"/"movie"), not the
				// poll source ("sonarr"/"radarr"): the client wire decoder
				// rejects source names, which silently killed the targeted
				// row-refresh path for poller-driven downloads.
				MediaType: result.Req.MediaType,
				MediaID:   mediaID,
			},
		})
		p.deps.StatsCache.Invalidate()
		if len(searchResult.Paths) > 0 && refreshFn != nil {
			if err := refreshFn(ctx, result.RefreshID); err != nil {
				slog.Warn("failed to notify arr", "id", result.RefreshID, "error", err)
			}
		}
	}
	return false
}

// processSonarrImport handles a single Sonarr import event from the history API.
func (p *Poller) processSonarrImport(ctx context.Context, ls *LiveState, entry *arrapi.HistoryRecord, excludeIDs map[int]struct{}) (retryable bool) {
	path := entry.ImportedPath()

	return p.processPollImport(
		ctx, ls, path,
		func() (*ImportResult, error) {
			series, err := ls.Sonarr.GetSeriesByID(ctx, entry.SeriesID)
			if err != nil {
				slog.Warn("poll: failed to get series", "series_id", entry.SeriesID, "error", err)
				return nil, err
			}
			if api.HasExcludeTag(series.Tags, excludeIDs) {
				slog.Info("poll: series excluded by tag", "series", series.Title)
				return nil, nil
			}

			ep, err := ls.Sonarr.GetEpisodeByID(ctx, entry.EpisodeID)
			if err != nil {
				slog.Warn("poll: failed to get episode", "episode_id", entry.EpisodeID, "error", err)
				return nil, err
			}

			label := fmt.Sprintf("%s (%d) - S%02dE%02d", series.Title, series.Year, ep.SeasonNumber, ep.EpisodeNumber)

			origLang := api.OriginalLangCode(series.OriginalLanguage)
			var audioLangs []string
			if ep.EpisodeFile != nil {
				audioLangs = api.AudioLanguages(ep.EpisodeFile.MediaInfo)
			}
			targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)

			req := scanning.EpisodeSearchRequest(&series, &ep, ls.Cfg.LanguageCodes())

			return &ImportResult{
				Req:       &req,
				Targets:   targets,
				Label:     label,
				Source:    PollSourceSonarr,
				RefreshID: series.ID,
			}, nil
		},
		func(ctx context.Context, id int) error {
			return ls.Sonarr.RescanSeries(ctx, id)
		},
	)
}

// processRadarrImport handles a single Radarr import event from the history API.
func (p *Poller) processRadarrImport(ctx context.Context, ls *LiveState, entry *arrapi.HistoryRecord, excludeIDs map[int]struct{}) (retryable bool) {
	path := entry.ImportedPath()

	return p.processPollImport(
		ctx, ls, path,
		func() (*ImportResult, error) {
			movie, err := ls.Radarr.GetMovieByID(ctx, entry.MovieID)
			if err != nil {
				slog.Warn("poll: failed to get movie", "movie_id", entry.MovieID, "error", err)
				return nil, err
			}
			if api.HasExcludeTag(movie.Tags, excludeIDs) {
				slog.Info("poll: movie excluded by tag", "movie", movie.Title)
				return nil, nil
			}

			label := fmt.Sprintf("%s (%d)", movie.Title, movie.Year)

			origLang := api.OriginalLangCode(movie.OriginalLanguage)
			var audioLangs []string
			if movie.MovieFile != nil {
				audioLangs = api.AudioLanguages(movie.MovieFile.MediaInfo)
			}
			targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)

			req := scanning.MovieSearchRequest(&movie, ls.Cfg.LanguageCodes())

			return &ImportResult{
				Req:       &req,
				Targets:   targets,
				Label:     label,
				Source:    PollSourceRadarr,
				RefreshID: movie.ID,
			}, nil
		},
		func(ctx context.Context, id int) error {
			return ls.Radarr.RescanMovie(ctx, id)
		},
	)
}
