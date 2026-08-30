// Package coveragehandlers provides HTTP handlers for the /api/coverage/* endpoints.
package coveragehandlers

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/errgroup"
)

var (
	errFetchSeries = errors.New("fetch series from arr")
	errFetchMovies = errors.New("fetch movies from arr")
)

// CoverageStore is the two reads the coverage endpoints answer from: the
// subtitle-file inventory and the scan-state rows. 2 of the 36 methods the
// store offers, and 2 of the 12 in the coverage family — these handlers report
// coverage, they never record it.
type CoverageStore interface {
	SubtitleFiles(ctx context.Context, mediaType subflux.MediaType, mediaIDPrefix string) ([]subflux.SubtitleEntry, error)
	ScanStates(ctx context.Context, mediaType subflux.MediaType, mediaIDPrefix string) ([]subflux.ScanStateRow, error)
}

// CoverageSonarrClient is the Sonarr surface coverage handlers use: the
// series list, the per-item tvdb→row index lookup the series summary answers
// from, the shipped fail-open exclude-tag resolution (plain reads), and the
// error-returning form a ?recovery=1 read needs — its wave failure or
// refusal must reach the wire typed, never as a silent empty-exclusion 200.
type CoverageSonarrClient interface {
	Series(ctx context.Context) ([]arrapi.Series, error)
	SeriesByTvdbID(ctx context.Context, tvdbID int) (arrapi.Series, bool, error)
	ResolveExcludeTagIDs(ctx context.Context, tags []string, logMissing bool) map[int]struct{}
	ResolveExcludeTagIDsErr(ctx context.Context, tags []string, logMissing bool) (map[int]struct{}, error)
}

// CoverageRadarrClient is the Radarr surface coverage handlers use; the four
// methods mirror CoverageSonarrClient's.
type CoverageRadarrClient interface {
	Movies(ctx context.Context) ([]arrapi.Movie, error)
	MovieByTmdbID(ctx context.Context, tmdbID int) (arrapi.Movie, bool, error)
	ResolveExcludeTagIDs(ctx context.Context, tags []string, logMissing bool) map[int]struct{}
	ResolveExcludeTagIDsErr(ctx context.Context, tags []string, logMissing bool) (map[int]struct{}, error)
}

// tagResolver is the exclude-tag surface the shared fetch paths need — the
// fail-open projection for plain reads and the error-returning form for
// marked ones; both role clients satisfy it.
type tagResolver interface {
	ResolveExcludeTagIDs(ctx context.Context, tags []string, logMissing bool) map[int]struct{}
	ResolveExcludeTagIDsErr(ctx context.Context, tags []string, logMissing bool) (map[int]struct{}, error)
}

// Deps holds the dependencies for coverage handlers.
type Deps struct {
	Store     CoverageStore
	StateFunc func() *LiveState
}

// coverageCfg is what the coverage endpoints read out of the configuration:
// the language targets each media item earns, the embedded-codec policy that
// decides which existing tracks count, and the arr exclude-tag list. 3 of the
// 37 values the config offers — these handlers report coverage against the
// language rules, so they ask nothing about providers, scoring or paths.
type coverageCfg interface {
	ResolveTargetsWithFallback(originalLang string, audioLangs []string) []subflux.SubtitleTarget
	EmbeddedPolicy() subflux.EmbeddedPolicy
	Search() subflux.SearchConfig
}

