// Package coveragehandlers provides HTTP handlers for the /api/coverage/* endpoints.
package coveragehandlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/coverage"
	"golang.org/x/sync/errgroup"
)

var (
	errFetchSeries = errors.New("fetch series from arr")
	errFetchMovies = errors.New("fetch movies from arr")
)

// CoverageStore documents the api.Store methods used by coverage handlers.
type CoverageStore interface {
	GetSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.SubtitleEntry, error)
	GetScanStates(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.ScanStateRow, error)
}

// Compile-time assertion: api.Store satisfies CoverageStore.
var _ CoverageStore = api.Store(nil)

// ArrClient is the narrow interface for arr API calls needed by coverage handlers.
type ArrClient interface {
	GetSeries(ctx context.Context) ([]api.Series, error)
	GetMovies(ctx context.Context) ([]api.Movie, error)
	ResolveExcludeTagIDs(ctx context.Context, tags []string, includeAll bool) map[int]struct{}
}

// Compile-time assertion: api.ArrClient satisfies ArrClient.
var _ ArrClient = api.ArrClient(nil)

// Deps holds the dependencies for coverage handlers.
type Deps struct {
	Store     CoverageStore
	StateFunc func() *LiveState
}

// LiveState holds the runtime state needed by coverage handlers.
type LiveState struct {
	Cfg    api.ConfigProvider
	Sonarr ArrClient // nil when sonarr not configured
	Radarr ArrClient // nil when radarr not configured
}

// Handler provides HTTP handlers for the /api/coverage/* endpoints.
type Handler struct {
	deps Deps
}

// NewHandler creates a coverage Handler with the given dependencies.
func NewHandler(deps Deps) *Handler {
	return &Handler{deps: deps}
}

// SeriesItem is the coverage summary for one TV series.
type SeriesItem struct {
	Title      string                    `json:"title"`
	ImdbID     string                    `json:"imdb_id,omitempty"`
	FirstAired string                    `json:"first_aired,omitempty"`
	AudioLang  string                    `json:"audio_lang"`
	Rule       string                    `json:"rule"`
	Targets    []coverage.TargetCoverage `json:"targets"`
	Tags       []int                     `json:"tags,omitempty"`
	ID         int                       `json:"id"`
	Year       int                       `json:"year"`
	TvdbID     int                       `json:"tvdb_id"`
	Episodes   int                       `json:"episodes"`
	Excluded   bool                      `json:"excluded,omitempty"`
}

// MovieItem is the coverage summary for one movie.
type MovieItem struct {
	Title          string                    `json:"title"`
	ImdbID         string                    `json:"imdb_id,omitempty"`
	Path           string                    `json:"path,omitempty"`
	SceneName      string                    `json:"scene_name,omitempty"`
	InCinemas      string                    `json:"in_cinemas,omitempty"`
	DigitalRelease string                    `json:"digital_release,omitempty"`
	AudioLang      string                    `json:"audio_lang"`
	Rule           string                    `json:"rule"`
	Targets        []coverage.TargetCoverage `json:"targets"`
	Subs           []api.SubtitleEntry       `json:"subs"`
	Tags           []int                     `json:"tags,omitempty"`
	TmdbID         int                       `json:"tmdb_id"`
	ID             int                       `json:"id"`
	Year           int                       `json:"year"`
	HasFile        bool                      `json:"has_file"`
	Excluded       bool                      `json:"excluded,omitempty"`
}

// HandleCoverageSeries returns subtitle coverage for all TV series.
// GET /api/coverage/series
func (h *Handler) HandleCoverageSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	ls := h.deps.StateFunc()
	if ls.Sonarr == nil {
		api.WriteJSON(w, []SeriesItem{})
		return
	}

	allSeries, excludeIDs, allFiles, err := h.fetchCoverageSeriesData(ctx, ls)
	if err != nil {
		if errors.Is(err, errFetchSeries) {
			slog.Error("coverage: failed to fetch series", "error", err)
			api.BadGatewayC(w, r, api.CodeBadGateway, "failed to fetch series")
		} else {
			api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "coverage series files")
		}
		return
	}

	ignoredCodecs := search.IgnoredCodecsFromConfig(ls.Cfg)
	episodeSubs := coverage.IndexSubStatus(allFiles, ignoredCodecs)
	grouped := groupEpisodeSubsBySeries(allSeries, episodeSubs)

	out := make([]SeriesItem, 0, len(allSeries))
	for i := range allSeries {
		ser := &allSeries[i]
		epCount := 0
		if ser.Statistics != nil {
			epCount = ser.Statistics.EpisodeFileCount
		}
		if epCount == 0 {
			continue
		}

		audioLang := ser.OriginalLangCode()
		targets := ls.Cfg.ResolveTargetsWithFallback(audioLang, nil)
		ruleName := coverage.ResolveRuleName(audioLang, targets)

		tCov := coverage.CountEpisodeCoverageGrouped(grouped[i], targets, epCount)

		out = append(out, SeriesItem{
			ID:         ser.ID,
			Title:      ser.Title,
			Year:       ser.Year,
			TvdbID:     ser.TvdbID,
			ImdbID:     ser.ImdbID,
			FirstAired: ser.FirstAired,
			Episodes:   epCount,
			AudioLang:  audioLang,
			Rule:       ruleName,
			Targets:    tCov,
			Tags:       ser.Tags,
			Excluded:   api.HasExcludeTag(ser.Tags, excludeIDs),
		})
	}
	slog.Debug("coverage: series computed", "count", len(out), "series_total", len(allSeries), "episode_files", len(allFiles))
	api.WriteJSON(w, out)
}

