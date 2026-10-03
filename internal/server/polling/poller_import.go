package polling

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/mediapresence"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/subflux"
)

// precheckImportPath decides an import before any arr API call is made. Only a
// video Presence reports definitely gone has its stale state deleted; a video
// whose media root cannot be read is held on that root (heldOn), never
// cleaned up. proceed is false for every outcome but a present, valid path.
func (p *Poller) precheckImportPath(ctx context.Context, ls *LiveState, path string) (proceed bool, heldOn string) {
	gone, err := p.deps.Presence.Gone(ctx, path)
	if uerr, ok := errors.AsType[*mediapresence.UnavailableError](err); ok {
		slog.Debug("poll: media root unavailable, holding the import", "path", path, "root", uerr.Root)
		return false, uerr.Root
	}
	if ctx.Err() != nil {
		return false, ""
	}
	if gone {
		slog.Debug("poll: video file gone, skipping", "path", path)
		if _, delErr := p.deps.Store.DeleteStateByPaths(ctx, []string{path}); delErr != nil {
			slog.Warn("poll: cleanup failed", "path", path, "error", delErr)
		}
		return false, ""
	}

	if err := ls.Cfg.ValidatePath(ctx, path); err != nil {
		slog.Warn("poll: path validation failed", "path", path, "error", err)
		return false, ""
	}
	return true, ""
}

type importResult struct {
	// heldOn is the folder that refused, when held and known.
	heldOn string
	// retryable: a transient arr failure; the watermark holds below the entry
	// for at most maxImportRetries cycles.
	retryable bool
	// queried: the search queried a provider, which keys the inter-entry
	// pacing delay, so skip paths and traffic-free searches don't pay it.
	queried bool
	// held: the entry's folder refuses writes; the batch stops here and the
	// watermark stays below the entry until the folder recovers.
	held bool
}

// pendingImport is one import resolved against its arr. result is nil for an
// entry that is skipped, retryable marks a transient arr failure, and heldOn
// names the unreadable media root an entry waits on.
type pendingImport struct {
	result    *ImportResult
	refresh   func(ctx context.Context, id int) error
	path      string
	heldOn    string
	retryable bool
}

// asksForSubtitles reports whether the import resolved at least one target.
// An entry with none still searches: the engine records what the folder
// already holds and writes nothing.
func (pi *pendingImport) asksForSubtitles() bool {
	return pi.result != nil && len(pi.result.Targets) > 0
}

func (p *Poller) resolveImport(
	ctx context.Context, ls *LiveState, path string,
	buildFn func() (*ImportResult, error),
	refreshFn func(ctx context.Context, id int) error,
) pendingImport {
	pi := pendingImport{path: path, refresh: refreshFn}
	proceed, heldOn := p.precheckImportPath(ctx, ls, path)
	if !proceed {
		pi.heldOn = heldOn
		return pi
	}
	result, err := buildFn()
	if err != nil {
		// Transient arr failure (metadata fetch): the caller holds the poll
		// watermark back (bounded by maxImportRetries) so the entry is
		// re-fetched next cycle instead of dropped until the next full scan.
		pi.retryable = true
		return pi
	}
	// A nil result is a deliberate skip (e.g. excluded by tag), never retried.
	pi.result = result
	return pi
}

// runImport searches a resolved import. The folder is tested again because
// another save can learn of a fault after the batch's write test passed.
func (p *Poller) runImport(ctx context.Context, ls *LiveState, pi *pendingImport) importResult {
	if pi.heldOn != "" {
		return importResult{held: true, heldOn: pi.heldOn}
	}
	if pi.result == nil {
		return importResult{retryable: pi.retryable}
	}
	// The video can vanish, or its share go away, after its entry resolved,
	// while earlier entries of the batch searched.
	gone, err := p.deps.Presence.Gone(ctx, pi.path)
	if uerr, ok := errors.AsType[*mediapresence.UnavailableError](err); ok {
		return importResult{held: true, heldOn: uerr.Root}
	}
	if ctx.Err() != nil {
		return importResult{}
	}
	if gone {
		slog.Debug("poll: video file removed after its import resolved", "path", pi.path)
		return importResult{}
	}
	if pi.asksForSubtitles() {
		if err := p.deps.Media.Preflight(ctx, mediawrite.PreflightRequest{Folders: []string{filepath.Dir(pi.path)}}); err != nil {
			return importResult{held: true, heldOn: refusedFolder(err)}
		}
	}
	return p.searchImport(ctx, ls, pi.path, pi.result, pi.refresh)
}

