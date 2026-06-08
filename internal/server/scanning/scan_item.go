package scanning

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cplieger/subflux/internal/api"
)

// ScanEpisode searches for subtitles for a single episode.
// Returns the scan outcome and the language codes that had at least one
// subtitle downloaded.
func ScanEpisode(ctx context.Context, deps *Deps, ls *LiveState, series *api.Series, ep *api.Episode, forceUpgrade ...bool) (outcome ScanOutcome, foundLangs []string) {
	label := fmt.Sprintf("%s (%d) - S%02dE%02d", series.Title, series.Year, ep.SeasonNumber, ep.EpisodeNumber)
	slog.Debug("scan: processing episode",
		"media", label, "imdb", series.ImdbID,
		"scene", ep.EpisodeFile.SceneName,
		"path", ep.EpisodeFile.Path)

	origLang := series.OriginalLangCode()
	audioLangs := ep.EpisodeFile.AudioLanguages()
	targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)

	req := EpisodeSearchRequest(series, ep, ls.Cfg.LanguageCodes())
	req.ForceUpgrade = len(forceUpgrade) > 0 && forceUpgrade[0]

	result, err := ls.Engine.SearchTargets(ctx, &req, ep.EpisodeFile.Path, targets)
	if err != nil {
		slog.Warn("episode search failed", "media", label, "error", err)
	}
	if len(result.Paths) > 0 || result.CoverageChanged {
		mediaID := api.BuildMediaID(&req)
		deps.Events.PublishCoverageUpdate(api.MediaTypeEpisode, mediaID)
		if len(result.Paths) > 0 && ls.Sonarr != nil {
			if err := ls.Sonarr.RefreshSeries(ctx, series.ID); err != nil {
				slog.Warn("failed to refresh series", "series_id", series.ID, "error", err)
			}
		}
		if len(result.Paths) > 0 {
			return ScanFound, result.FoundLangs
		}
	}
	if result.Searched == 0 {
		return ScanSkipped, nil
	}
	return ScanNoResult, nil
}

// ScanMovie searches for subtitles for a single movie.
func ScanMovie(ctx context.Context, deps *Deps, ls *LiveState, m *api.Movie, forceUpgrade ...bool) ScanOutcome {
	label := fmt.Sprintf("%s (%d)", m.Title, m.Year)
	slog.Debug("scan: processing movie",
		"media", label, "imdb", m.ImdbID, "tmdb", m.TmdbID,
		"scene", m.MovieFile.SceneName,
		"path", m.MovieFile.Path)

	origLang := m.OriginalLangCode()
	audioLangs := m.MovieFile.AudioLanguages()
	targets := ls.Cfg.ResolveTargetsWithFallback(origLang, audioLangs)

	req := MovieSearchRequest(m, ls.Cfg.LanguageCodes())
	req.ForceUpgrade = len(forceUpgrade) > 0 && forceUpgrade[0]

	result, err := ls.Engine.SearchTargets(ctx, &req, m.MovieFile.Path, targets)
	if err != nil {
		slog.Warn("movie search failed", "media", label, "error", err)
	}
	if len(result.Paths) > 0 || result.CoverageChanged {
		mediaID := api.BuildMediaID(&req)
		deps.Events.PublishCoverageUpdate(api.MediaTypeMovie, mediaID)
		if len(result.Paths) > 0 && ls.Radarr != nil {
			if err := ls.Radarr.RefreshMovie(ctx, m.ID); err != nil {
				slog.Warn("failed to refresh movie", "movie_id", m.ID, "error", err)
			}
		}
		if len(result.Paths) > 0 {
			return ScanFound
		}
	}
	if result.Searched == 0 {
		return ScanSkipped
	}
	return ScanNoResult
}

// SceneOrPath returns sceneName if non-empty, otherwise filePath.
func SceneOrPath(sceneName, filePath string) string {
	if sceneName != "" {
		return sceneName
	}
	return filePath
}

// ExtractAltTitles returns unique alternative titles, excluding the primary.
func ExtractAltTitles(alts []api.AlternateTitle, primary string) []string {
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

// collectEpisodes fetches all wanted episodes from Sonarr.
func collectEpisodes(ctx context.Context, ls *LiveState, alerts AlertRecorder,
	excludeTags map[int]struct{},
) []ScanItem {
	items := make([]ScanItem, 0, 60000)
	slog.Debug("fetching series from sonarr")
	err := ls.Sonarr.GetWantedEpisodes(ctx, excludeTags,
		func(series api.Series, ep api.Episode) error {
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

// collectMovies fetches all wanted movies from Radarr.
func collectMovies(ctx context.Context, ls *LiveState, alerts AlertRecorder,
	excludeTags map[int]struct{},
) []ScanItem {
	items := make([]ScanItem, 0, 5000)
	slog.Debug("fetching movies from radarr")
	err := ls.Radarr.GetWantedMovies(ctx, excludeTags,
		func(m api.Movie) error {
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
func EpisodeSearchRequest(series *api.Series, ep *api.Episode, langs []string) api.SearchRequest {
	origLang := series.OriginalLangCode()
	var audioLangs []string
	if ep.EpisodeFile != nil {
		audioLangs = ep.EpisodeFile.AudioLanguages()
	}
	resolvedAudio := origLang
	if resolvedAudio == "" && len(audioLangs) > 0 {
		resolvedAudio = audioLangs[0]
	}
	sceneName := ""
	if ep.EpisodeFile != nil {
		sceneName = SceneOrPath(ep.EpisodeFile.SceneName, ep.EpisodeFile.Path)
	}
	return api.SearchRequest{
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
		MediaType:         api.MediaTypeEpisode,
		AudioLang:         resolvedAudio,
	}
}

// MovieSearchRequest builds a SearchRequest from arr Movie data.
// This is the single source of truth for the movie→SearchRequest mapping,
// used by both scanning and polling.
func MovieSearchRequest(m *api.Movie, langs []string) api.SearchRequest {
	origLang := m.OriginalLangCode()
	var audioLangs []string
	if m.MovieFile != nil {
		audioLangs = m.MovieFile.AudioLanguages()
	}
	resolvedAudio := origLang
	if resolvedAudio == "" && len(audioLangs) > 0 {
		resolvedAudio = audioLangs[0]
	}
	sceneName := ""
	if m.MovieFile != nil {
		sceneName = SceneOrPath(m.MovieFile.SceneName, m.MovieFile.Path)
	}
	return api.SearchRequest{
		Title:             m.Title,
		AlternativeTitles: ExtractAltTitles(m.AlternateTitles, m.Title),
		Year:              m.Year,
		ImdbID:            m.ImdbID,
		TmdbID:            m.TmdbID,
		Languages:         langs,
		ReleaseName:       sceneName,
		MediaType:         api.MediaTypeMovie,
		AudioLang:         resolvedAudio,
	}
}