// groupEpisodeSubsBySeries buckets indexed episode subtitle maps by their
// owning series, returning a slice parallel to allSeries. Episode media IDs
// whose series prefix doesn't match any series are dropped.
func groupEpisodeSubsBySeries(allSeries []api.Series, episodeSubs map[string]map[coverage.Key]*coverage.Status) [][]map[coverage.Key]*coverage.Status {
	prefixToIdx := make(map[string]int, len(allSeries))
	for i := range allSeries {
		p := api.BuildSeriesPrefix(allSeries[i].TvdbID, allSeries[i].ImdbID)
		if p != "" {
			prefixToIdx[p] = i
		}
	}
	grouped := make([][]map[coverage.Key]*coverage.Status, len(allSeries))
	for epMediaID, subs := range episodeSubs {
		p := coverage.ExtractSeriesPrefix(epMediaID)
		if idx, ok := prefixToIdx[p]; ok {
			grouped[idx] = append(grouped[idx], subs)
		}
	}
	return grouped
}

// HandleCoverageMovies returns subtitle coverage for all movies.
// GET /api/coverage/movies
func (h *Handler) HandleCoverageMovies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	ls := h.deps.StateFunc()
	if ls.Radarr == nil {
		api.WriteJSON(w, []MovieItem{})
		return
	}

	allMovies, excludeIDs, allFiles, err := h.fetchCoverageMoviesData(ctx, ls)
	if err != nil {
		if errors.Is(err, errFetchMovies) {
			slog.Error("coverage: failed to fetch movies", "error", err)
			api.BadGatewayC(w, r, api.CodeBadGateway, "failed to fetch movies")
		} else {
			api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "coverage movies files")
		}
		return
	}

	ignoredCodecs := search.IgnoredCodecsFromConfig(ls.Cfg)
	movieSubs := coverage.IndexSubStatus(allFiles, ignoredCodecs)
	movieFiles := make(map[string][]api.SubtitleEntry)
	for i := range allFiles {
		movieFiles[allFiles[i].MediaID] = append(movieFiles[allFiles[i].MediaID], allFiles[i])
	}

	out := make([]MovieItem, 0, len(allMovies))
	for i := range allMovies {
		m := &allMovies[i]
		if !m.HasFile {
			continue
		}

		audioLang := m.OriginalLangCode()
		targets := ls.Cfg.ResolveTargetsWithFallback(audioLang, nil)
		ruleName := coverage.ResolveRuleName(audioLang, targets)

		mediaID := api.BuildMovieID(m.TmdbID, m.ImdbID)
		if mediaID == "" {
			continue
		}
		tCov := coverage.CountMovieCoverage(movieSubs[mediaID], targets)

		var filePath, sceneName string
		if m.MovieFile != nil {
			filePath = m.MovieFile.Path
			sceneName = m.MovieFile.SceneName
		}

		out = append(out, MovieItem{
			ID:             m.ID,
			Title:          m.Title,
			Year:           m.Year,
			TmdbID:         m.TmdbID,
			ImdbID:         m.ImdbID,
			InCinemas:      m.InCinemas,
			DigitalRelease: m.DigitalRelease,
			HasFile:        m.HasFile,
			Path:           filePath,
			SceneName:      sceneName,
			AudioLang:      audioLang,
			Rule:           ruleName,
			Targets:        tCov,
			Subs:           coverage.DeduplicateFileRows(movieFiles[mediaID]),
			Tags:           m.Tags,
			Excluded:       api.HasExcludeTag(m.Tags, excludeIDs),
		})
	}
	slog.Debug("coverage: movies computed", "count", len(out), "movie_total", len(allMovies), "movie_files", len(allFiles))
	api.WriteJSON(w, out)
}