// LiveState holds the runtime state needed by coverage handlers.
type LiveState struct {
	Cfg    coverageCfg
	Sonarr CoverageSonarrClient // nil when sonarr not configured
	Radarr CoverageRadarrClient // nil when radarr not configured
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

// MovieItem is the coverage summary for one movie. It carries no file path
// (S7): clients address the video by MediaRef (id = arr ID) and the server
// resolves paths. Subtitle rows travel on /subs (A2/A3), never inline.
type MovieItem struct {
	Title          string                    `json:"title"`
	ImdbID         string                    `json:"imdb_id,omitempty"`
	SceneName      string                    `json:"scene_name,omitempty"`
	InCinemas      string                    `json:"in_cinemas,omitempty"`
	DigitalRelease string                    `json:"digital_release,omitempty"`
	AudioLang      string                    `json:"audio_lang"`
	Rule           string                    `json:"rule"`
	Targets        []coverage.TargetCoverage `json:"targets"`
	Tags           []int                     `json:"tags,omitempty"`
	TmdbID         int                       `json:"tmdb_id"`
	ID             int                       `json:"id"`
	Year           int                       `json:"year"`
	HasFile        bool                      `json:"has_file"`
	Excluded       bool                      `json:"excluded,omitempty"`
}

// HandleCoverageSeries returns subtitle coverage for all TV series.
// Honors ?recovery=1 by marking the request context for the arr-read wrapper.
// GET /api/coverage/series
func (h *Handler) HandleCoverageSeries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}
	ctx := markRecovery(r)
	ls := h.deps.StateFunc()
	if ls.Sonarr == nil {
		httpapi.WriteJSON(w, []SeriesItem{})
		return
	}

	allSeries, excludeIDs, allFiles, err := h.fetchCoverageSeriesData(ctx, ls)
	if err != nil {
		writeCollectionFetchError(w, r, err, errFetchSeries, "series")
		return
	}

	ignoredCodecs := search.IgnoredCodecsFromConfig(ls.Cfg)
	episodeSubs := coverage.IndexSubStatus(allFiles, ignoredCodecs)
	grouped := groupEpisodeSubsBySeries(allSeries, episodeSubs)

	out := make([]SeriesItem, 0, len(allSeries))
	for i := range allSeries {
		ser := &allSeries[i]
		if !seriesIncluded(ser) {
			continue
		}
		out = append(out, buildSeriesItem(ls.Cfg, ser, grouped[i], excludeIDs))
	}
	slog.Debug("coverage: series computed", "count", len(out), "series_total", len(allSeries), "episode_files", len(allFiles))
	httpapi.WriteJSON(w, out)
}

// seriesEpisodeCount returns the series' episode-file count (0 when arr sent
// no statistics).
func seriesEpisodeCount(ser *arrapi.Series) int {
	if ser.Statistics == nil {
		return 0
	}
	return ser.Statistics.EpisodeFileCount
}

// seriesIncluded is the series half of the exclusion-parity predicate shared
// by the collection and the per-item summary (A2): the summary 404s exactly
// where the collection omits — a series with zero episode files, or without a
// positive TVDB id, the canonical id the client keys rows by (every zero-id
// row would collide onto one "tvdb-0" key).
func seriesIncluded(ser *arrapi.Series) bool {
	return ser.TvdbID > 0 && seriesEpisodeCount(ser) > 0
}

// buildSeriesItem assembles one SeriesItem — the one construction site the
// collection and the per-item summary share, so the summary stays deep-equal
// to the collection's row.
func buildSeriesItem(cfg coverageCfg, ser *arrapi.Series, epSubs []map[coverage.Key]*coverage.Status, excludeIDs map[int]struct{}) SeriesItem {
	epCount := seriesEpisodeCount(ser)
	audioLang := arrsvc.OriginalLangCode(ser.OriginalLanguage)
	targets := cfg.ResolveTargetsWithFallback(audioLang, nil)
	return SeriesItem{
		ID:         ser.ID,
		Title:      ser.Title,
		Year:       ser.Year,
		TvdbID:     ser.TvdbID,
		ImdbID:     ser.ImdbID,
		FirstAired: ser.FirstAired,
		Episodes:   epCount,
		AudioLang:  audioLang,
		Rule:       coverage.ResolveRuleName(audioLang, targets),
		Targets:    coverage.CountEpisodesGrouped(epSubs, targets, epCount),
		Tags:       ser.Tags,
		Excluded:   arrsvc.HasExcludeTag(ser.Tags, excludeIDs),
	}
}