func refusedFolder(err error) string {
	if uerr, ok := errors.AsType[*mediawrite.UnwritableError](err); ok {
		return uerr.Folder
	}
	return ""
}

// searchImport searches one import and publishes what it saved. A search cut
// short by the poller stopping reports nothing.
func (p *Poller) searchImport(ctx context.Context, ls *LiveState, path string,
	result *ImportResult, refreshFn func(ctx context.Context, id int) error,
) importResult {
	slog.Info("poll: import detected",
		"media", result.Label, "path", path)
	p.deps.Metrics.RecordImport(subflux.PollKey(result.Source))

	searchResult, searchErr := ls.Engine.SearchTargets(ctx, result.Req, path, result.Targets)
	queried := searchResult.ProviderQueried()
	// The media writer already logged and alerted the folder fault.
	if folder := refusedFolder(searchErr); folder != "" {
		return importResult{held: true, heldOn: folder, queried: queried}
	}
	if searchResult.WriteBlocked() > 0 {
		folder, _ := p.deps.Media.Blocked(path)
		return importResult{held: true, heldOn: folder, queried: queried}
	}
	if searchErr != nil {
		if ctx.Err() == nil {
			slog.Error("poll: subtitle search failed",
				"media", result.Label, "error", searchErr)
			p.deps.Alerts.RecordWarn(string(result.Source),
				fmt.Sprintf("Search failed for %s: %v", result.Label, searchErr))
		}
		return importResult{queried: queried}
	}
	searchPaths := searchResult.Paths()
	if len(searchPaths) > 0 || searchResult.CoverageChanged {
		mediaID := mediaid.Build(result.Req)
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
		if len(searchPaths) > 0 && refreshFn != nil {
			if err := refreshFn(ctx, result.RefreshID); err != nil {
				slog.Warn("failed to notify arr", "id", result.RefreshID, "error", err)
			}
		}
	}
	return importResult{queried: queried}
}

func (p *Poller) resolveSonarrImport(ctx context.Context, ls *LiveState, entry *arrapi.HistoryRecord, excludeIDs map[int]struct{}) pendingImport {
	path := entry.ImportedPath()

	return p.resolveImport(
		ctx, ls, path,
		func() (*ImportResult, error) {
			series, err := ls.Sonarr.SeriesByID(ctx, entry.SeriesID)
			if err != nil {
				slog.Warn("poll: failed to get series", "series_id", entry.SeriesID, "error", err)
				return nil, err
			}
			if arrsvc.HasExcludeTag(series.Tags, excludeIDs) {
				slog.Info("poll: series excluded by tag", "series", series.Title)
				return nil, nil
			}

			ep, err := ls.Sonarr.EpisodeByID(ctx, entry.EpisodeID)
			if err != nil {
				slog.Warn("poll: failed to get episode", "episode_id", entry.EpisodeID, "error", err)
				return nil, err
			}

			label := fmt.Sprintf("%s (%d) - S%02dE%02d", series.Title, series.Year, ep.SeasonNumber, ep.EpisodeNumber)

			origLang := arrsvc.OriginalLangCode(series.OriginalLanguage)
			var audioLangs []string
			if ep.EpisodeFile != nil {
				audioLangs = arrsvc.AudioLanguages(ep.EpisodeFile.MediaInfo)
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

func (p *Poller) resolveRadarrImport(ctx context.Context, ls *LiveState, entry *arrapi.HistoryRecord, excludeIDs map[int]struct{}) pendingImport {
	path := entry.ImportedPath()

	return p.resolveImport(
		ctx, ls, path,
		func() (*ImportResult, error) {
			movie, err := ls.Radarr.MovieByID(ctx, entry.MovieID)
			if err != nil {
				slog.Warn("poll: failed to get movie", "movie_id", entry.MovieID, "error", err)
				return nil, err
			}
			if arrsvc.HasExcludeTag(movie.Tags, excludeIDs) {
				slog.Info("poll: movie excluded by tag", "movie", movie.Title)
				return nil, nil
			}

			label := fmt.Sprintf("%s (%d)", movie.Title, movie.Year)

			origLang := arrsvc.OriginalLangCode(movie.OriginalLanguage)
			var audioLangs []string
			if movie.MovieFile != nil {
				audioLangs = arrsvc.AudioLanguages(movie.MovieFile.MediaInfo)
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