// HandleCoverageDetail returns per-episode subtitle files for a series.
// GET /api/coverage/series/{tvdbId}
func (h *Handler) HandleCoverageDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}

	tvdbStr := extractPathSegment(r.URL.Path, "/api/coverage/series/", "")
	if tvdbStr == "" {
		api.BadRequestC(w, r, api.CodeBadRequest, "missing tvdb id")
		return
	}
	if tvdbID, err := strconv.Atoi(tvdbStr); err != nil || tvdbID <= 0 {
		api.BadRequestC(w, r, api.CodeBadRequest, "invalid tvdb id")
		return
	}

	prefix := "tvdb-" + tvdbStr + "-"
	files, err := h.deps.Store.GetSubtitleFiles(ctx, api.MediaTypeEpisode, prefix)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "coverage detail")
		return
	}
	api.WriteJSON(w, coverage.DeduplicateFileRows(files))
}

// HandleScanStates returns scan timestamps for all scanned media items.
// GET /api/coverage/scan-state?type=episode&prefix=tvdb-81189-
func (h *Handler) HandleScanStates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	mediaType := r.URL.Query().Get("type")
	prefix := r.URL.Query().Get("prefix")
	if mediaType == "" {
		mediaType = string(api.MediaTypeEpisode)
	}
	if mediaType != string(api.MediaTypeEpisode) && mediaType != string(api.MediaTypeMovie) {
		api.BadRequestC(w, r, api.CodeQueryInvalidFilter, "invalid type parameter")
		return
	}
	states, err := h.deps.Store.GetScanStates(ctx, api.MediaType(mediaType), prefix)
	if err != nil {
		api.InternalErrorC(w, r, err, api.CodeInternalError, "query", "scan states")
		return
	}
	api.WriteJSON(w, states)
}

// fetchCoverageSeriesData fetches series, exclude tags, and subtitle files concurrently.
//
//nolint:dupl // type-specific wrappers around shared fetchCoverageData
func (h *Handler) fetchCoverageSeriesData(ctx context.Context, ls *LiveState) ([]api.Series, map[int]struct{}, []api.SubtitleEntry, error) {
	var allSeries []api.Series
	excludeIDs, allFiles, err := h.fetchCoverageData(ctx, ls.Sonarr, api.MediaTypeEpisode, ls.Cfg.Search().ExcludeArrTags, func(gctx context.Context) error {
		var ferr error
		allSeries, ferr = ls.Sonarr.GetSeries(gctx)
		if ferr != nil {
			return fmt.Errorf("%w: %w", errFetchSeries, ferr)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return allSeries, excludeIDs, allFiles, nil
}

// fetchCoverageMoviesData fetches movies, exclude tags, and subtitle files concurrently.
//
//nolint:dupl // type-specific wrappers around shared fetchCoverageData
func (h *Handler) fetchCoverageMoviesData(ctx context.Context, ls *LiveState) ([]api.Movie, map[int]struct{}, []api.SubtitleEntry, error) {
	var allMovies []api.Movie
	excludeIDs, allFiles, err := h.fetchCoverageData(ctx, ls.Radarr, api.MediaTypeMovie, ls.Cfg.Search().ExcludeArrTags, func(gctx context.Context) error {
		var ferr error
		allMovies, ferr = ls.Radarr.GetMovies(gctx)
		if ferr != nil {
			return fmt.Errorf("%w: %w", errFetchMovies, ferr)
		}
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	return allMovies, excludeIDs, allFiles, nil
}

// fetchCoverageData is the shared concurrent fetch pattern for coverage handlers.
func (h *Handler) fetchCoverageData(ctx context.Context, client ArrClient, mediaType api.MediaType, excludeTags []string, fetchMedia func(context.Context) error) (map[int]struct{}, []api.SubtitleEntry, error) {
	var (
		excludeIDs map[int]struct{}
		allFiles   []api.SubtitleEntry
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return fetchMedia(gctx)
	})
	g.Go(func() error {
		excludeIDs = client.ResolveExcludeTagIDs(gctx, excludeTags, false)
		return nil
	})
	g.Go(func() error {
		var err error
		allFiles, err = h.deps.Store.GetSubtitleFiles(gctx, mediaType, "")
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	return excludeIDs, allFiles, nil
}

// extractPathSegment extracts the segment between prefix and suffix in a URL path.
func extractPathSegment(path, prefix, suffix string) string {
	if len(path) <= len(prefix) || path[:len(prefix)] != prefix {
		return ""
	}
	rest := path[len(prefix):]
	if suffix != "" {
		for i := 0; i < len(rest); i++ {
			if rest[i:] == suffix || (len(rest[i:]) >= len(suffix) && rest[i:i+len(suffix)] == suffix) {
				rest = rest[:i]
				break
			}
		}
	}
	return rest
}
