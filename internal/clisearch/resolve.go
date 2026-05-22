package clisearch

import (
	"context"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"subflux/internal/api"
	"subflux/internal/arrapi"
)

// resolveItems queries Sonarr and Radarr to find media items matching the
// search criteria. Sonarr is tried first for series/episodes, then Radarr
// for movies if no items were found.
func resolveItems(ctx context.Context, cfg api.ConfigProvider,
	imdbID, tmdbID, title string,
	seasonFilter, episodeFilter int) []searchItem {

	resolveCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	var queried bool
	var items []searchItem
	if sc := cfg.SonarrConfig(); sc.URL != "" {
		queried = true
		sonarr, err := arrapi.NewClient(sc.URL, sc.APIKey)
		if err != nil {
			slog.Warn("invalid sonarr config", "error", err)
		} else {
			items = resolveFromSonarr(resolveCtx, sonarr, imdbID, title, seasonFilter, episodeFilter)
		}
	}
	if len(items) == 0 && seasonFilter == 0 && episodeFilter == 0 {
		if rc := cfg.RadarrConfig(); rc.URL != "" {
			queried = true
			radarr, err := arrapi.NewClient(rc.URL, rc.APIKey)
			if err != nil {
				slog.Warn("invalid radarr config", "error", err)
			} else {
				items = resolveFromRadarr(resolveCtx, radarr, imdbID, tmdbID, title)
			}
		}
	}
	if !queried {
		slog.Warn("neither sonarr nor radarr configured, cannot resolve media")
	}
	if resolveCtx.Err() != nil && len(items) == 0 {
		slog.Warn("arr lookup timed out", "error", resolveCtx.Err())
	}
	return items
}

func resolveFromSonarr(ctx context.Context, client *arrapi.Client,
	imdbID, title string,
	seasonFilter, episodeFilter int) []searchItem {

	var items []searchItem
	allSeries, err := client.GetSeries(ctx)
	if err != nil {
		slog.Debug("sonarr lookup failed", "error", err)
		return items
	}
	for i := range allSeries {
		if !matchSeries(&allSeries[i], imdbID, title) {
			continue
		}
		episodes, epErr := client.GetEpisodes(ctx, allSeries[i].ID)
		if epErr != nil {
			slog.Debug("sonarr episode fetch failed", "series", allSeries[i].Title, "error", epErr)
			continue
		}
		items = episodesForSeries(&allSeries[i], episodes, seasonFilter, episodeFilter)
		break
	}
	return items
}

func matchSeries(s *api.Series, imdbID, title string) bool {
	return (imdbID != "" && s.ImdbID == imdbID) ||
		(title != "" && strings.EqualFold(s.Title, title))
}

func episodesForSeries(series *api.Series, episodes []api.Episode,
	seasonFilter, episodeFilter int) []searchItem {

	var items []searchItem
	for _, ep := range episodes {
		if !ep.HasFile || ep.EpisodeFile == nil {
			continue
		}
		if seasonFilter > 0 && ep.SeasonNumber != seasonFilter {
			continue
		}
		if episodeFilter > 0 && ep.EpisodeNumber != episodeFilter {
			continue
		}
		items = append(items, searchItem{
			Title: series.Title, Year: series.Year, ImdbID: series.ImdbID,
			TvdbID: series.TvdbID,
			Season: ep.SeasonNumber, Episode: ep.EpisodeNumber,
			MediaType: api.MediaTypeEpisode, SceneName: ep.EpisodeFile.SceneName,
			FilePath: ep.EpisodeFile.Path,
		})
	}
	return items
}

func resolveFromRadarr(ctx context.Context, client *arrapi.Client,
	imdbID, tmdbID, title string) []searchItem {

	tmdbInt := parseTmdbID(tmdbID)
	allMovies, err := client.GetMovies(ctx)
	if err != nil {
		slog.Debug("radarr lookup failed", "error", err)
		return nil
	}
	return filterRadarrMovies(allMovies, imdbID, tmdbInt, title)
}

func parseTmdbID(tmdbID string) int {
	n, err := strconv.Atoi(tmdbID)
	if err != nil {
		return 0
	}
	return n
}

func matchMovie(m *api.Movie, imdbID string, tmdbInt int, title string) bool {
	return (imdbID != "" && m.ImdbID == imdbID) ||
		(tmdbInt > 0 && m.TmdbID == tmdbInt) ||
		(title != "" && strings.EqualFold(m.Title, title))
}

func filterRadarrMovies(movies []api.Movie, imdbID string, tmdbInt int, title string) []searchItem {
	for i := range movies {
		m := &movies[i]
		if !matchMovie(m, imdbID, tmdbInt, title) || !m.HasFile || m.MovieFile == nil {
			continue
		}
		return []searchItem{{
			Title: m.Title, Year: m.Year, ImdbID: m.ImdbID,
			TmdbID:    m.TmdbID,
			MediaType: api.MediaTypeMovie, SceneName: m.MovieFile.SceneName,
			FilePath: m.MovieFile.Path,
		}}
	}
	return nil
}
