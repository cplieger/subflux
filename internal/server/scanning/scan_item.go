package scanning

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/subflux"
)

// itemScan is one item's scan result.
type itemScan struct {
	// WriteFailure is set when the item's save learned that its folder
	// refuses writes; the scan stops after this item.
	WriteFailure *mediawrite.UnwritableError
	Outcome      scanOutcome
	// Langs are the engine's per-language outcomes; the season tracker
	// records evidence only for subflux.LangSearched entries.
	Langs   []subflux.LangOutcome
	Found   int  // targets saved
	Queried bool // a provider was queried: the inter-item pacing signal
}

// scanEpisode searches for subtitles for a single episode.
func scanEpisode(ctx context.Context, deps *Deps, ls *LiveState, series *arrapi.Series, ep *arrapi.Episode, forceUpgrade ...bool) itemScan {
	label := fmt.Sprintf("%s (%d) - S%02dE%02d", series.Title, series.Year, ep.SeasonNumber, ep.EpisodeNumber)
	slog.Debug("scan: processing episode",
		"media", label, "imdb", series.ImdbID,
		"scene", ep.EpisodeFile.SceneName,
		"path", ep.EpisodeFile.Path)

	origLang := arrsvc.OriginalLangCode(series.OriginalLanguage)
	audioLangs := arrsvc.AudioLanguages(ep.EpisodeFile.MediaInfo)
	targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)

	req := EpisodeSearchRequest(series, ep, ls.Cfg.LanguageCodes())
	req.ForceUpgrade = len(forceUpgrade) > 0 && forceUpgrade[0]

	result, err := ls.Engine.SearchTargets(ctx, &req, ep.EpisodeFile.Path, targets)
	scan := itemScan{Langs: result.Langs, Queried: result.ProviderQueried()}
	if !errors.As(err, &scan.WriteFailure) && err != nil {
		slog.Warn("episode search failed", "media", label, "error", err)
	}
	paths := result.Paths()
	scan.Found = len(paths)
	if len(paths) > 0 || result.CoverageChanged {
		mediaID := mediaid.Build(&req)
		deps.Events.PublishCoverageUpdate(&events.CoverageEvent{
			MediaType: subflux.MediaTypeEpisode, MediaID: mediaID,
		})
		if len(paths) > 0 && ls.Sonarr != nil {
			if err := ls.Sonarr.RescanSeries(ctx, series.ID); err != nil {
				slog.Warn("failed to refresh series", "series_id", series.ID, "error", err)
			}
		}
	}
	scan.Outcome = itemOutcome(&result)
	return scan
}

func itemOutcome(result *subflux.SearchResult) scanOutcome {
	switch {
	case len(result.Paths()) > 0:
		return scanFound
	case result.WriteBlocked() > 0:
		return scanWriteBlocked
	case result.TargetsFailed() > 0:
		return scanDownloadFailed
	case result.TargetsSearched() > 0:
		return scanNoResult
	case result.TargetsBackedOff() > 0:
		// Every language needing a search had all providers in adaptive
		// backoff: no query ran, so this is neither skipped-as-covered nor
		// searched-with-no-result.
		return scanBackedOff
	default:
		return scanSkipped
	}
}

// scanMovieDetail searches for subtitles for a single movie. Found counts
// the targets saved (one file each), for callers that report per-target
// found counts.
func scanMovieDetail(ctx context.Context, deps *Deps, ls *LiveState, m *arrapi.Movie, forceUpgrade ...bool) itemScan {
	label := fmt.Sprintf("%s (%d)", m.Title, m.Year)
	slog.Debug("scan: processing movie",
		"media", label, "imdb", m.ImdbID, "tmdb", m.TmdbID,
		"scene", m.MovieFile.SceneName,
		"path", m.MovieFile.Path)

	origLang := arrsvc.OriginalLangCode(m.OriginalLanguage)
	audioLangs := arrsvc.AudioLanguages(m.MovieFile.MediaInfo)
	targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)

	req := MovieSearchRequest(m, ls.Cfg.LanguageCodes())
	req.ForceUpgrade = len(forceUpgrade) > 0 && forceUpgrade[0]

	result, err := ls.Engine.SearchTargets(ctx, &req, m.MovieFile.Path, targets)
	scan := itemScan{Langs: result.Langs, Queried: result.ProviderQueried()}
	if !errors.As(err, &scan.WriteFailure) && err != nil {
		slog.Warn("movie search failed", "media", label, "error", err)
	}
	paths := result.Paths()
	scan.Found = len(paths)
	if len(paths) > 0 || result.CoverageChanged {
		mediaID := mediaid.Build(&req)
		deps.Events.PublishCoverageUpdate(&events.CoverageEvent{
			MediaType: subflux.MediaTypeMovie, MediaID: mediaID,
		})
		if len(paths) > 0 && ls.Radarr != nil {
			if err := ls.Radarr.RescanMovie(ctx, m.ID); err != nil {
				slog.Warn("failed to refresh movie", "movie_id", m.ID, "error", err)
			}
		}
	}
	scan.Outcome = itemOutcome(&result)
	return scan
}

