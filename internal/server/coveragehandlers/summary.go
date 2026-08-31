package coveragehandlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/httpapi"
	"github.com/cplieger/subflux/internal/mediaid"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/subflux"
)

// HandleCoverageSeriesSummary returns the coverage summary for ONE series,
// keyed by its TVDB id: the same row the collection serializes, resolved
// through the arr wrapper's tvdb→row index and one prefix-bounded
// subtitle_files scan. Exclusion parity (A2): 404 exactly where the
// collection omits. Honors ?recovery=1 by marking the request context for
// the arr-read wrapper.
// GET /api/coverage/series/{tvdbId}/summary
func (h *Handler) HandleCoverageSeriesSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}
	tvdbID, ok := positivePathID(w, r, "tvdbId", "tvdb id")
	if !ok {
		return
	}
	ctx := markRecovery(r)
	ls := h.deps.StateFunc()
	if ls.Sonarr == nil {
		httpapi.NotFoundC(w, r, subflux.CodeMediaNotFound, "unknown tvdb id")
		return
	}
	ser, found, err := ls.Sonarr.SeriesByTvdbID(ctx, tvdbID)
	if err != nil {
		writeArrReadError(w, r, err, "series")
		return
	}
	if !found || !seriesIncluded(&ser) {
		httpapi.NotFoundC(w, r, subflux.CodeMediaNotFound, "unknown tvdb id")
		return
	}
	excludeIDs, err := resolveExcludeTags(ctx, ls.Sonarr, ls.Cfg.Search().ExcludeArrTags)
	if err != nil {
		writeArrReadError(w, r, err, "series")
		return
	}
	prefix := mediaid.SeriesPrefix(ser.TvdbID, ser.ImdbID)
	files, err := h.deps.Store.SubtitleFiles(ctx, subflux.MediaTypeEpisode, prefix)
	if err != nil {
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "query", "series summary files")
		return
	}
	epSubs := seriesEpisodeSubs(files, prefix, search.IgnoredCodecsFromConfig(ls.Cfg))
	httpapi.WriteJSON(w, buildSeriesItem(ls.Cfg, &ser, epSubs, excludeIDs))
}

// HandleCoverageMovieSummary returns the coverage summary for ONE movie,
// keyed by its TMDB id, with no subtitle rows (/subs owns those). Exclusion
// parity (A2): 404 exactly where the collection omits. Honors ?recovery=1.
// GET /api/coverage/movies/{tmdbId}/summary
func (h *Handler) HandleCoverageMovieSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}
	tmdbID, ok := positivePathID(w, r, "tmdbId", "tmdb id")
	if !ok {
		return
	}
	ctx := markRecovery(r)
	ls := h.deps.StateFunc()
	if ls.Radarr == nil {
		httpapi.NotFoundC(w, r, subflux.CodeMediaNotFound, "unknown tmdb id")
		return
	}
	m, found, err := ls.Radarr.MovieByTmdbID(ctx, tmdbID)
	if err != nil {
		writeArrReadError(w, r, err, "movie")
		return
	}
	if !found || !movieIncluded(&m) {
		httpapi.NotFoundC(w, r, subflux.CodeMediaNotFound, "unknown tmdb id")
		return
	}
	excludeIDs, err := resolveExcludeTags(ctx, ls.Radarr, ls.Cfg.Search().ExcludeArrTags)
	if err != nil {
		writeArrReadError(w, r, err, "movie")
		return
	}
	mediaID := mediaid.Movie(m.TmdbID, m.ImdbID)
	rows, err := movieRows(ctx, h.deps.Store, mediaID)
	if err != nil {
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "query", "movie summary files")
		return
	}
	movieSubs := coverage.IndexSubStatus(rows, search.IgnoredCodecsFromConfig(ls.Cfg))
	httpapi.WriteJSON(w, buildMovieItem(ls.Cfg, &m, movieSubs[mediaID], excludeIDs))
}