// groupEpisodeSubsBySeries buckets indexed episode subtitle maps by their
// owning series, returning a slice parallel to allSeries. Episode media IDs
// whose series prefix doesn't match any series are dropped.
func groupEpisodeSubsBySeries(allSeries []arrapi.Series, episodeSubs map[string]map[coverage.Key]*coverage.Status) [][]map[coverage.Key]*coverage.Status {
	prefixToIdx := make(map[string]int, len(allSeries))
	for i := range allSeries {
		p := mediaid.SeriesPrefix(allSeries[i].TvdbID, allSeries[i].ImdbID)
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
// Honors ?recovery=1 by marking the request context for the arr-read wrapper.
// GET /api/coverage/movies
func (h *Handler) HandleCoverageMovies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}
	ctx := markRecovery(r)
	ls := h.deps.StateFunc()
	if ls.Radarr == nil {
		httpapi.WriteJSON(w, []MovieItem{})
		return
	}

	allMovies, excludeIDs, allFiles, err := h.fetchCoverageMoviesData(ctx, ls)
	if err != nil {
		writeCollectionFetchError(w, r, err, errFetchMovies, "movies")
		return
	}

	ignoredCodecs := search.IgnoredCodecsFromConfig(ls.Cfg)
	movieSubs := coverage.IndexSubStatus(allFiles, ignoredCodecs)

	out := make([]MovieItem, 0, len(allMovies))
	for i := range allMovies {
		m := &allMovies[i]
		if !movieIncluded(m) {
			continue
		}
		mediaID := mediaid.Movie(m.TmdbID, m.ImdbID)
		out = append(out, buildMovieItem(ls.Cfg, m, movieSubs[mediaID], excludeIDs))
	}
	slog.Debug("coverage: movies computed", "count", len(out), "movie_total", len(allMovies), "movie_files", len(allFiles))
	httpapi.WriteJSON(w, out)
}

// movieIncluded is the movie half of the exclusion-parity predicate shared by
// the collection and the per-item summary (A2): the collection omits
// file-less movies and rows without a positive TMDB id — the canonical id the
// client keys rows by and the summary routes on, so an imdb-only row is
// unaddressable and a zero id would collide onto "tmdb-0".
func movieIncluded(m *arrapi.Movie) bool {
	return m.HasFile && m.TmdbID > 0
}

// buildMovieItem assembles one MovieItem — the one construction site the
// collection and the per-item summary share, so the summary stays deep-equal
// to the collection's row. Subtitle rows are never inlined; /subs owns them.
func buildMovieItem(cfg coverageCfg, m *arrapi.Movie, subs map[coverage.Key]*coverage.Status, excludeIDs map[int]struct{}) MovieItem {
	audioLang := arrsvc.OriginalLangCode(m.OriginalLanguage)
	targets := cfg.ResolveTargetsWithFallback(audioLang, nil)
	var sceneName string
	if m.MovieFile != nil {
		sceneName = m.MovieFile.SceneName
	}
	return MovieItem{
		ID:             m.ID,
		Title:          m.Title,
		Year:           m.Year,
		TmdbID:         m.TmdbID,
		ImdbID:         m.ImdbID,
		InCinemas:      m.InCinemas,
		DigitalRelease: m.DigitalRelease,
		HasFile:        m.HasFile,
		SceneName:      sceneName,
		AudioLang:      audioLang,
		Rule:           coverage.ResolveRuleName(audioLang, targets),
		Targets:        coverage.CountMovies(subs, targets),
		Tags:           m.Tags,
		Excluded:       arrsvc.HasExcludeTag(m.Tags, excludeIDs),
	}
}

// HandleCoverageDetail returns per-episode subtitle files for a series.
// GET /api/coverage/series/{tvdbId}
func (h *Handler) HandleCoverageDetail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}

	tvdbStr := extractPathSegment(r.URL.Path, "/api/coverage/series/", "")
	if tvdbStr == "" {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "missing tvdb id")
		return
	}
	if tvdbID, err := strconv.Atoi(tvdbStr); err != nil || tvdbID <= 0 {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid tvdb id")
		return
	}

	prefix := "tvdb-" + tvdbStr + "-"
	files, err := h.deps.Store.SubtitleFiles(ctx, subflux.MediaTypeEpisode, prefix)
	if err != nil {
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "query", "coverage detail")
		return
	}
	httpapi.WriteJSON(w, coverage.DeduplicateFileRows(files))
}

