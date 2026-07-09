// Package mediahandlers provides HTTP handlers for the /api/media/* endpoints.
package mediahandlers

import (
	"cmp"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
	"golang.org/x/sync/singleflight"
)

// MediaSonarrClient is the Sonarr surface the media browser uses.
type MediaSonarrClient interface {
	GetSeries(ctx context.Context) ([]arrapi.Series, error)
	GetEpisodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error)
}

// MediaRadarrClient is the Radarr surface the media browser uses.
type MediaRadarrClient interface {
	GetMovies(ctx context.Context) ([]arrapi.Movie, error)
}

// Compile-time assertions: the arrapi-backed role clients satisfy the media
// browser surfaces.
var (
	_ MediaSonarrClient = api.SonarrClient(nil)
	_ MediaRadarrClient = api.RadarrClient(nil)
)

// Deps holds the dependencies for media handlers.
type Deps struct {
	StateFunc func() *LiveState
	// ServerCtx returns the server-level context (outlives individual requests).
	// Used for singleflight closures so that a cancelled request doesn't
	// abort a shared fetch that other callers are waiting on.
	ServerCtx func() context.Context
}

// LiveState holds the runtime state needed by media handlers.
type LiveState struct {
	Cfg    api.ConfigProvider
	Sonarr MediaSonarrClient // nil when sonarr not configured
	Radarr MediaRadarrClient // nil when radarr not configured
}

// Handler provides HTTP handlers for the /api/media/* endpoints.
type Handler struct {
	deps    Deps
	mediaSF singleflight.Group
}

// NewHandler creates a media Handler with the given dependencies.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// SeriesItem is the JSON shape returned by GET /api/media/series.
type SeriesItem struct {
	Title    string `json:"title"`
	ImdbID   string `json:"imdb_id,omitempty"`
	ID       int    `json:"id"`
	Year     int    `json:"year"`
	TvdbID   int    `json:"tvdb_id"`
	Seasons  int    `json:"seasons"`
	Episodes int    `json:"episodes"`
}

// HandleMediaSeries returns all series from Sonarr for the media browser.
// GET /api/media/series
func (h *Handler) HandleMediaSeries(w http.ResponseWriter, r *http.Request) {
	ls := h.deps.StateFunc()
	if ls.Sonarr == nil {
		slog.Debug("media series: sonarr not configured")
		api.WriteJSON(w, []SeriesItem{})
		return
	}
	// Use server context for singleflight to avoid cancellation from a single
	// request aborting a shared fetch that other callers are waiting on.
	ctx := h.deps.ServerCtx()
	v, err, _ := h.mediaSF.Do("series", func() (any, error) {
		series, err := ls.Sonarr.GetSeries(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]SeriesItem, 0, len(series))
		for i := range series {
			item := SeriesItem{
				ID:     series[i].ID,
				Title:  series[i].Title,
				Year:   series[i].Year,
				TvdbID: series[i].TvdbID,
				ImdbID: series[i].ImdbID,
			}
			if series[i].Statistics != nil {
				item.Episodes = series[i].Statistics.EpisodeFileCount
				item.Seasons = series[i].Statistics.SeasonCount
			}
			out = append(out, item)
		}
		return out, nil
	})
	if err != nil {
		slog.Error("media browser: failed to fetch series", "error", err)
		api.BadGatewayC(w, r, api.CodeBadGateway, "failed to fetch series")
		return
	}
	api.WriteJSON(w, v)
}

// MovieItem is the JSON shape returned by GET /api/media/movies.
type MovieItem struct {
	Title     string `json:"title"`
	ImdbID    string `json:"imdb_id,omitempty"`
	Path      string `json:"path,omitempty"`
	SceneName string `json:"scene_name,omitempty"`
	ID        int    `json:"id"`
	Year      int    `json:"year"`
	TmdbID    int    `json:"tmdb_id"`
	HasFile   bool   `json:"has_file"`
}

// HandleMediaMovies returns all movies from Radarr for the media browser.
// GET /api/media/movies
func (h *Handler) HandleMediaMovies(w http.ResponseWriter, r *http.Request) {
	ls := h.deps.StateFunc()
	if ls.Radarr == nil {
		slog.Debug("media movies: radarr not configured")
		api.WriteJSON(w, []MovieItem{})
		return
	}
	// Use server context for singleflight — same rationale as HandleMediaSeries.
	ctx := h.deps.ServerCtx()
	v, err, _ := h.mediaSF.Do("movies", func() (any, error) {
		movies, err := ls.Radarr.GetMovies(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]MovieItem, 0, len(movies))
		for i := range movies {
			m := &movies[i]
			item := MovieItem{
				ID:      m.ID,
				Title:   m.Title,
				Year:    m.Year,
				TmdbID:  m.TmdbID,
				ImdbID:  m.ImdbID,
				HasFile: m.HasFile,
			}
			if m.MovieFile != nil {
				item.Path = m.MovieFile.Path
				item.SceneName = m.MovieFile.SceneName
			}
			out = append(out, item)
		}
		return out, nil
	})
	if err != nil {
		slog.Error("media browser: failed to fetch movies", "error", err)
		api.BadGatewayC(w, r, api.CodeBadGateway, "failed to fetch movies")
		return
	}
	api.WriteJSON(w, v)
}