// HandleCoverageMovieSubs returns one movie's subtitle rows. A STORE-ONLY
// read (A2): rows or an empty list, whatever the arrs would say about the id
// — an excluded or vanished movie still answers its rows — so it performs no
// arr read, has nothing to wave, and does not interpret ?recovery=1. 400
// only on a malformed id.
// GET /api/coverage/movies/{tmdbId}/subs
func (h *Handler) HandleCoverageMovieSubs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		httpapi.MethodNotAllowedC(w, r, subflux.CodeMethodNotAllowed)
		return
	}
	tmdbID, ok := positivePathID(w, r, "tmdbId", "tmdb id")
	if !ok {
		return
	}
	rows, err := movieRows(r.Context(), h.deps.Store, mediaid.Movie(tmdbID, ""))
	if err != nil {
		httpapi.InternalErrorC(w, r, err, subflux.CodeInternalError, "query", "movie subs")
		return
	}
	httpapi.WriteJSON(w, coverage.DeduplicateFileRows(rows))
}

// positivePathID parses one numeric {id} path wildcard. Malformed input —
// non-numeric, or non-positive, which no canonical id can be — answers 400;
// the caller owns the well-formed-but-unknown 404.
func positivePathID(w http.ResponseWriter, r *http.Request, name, label string) (int, bool) {
	id, err := strconv.Atoi(r.PathValue(name))
	if err != nil || id <= 0 {
		httpapi.BadRequestC(w, r, subflux.CodeBadRequest, "invalid "+label)
		return 0, false
	}
	return id, true
}

// markRecovery returns the request context, marked for wave admission when
// the request carries ?recovery=1. The two collections and the two summaries
// are this family's honoring endpoints; /subs never calls it.
func markRecovery(r *http.Request) context.Context {
	if r.URL.Query().Get("recovery") == "1" {
		return arrsvc.WithRecovery(r.Context())
	}
	return r.Context()
}

// seriesEpisodeSubs indexes a prefix-bounded scan's rows into the
// per-episode status maps buildSeriesItem counts, keeping exactly the rows
// the collection would group under this series (ExtractSeriesPrefix parity).
func seriesEpisodeSubs(files []subflux.SubtitleEntry, prefix string, ignoredCodecs map[string]bool) []map[coverage.Key]*coverage.Status {
	episodeSubs := coverage.IndexSubStatus(files, ignoredCodecs)
	out := make([]map[coverage.Key]*coverage.Status, 0, len(episodeSubs))
	for epID, subs := range episodeSubs {
		if coverage.ExtractSeriesPrefix(epID) == prefix {
			out = append(out, subs)
		}
	}
	return out
}

// movieRows reads one movie's subtitle rows: the store scan is bounded by
// the movie's own media id as the prefix, then filtered to exact matches —
// "tmdb-1" is a prefix of "tmdb-10", so the bound alone over-matches.
func movieRows(ctx context.Context, store CoverageStore, mediaID string) ([]subflux.SubtitleEntry, error) {
	files, err := store.SubtitleFiles(ctx, subflux.MediaTypeMovie, mediaID)
	if err != nil {
		return nil, err
	}
	var rows []subflux.SubtitleEntry
	for i := range files {
		if files[i].MediaID == mediaID {
			rows = append(rows, files[i])
		}
	}
	return rows, nil
}

// writeArrReadError maps an arr-read failure from the wrapper onto this
// family's wire statuses: the refusal sentinel answers 429 (a refusal to
// keep waiting, never a 500 through the generic arm; deliberately no
// Retry-After — the client's latch ladder is the retry policy), the ordered
// gate's unknown-series verdict answers 404, a client walk-away (only
// r.Context().Err() reports it) gets no error log and no write, and
// everything else — wave execution failures included — answers the family's
// upstream-failure 502.
func writeArrReadError(w http.ResponseWriter, r *http.Request, err error, what string) {
	switch {
	case errors.Is(err, arrsvc.ErrRecoveryRefused):
		httpapi.TooManyRequestsC(w, r, subflux.CodeRateLimited, "arr read refused, retry later")
	case errors.Is(err, arrsvc.ErrUnknownSeries):
		httpapi.NotFoundC(w, r, subflux.CodeMediaNotFound, "unknown series")
	case r.Context().Err() != nil:
	default:
		slog.Error("coverage: failed to fetch "+what, "error", err)
		httpapi.BadGatewayC(w, r, subflux.CodeBadGateway, "failed to fetch "+what)
	}
}