// HandleScanStates returns scan timestamps for all scanned media items.
// GET /api/coverage/scan-state?type=episode&prefix=tvdb-81189-
func (h *Handler) HandleScanStates(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}
	mediaType := r.URL.Query().Get("type")
	prefix := r.URL.Query().Get("prefix")
	if mediaType == "" {
		mediaType = string(subflux.MediaTypeEpisode)
	}
	if mediaType != string(subflux.MediaTypeEpisode) && mediaType != string(subflux.MediaTypeMovie) {
		httpapi.BadRequestC(w, r, subflux.CodeQueryInvalidFilter, "invalid type parameter")
		return
	}
	states, err := h.deps.Store.ScanStates(ctx, subflux.MediaType(mediaType), prefix)
	if err != nil {
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "query", "scan states")
		return
	}
	httpapi.WriteJSON(w, states)
}

// fetchCoverageSeriesData fetches series, exclude tags, and subtitle files concurrently.
func (h *Handler) fetchCoverageSeriesData(ctx context.Context, ls *LiveState) (allSeries []arrapi.Series, excludeIDs map[int]struct{}, allFiles []subflux.SubtitleEntry, err error) {
	excludeIDs, allFiles, err = h.fetchCoverageData(ctx, ls.Sonarr, subflux.MediaTypeEpisode, ls.Cfg.Search().ExcludeArrTags, func(gctx context.Context) error {
		var ferr error
		allSeries, ferr = ls.Sonarr.Series(gctx)
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
func (h *Handler) fetchCoverageMoviesData(ctx context.Context, ls *LiveState) (allMovies []arrapi.Movie, excludeIDs map[int]struct{}, allFiles []subflux.SubtitleEntry, err error) {
	excludeIDs, allFiles, err = h.fetchCoverageData(ctx, ls.Radarr, subflux.MediaTypeMovie, ls.Cfg.Search().ExcludeArrTags, func(gctx context.Context) error {
		var ferr error
		allMovies, ferr = ls.Radarr.Movies(gctx)
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
func (h *Handler) fetchCoverageData(ctx context.Context, client tagResolver, mediaType subflux.MediaType, excludeTags []string, fetchMedia func(context.Context) error) (map[int]struct{}, []subflux.SubtitleEntry, error) {
	var (
		excludeIDs map[int]struct{}
		allFiles   []subflux.SubtitleEntry
	)

	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error {
		return fetchMedia(gctx)
	})
	g.Go(func() error {
		var terr error
		excludeIDs, terr = resolveExcludeTags(gctx, client, excludeTags)
		return terr
	})
	g.Go(func() error {
		var err error
		allFiles, err = h.deps.Store.SubtitleFiles(gctx, mediaType, "")
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, nil, err
	}
	return excludeIDs, allFiles, nil
}

// resolveExcludeTags resolves the exclude-tag set for one coverage read: a
// marked read propagates the wrapper's typed failure (a refusal or wave
// failure must never become a silent empty-exclusion 200), a plain read keeps
// the fail-open projection.
func resolveExcludeTags(ctx context.Context, client tagResolver, tags []string) (map[int]struct{}, error) {
	if arrsvc.RecoveryMarked(ctx) {
		return client.ResolveExcludeTagIDsErr(ctx, tags, false)
	}
	return client.ResolveExcludeTagIDs(ctx, tags, false), nil
}

// writeCollectionFetchError maps a collection fetch failure onto the wire the
// way the summaries do (A3): the wrapper's refusal sentinel answers 429 (a
// refusal to keep waiting, never a 500; deliberately no Retry-After — the
// client's latch ladder is the retry policy), an arr read failure — a marked
// exclude-tag leg's wave failure included — answers the family's
// upstream-failure 502, and a store failure keeps the generic 500 arm.
func writeCollectionFetchError(w http.ResponseWriter, r *http.Request, err error, fetchErr error, what string) {
	switch {
	case errors.Is(err, arrsvc.ErrRecoveryRefused):
		httpapi.TooManyRequestsC(w, r, subflux.CodeRateLimited, "arr read refused, retry later")
	case errors.Is(err, fetchErr), errors.Is(err, arrsvc.ErrRecoveryFailed):
		slog.Error("coverage: failed to fetch "+what, "error", err)
		httpapi.BadGatewayC(w, r, subflux.CodeBadGateway, "failed to fetch "+what)
	default:
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "query", "coverage "+what+" files")
	}
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