// episodeItem is the JSON shape for a single episode.
type episodeItem struct {
	Title                 string `json:"title"`
	Path                  string `json:"path,omitempty"`
	SceneName             string `json:"scene_name,omitempty"`
	ID                    int    `json:"id"`
	SeasonNumber          int    `json:"season"`
	EpisodeNumber         int    `json:"episode"`
	SceneSeasonNumber     int    `json:"scene_season,omitempty"`
	SceneEpisodeNumber    int    `json:"scene_episode,omitempty"`
	AbsoluteEpisodeNumber int    `json:"absolute_episode,omitempty"`
	HasFile               bool   `json:"has_file"`
}

// SeasonGroup groups episodes by season number.
type SeasonGroup struct {
	Episodes []episodeItem `json:"episodes"`
	Season   int           `json:"season"`
}

// HandleMediaEpisodes returns episodes for a series, grouped by season.
// GET /api/media/series/{id}/episodes
func (h *Handler) HandleMediaEpisodes(w http.ResponseWriter, r *http.Request) {
	ls := h.deps.StateFunc()
	if ls.Sonarr == nil {
		api.BadRequestC(w, r, api.CodeBadRequest, "sonarr not configured")
		return
	}

	idStr := extractPathSegment(r.URL.Path, "/api/media/series/", "/episodes")
	if idStr == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "missing series id")
		return
	}
	seriesID, err := strconv.Atoi(idStr)
	if err != nil || seriesID <= 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid series id")
		return
	}

	episodes, err := ls.Sonarr.GetEpisodes(r.Context(), seriesID)
	if err != nil {
		slog.Error("media browser: failed to fetch episodes",
			"series_id", seriesID, "error", err)
		api.BadGatewayC(w, r, api.CodeBadGateway, "failed to fetch episodes")
		return
	}

	out := make([]episodeItem, 0, len(episodes))
	for _, ep := range episodes {
		item := episodeItem{
			ID:                    ep.ID,
			Title:                 ep.Title,
			SeasonNumber:          ep.SeasonNumber,
			EpisodeNumber:         ep.EpisodeNumber,
			SceneSeasonNumber:     ep.SceneSeasonNumber,
			SceneEpisodeNumber:    ep.SceneEpisodeNumber,
			AbsoluteEpisodeNumber: ep.AbsoluteEpisodeNumber,
			HasFile:               ep.HasFile,
		}
		if ep.EpisodeFile != nil {
			item.Path = ep.EpisodeFile.Path
			item.SceneName = ep.EpisodeFile.SceneName
		}
		out = append(out, item)
	}
	api.WriteJSON(w, groupEpisodesBySeason(out))
}

// groupEpisodesBySeason groups episodes by season number, sorted ascending.
func groupEpisodesBySeason(episodes []episodeItem) []SeasonGroup {
	seasonMap := make(map[int][]episodeItem)
	for _, ep := range episodes {
		seasonMap[ep.SeasonNumber] = append(seasonMap[ep.SeasonNumber], ep)
	}
	out := make([]SeasonGroup, 0, len(seasonMap))
	for sn := range seasonMap {
		eps := seasonMap[sn]
		slices.SortFunc(eps, func(a, b episodeItem) int {
			return cmp.Compare(a.EpisodeNumber, b.EpisodeNumber)
		})
		out = append(out, SeasonGroup{Season: sn, Episodes: eps})
	}
	slices.SortFunc(out, func(a, b SeasonGroup) int {
		return cmp.Compare(a.Season, b.Season)
	})
	return out
}

// extractPathSegment extracts the segment between prefix and suffix in a URL path.
func extractPathSegment(path, prefix, suffix string) string {
	if !strings.HasPrefix(path, prefix) {
		return ""
	}
	rest := path[len(prefix):]
	if suffix != "" {
		idx := strings.Index(rest, suffix)
		if idx < 0 {
			return ""
		}
		rest = rest[:idx]
	}
	return rest
}