// SceneOrPath returns sceneName if set, otherwise filePath.
func SceneOrPath(sceneName, filePath string) string {
	if sceneName != "" {
		return sceneName
	}
	return filePath
}

// ExtractAltTitles returns the alternate titles distinct from primary, case-insensitively deduped.
func ExtractAltTitles(alts []arrapi.AlternateTitle, primary string) []string {
	if len(alts) == 0 {
		return nil
	}
	seen := map[string]bool{strings.ToLower(primary): true}
	var titles []string
	for _, a := range alts {
		lower := strings.ToLower(a.Title)
		if a.Title != "" && !seen[lower] {
			seen[lower] = true
			titles = append(titles, a.Title)
		}
	}
	return titles
}

func collectEpisodes(ctx context.Context, ls *LiveState, alerts AlertRecorder,
	excludeTags map[int]struct{},
) []ScanItem {
	items := make([]ScanItem, 0, 60000)
	slog.Debug("fetching series from sonarr")
	err := ls.Sonarr.WantedEpisodes(ctx, excludeTags,
		func(series arrapi.Series, ep arrapi.Episode) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if ep.EpisodeFile != nil {
				ser := series
				e := ep
				items = append(items, ScanItem{Series: &ser, Ep: &e})
			}
			return nil
		})
	if err != nil {
		slog.Error("series fetch failed", "error", err)
		alerts.Record("scan", "Series fetch failed: "+err.Error())
	}
	return items
}

func collectMovies(ctx context.Context, ls *LiveState, alerts AlertRecorder,
	excludeTags map[int]struct{},
) []ScanItem {
	items := make([]ScanItem, 0, 5000)
	slog.Debug("fetching movies from radarr")
	err := ls.Radarr.WantedMovies(ctx, excludeTags,
		func(m arrapi.Movie) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if m.MovieFile != nil {
				mov := m
				items = append(items, ScanItem{Movie: &mov})
			}
			return nil
		})
	if err != nil {
		slog.Error("movie fetch failed", "error", err)
		alerts.Record("scan", "Movie fetch failed: "+err.Error())
	}
	return items
}

// EpisodeSearchRequest builds a SearchRequest from arr Series+Episode data.
// This is the single source of truth for the episode→SearchRequest mapping,
// used by both scanning and polling.
func EpisodeSearchRequest(series *arrapi.Series, ep *arrapi.Episode, langs []string) subflux.SearchRequest {
	origLang := arrsvc.OriginalLangCode(series.OriginalLanguage)
	var audioLangs []string
	if ep.EpisodeFile != nil {
		audioLangs = arrsvc.AudioLanguages(ep.EpisodeFile.MediaInfo)
	}
	resolvedAudio := origLang
	if resolvedAudio == "" && len(audioLangs) > 0 {
		resolvedAudio = audioLangs[0]
	}
	sceneName := ""
	if ep.EpisodeFile != nil {
		sceneName = SceneOrPath(ep.EpisodeFile.SceneName, ep.EpisodeFile.Path)
	}
	return subflux.SearchRequest{
		Title:             series.Title,
		AlternativeTitles: ExtractAltTitles(series.AlternateTitles, series.Title),
		EpisodeTitle:      ep.Title,
		Year:              series.Year,
		Season:            ep.SeasonNumber,
		Episode:           ep.EpisodeNumber,
		SceneSeason:       ep.SceneSeasonNumber,
		SceneEpisode:      ep.SceneEpisodeNumber,
		AbsoluteEpisode:   ep.AbsoluteEpisodeNumber,
		ImdbID:            series.ImdbID,
		TvdbID:            series.TvdbID,
		Languages:         langs,
		ReleaseName:       sceneName,
		MediaType:         subflux.MediaTypeEpisode,
		AudioLang:         resolvedAudio,
	}
}

// MovieSearchRequest builds a SearchRequest from arr Movie data.
// This is the single source of truth for the movie→SearchRequest mapping,
// used by both scanning and polling.
func MovieSearchRequest(m *arrapi.Movie, langs []string) subflux.SearchRequest {
	origLang := arrsvc.OriginalLangCode(m.OriginalLanguage)
	var audioLangs []string
	if m.MovieFile != nil {
		audioLangs = arrsvc.AudioLanguages(m.MovieFile.MediaInfo)
	}
	resolvedAudio := origLang
	if resolvedAudio == "" && len(audioLangs) > 0 {
		resolvedAudio = audioLangs[0]
	}
	sceneName := ""
	if m.MovieFile != nil {
		sceneName = SceneOrPath(m.MovieFile.SceneName, m.MovieFile.Path)
	}
	return subflux.SearchRequest{
		Title:             m.Title,
		AlternativeTitles: ExtractAltTitles(m.AlternateTitles, m.Title),
		Year:              m.Year,
		ImdbID:            m.ImdbID,
		TmdbID:            m.TmdbID,
		Languages:         langs,
		ReleaseName:       sceneName,
		MediaType:         subflux.MediaTypeMovie,
		AudioLang:         resolvedAudio,
	}
}
