package polling

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/scanning"
)

// processPollImport is the shared logic for Sonarr/Radarr import events.
func (p *Poller) processPollImport(
	ctx context.Context, ls *LiveState, path string,
	buildFn func() (*ImportResult, error),
	refreshFn func(ctx context.Context, id int) error,
) {
	if _, err := os.Stat(path); err != nil {
		slog.Debug("poll: video file gone, skipping", "path", path)
		if _, delErr := p.deps.Store.DeleteStateByPaths(ctx, []string{path}); delErr != nil {
			slog.Warn("poll: cleanup failed", "path", path, "error", delErr)
		}
		return
	}

	if err := ls.Cfg.ValidatePath(ctx, path); err != nil {
		slog.Warn("poll: path validation failed", "path", path, "error", err)
		return
	}

	result, err := buildFn()
	if err != nil || result == nil {
		return
	}

	// Re-verify file exists after arr API calls (race window: 200-800ms).
	if _, err := os.Stat(path); err != nil {
		slog.Debug("poll: video file removed during metadata fetch", "path", path)
		return
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
		return
	}
	if len(searchResult.Paths) > 0 || searchResult.CoverageChanged {
		mediaID := api.BuildMediaID(result.Req)
		p.deps.Events.Publish(events.Event{
			Type: events.CoverageUpdate,
			Data: events.CoverageEvent{
				MediaType: api.MediaType(result.Source),
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
}

// processSonarrImport handles a single Sonarr import event from the history API.
func (p *Poller) processSonarrImport(ctx context.Context, ls *LiveState, entry *api.HistoryEntry, excludeIDs map[int]struct{}) {
	path := entry.ImportedPath()

	p.processPollImport(ctx, ls, path,
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

			origLang := series.OriginalLangCode()
			var audioLangs []string
			if ep.EpisodeFile != nil {
				audioLangs = ep.EpisodeFile.AudioLanguages()
			}
			targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)

			req := scanning.EpisodeSearchRequest(series, ep, ls.Cfg.LanguageCodes())

			return &ImportResult{
				Req:       &req,
				Targets:   targets,
				Label:     label,
				Source:    PollSourceSonarr,
				RefreshID: series.ID,
			}, nil
		},
		func(ctx context.Context, id int) error {
			return ls.Sonarr.RefreshSeries(ctx, id)
		},
	)
}

// processRadarrImport handles a single Radarr import event from the history API.
func (p *Poller) processRadarrImport(ctx context.Context, ls *LiveState, entry *api.HistoryEntry, excludeIDs map[int]struct{}) {
	path := entry.ImportedPath()

	p.processPollImport(ctx, ls, path,
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

			origLang := movie.OriginalLangCode()
			var audioLangs []string
			if movie.MovieFile != nil {
				audioLangs = movie.MovieFile.AudioLanguages()
			}
			targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)

			req := scanning.MovieSearchRequest(movie, ls.Cfg.LanguageCodes())

			return &ImportResult{
				Req:       &req,
				Targets:   targets,
				Label:     label,
				Source:    PollSourceRadarr,
				RefreshID: movie.ID,
			}, nil
		},
		func(ctx context.Context, id int) error {
			return ls.Radarr.RefreshMovie(ctx, id)
		},
	)
}
