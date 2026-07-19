package manualops

// resolve_query.go implements GET /api/search/resolve: mapping a user query
// (title / imdb / tmdb, optional type, optional season/episode narrowing)
// onto arr media items. It returns the API-wide (media_type, media_id)
// identity — the Sonarr series ID / Radarr movie ID every existing endpoint
// uses and S7's MediaRef standardizes — plus the stable search IDs the
// search leg forwards. No filesystem path and no new ID kind.
//
// The exact-match primitive (strings.EqualFold on titles, equality on
// stable IDs) moved here near-verbatim from the deleted internal/clisearch
// resolver (matchSeries / matchMovie); the selection rules around it are
// the spec-settled ones: stable IDs outrank title, contradictory
// identifiers answer 400 with a machine code, equal-titled matches answer
// a typed ambiguity result disambiguated by year, and the absent-type
// fallback preserves the deleted code's series-then-movie order — flipped
// movie-first when a TMDB id (a movie-only criterion) was supplied, so the
// supplied stable ID is evaluated before any series title match can win.

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
)

// resolveTimeout caps a resolve pass (full library listing + episode
// expansion), matching the deleted local resolver's budget.
const resolveTimeout = 2 * time.Minute

// Resolve `type` parameter vocabulary: which arm(s) to consult. Distinct
// from the search/download media_type vocabulary (movie|episode): a series
// resolves INTO episode items.
const (
	resolveTypeSeries = "series"
	resolveTypeMovie  = "movie"
)

// mediaTypeSeries labels series-level ambiguity candidates. The wire enum
// for MediaType includes "series" alongside movie/episode.
const mediaTypeSeries = api.MediaType("series")

// errResolveConflict marks contradictory identifiers (an ID resolving to
// item A while the title resolves to item B). Answered as 400 with the
// resolve_conflict machine code.
var errResolveConflict = errors.New("conflicting identifiers")

// ResolveSonarrClient is the Sonarr surface query resolution uses: the full
// series list for matching and per-series episodes for expansion.
type ResolveSonarrClient interface {
	GetSeries(ctx context.Context) ([]arrapi.Series, error)
	GetEpisodes(ctx context.Context, seriesID int) ([]arrapi.Episode, error)
}

// ResolveRadarrClient is the Radarr surface query resolution uses.
type ResolveRadarrClient interface {
	GetMovies(ctx context.Context) ([]arrapi.Movie, error)
}

// Compile-time assertions: the arrapi-backed role clients satisfy the
// resolve surfaces.
var (
	_ ResolveSonarrClient = api.SonarrClient(nil)
	_ ResolveRadarrClient = api.RadarrClient(nil)
)

// ResolveSearchIDs carries the stable identifiers of a resolved item for
// the follow-up search call.
type ResolveSearchIDs struct {
	Imdb string `json:"imdb,omitempty"`
	Tvdb int    `json:"tvdb,omitempty"`
	Tmdb int    `json:"tmdb,omitempty"`
}

// ResolvedItem is one searchable media item: an episode of the matched
// series (file-bearing only) or the matched movie. MediaID is the arr ID
// (Sonarr series ID / Radarr movie ID) — the same identity the search and
// download endpoints consume.
type ResolvedItem struct {
	MediaType api.MediaType    `json:"media_type"`
	Title     string           `json:"title"`
	SearchIDs ResolveSearchIDs `json:"search_ids"`
	MediaID   int              `json:"media_id"`
	Year      int              `json:"year,omitempty"`
	Season    int              `json:"season,omitempty"`
	Episode   int              `json:"episode,omitempty"`
}

// ResolveCandidate is one ambiguity candidate; Year disambiguates equal
// titles.
type ResolveCandidate struct {
	MediaType api.MediaType `json:"media_type"`
	Title     string        `json:"title"`
	MediaID   int           `json:"media_id"`
	Year      int           `json:"year,omitempty"`
}

// ResolveResponse is the typed result of GET /api/search/resolve. Exactly
// one of the following holds: Resolved with Items (success), Candidates
// (ambiguous — the client disambiguates), or all empty (no match).
type ResolveResponse struct {
	Items      []ResolvedItem     `json:"items,omitempty"`
	Candidates []ResolveCandidate `json:"candidates,omitempty"`
	Resolved   bool               `json:"resolved"`
}

// ResolveQueryParams is the validated query surface of the resolve
// endpoint. Season and Episode carry PRESENCE, not just value: nil means
// the parameter was absent, while a non-nil zero is an explicit season=0
// (specials) or episode=0 — an explicit zero narrows the expansion and
// marks the query episodic exactly like any other supplied value.
type ResolveQueryParams struct {
	Season  *int
	Episode *int
	Title   string
	Imdb    string
	Type    string // "", "series", or "movie"
	Tmdb    int
}

// parseResolveParams validates the raw query. errMsg is non-empty on a
// validation failure (the handler's 400).
func parseResolveParams(q url.Values) (p ResolveQueryParams, errMsg string) {
	p = ResolveQueryParams{
		Title: strings.TrimSpace(q.Get("title")),
		Imdb:  strings.TrimSpace(q.Get("imdb")),
		Type:  q.Get("type"),
	}
	if v := q.Get("tmdb"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			return p, "tmdb must be a positive integer"
		}
		p.Tmdb = n
	}
	if p.Title == "" && p.Imdb == "" && p.Tmdb == 0 {
		return p, "at least one of title, imdb, or tmdb is required"
	}
	if p.Type != "" && p.Type != resolveTypeSeries && p.Type != resolveTypeMovie {
		return p, "type must be series or movie"
	}
	if p.Season, errMsg = parseNarrowingParam(q, "season"); errMsg != "" {
		return p, errMsg
	}
	if p.Episode, errMsg = parseNarrowingParam(q, "episode"); errMsg != "" {
		return p, errMsg
	}
	return p, ""
}

// parseNarrowingParam parses an optional non-negative integer query
// parameter, preserving presence: an absent (or empty-valued) parameter is
// nil, while an explicit 0 — the specials season — is a supplied value the
// narrowing honors. errMsg is non-empty on a validation failure.
func parseNarrowingParam(q url.Values, name string) (val *int, errMsg string) {
	v := q.Get(name)
	if v == "" {
		return nil, ""
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return nil, name + " must be a non-negative integer"
	}
	return &n, ""
}

// HandleSearchResolve handles GET /api/search/resolve.
func (h *Handler) HandleSearchResolve(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		api.MethodNotAllowedC(w, r, api.CodeMethodNotAllowed)
		return
	}
	params, errMsg := parseResolveParams(r.URL.Query())
	if errMsg != "" {
		api.BadRequestC(w, r, api.CodeBadRequest, errMsg)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), resolveTimeout)
	defer cancel()

	resp, err := ResolveQuery(ctx, h.deps.StateFunc(), &params)
	switch {
	case errors.Is(err, errResolveConflict):
		api.BadRequestC(w, r, "resolve_conflict", err.Error())
	case err != nil:
		slog.Error("search resolve failed", "error", err,
			"title", params.Title, "imdb", params.Imdb, "tmdb", params.Tmdb, "type", params.Type)
		api.BadGatewayC(w, r, api.CodeArrUnreachable, "media resolution failed: "+err.Error())
	default:
		api.WriteJSON(w, resp)
	}
}

// ResolveQuery maps the validated query onto arr media items. With an
// explicit type only that arm runs (an unconfigured arr is then an error);
// with no type the deleted local resolver's fallback is preserved: series
// first, movies only when the series arm found nothing and no
// season/episode narrowing was requested — EXCEPT when a TMDB id was
// supplied, which is a movie-only criterion and therefore resolves the
// movie identity first (a supplied stable ID always outranks a title
// match). A conflict short-circuits; an arm failure surfaces only when the
// healthy arm could not satisfy the query (partial-arr rule).
func ResolveQuery(ctx context.Context, ls *LiveState, p *ResolveQueryParams) (ResolveResponse, error) {
	switch p.Type {
	case resolveTypeSeries:
		return resolveSeriesArm(ctx, ls.SonarrLib, p, true)
	case resolveTypeMovie:
		return resolveMovieArm(ctx, ls.RadarrLib, p, true)
	default:
		return resolveWithFallback(ctx, ls, p)
	}
}

// episodic reports whether season/episode narrowing was supplied. Presence,
// not value, marks the query episodic: an explicit season=0 (specials)
// narrows exactly like season=3.
func (p *ResolveQueryParams) episodic() bool {
	return p.Season != nil || p.Episode != nil
}

// resolveWithFallback runs the absent-type arm order: series first, movies
// only when the series arm found nothing and no season/episode narrowing
// was requested. Two refinements to that base order:
//
//   - Episodic narrowing (season and/or episode supplied, INCLUDING an
//     explicit season=0) is a series-only query: the movie arm never runs,
//     so a specials query can never resolve a movie.
//   - A supplied TMDB id without narrowing flips the order movie-first:
//     tmdb is not a series matching criterion, so the series arm would
//     otherwise answer from the TITLE alone and the wrong media could win
//     over the supplied stable movie id (IDs outrank titles).
func resolveWithFallback(ctx context.Context, ls *LiveState, p *ResolveQueryParams) (ResolveResponse, error) {
	if p.Tmdb > 0 && !p.episodic() {
		return resolveMovieFirst(ctx, ls, p)
	}
	seriesRes, seriesErr := resolveSeriesArm(ctx, ls.SonarrLib, p, false)
	if errors.Is(seriesErr, errResolveConflict) {
		return ResolveResponse{}, seriesErr
	}
	if seriesErr == nil && seriesRes.decisive() {
		return seriesRes, nil
	}
	if p.episodic() {
		// Season/episode narrowing is a series-only query; the deleted
		// resolver never fell through to movies in this case.
		return seriesRes, seriesErr
	}
	movieRes, movieErr := resolveMovieArm(ctx, ls.RadarrLib, p, false)
	if errors.Is(movieErr, errResolveConflict) {
		return ResolveResponse{}, movieErr
	}
	if movieErr == nil && movieRes.decisive() {
		return movieRes, nil
	}
	// Nothing matched. If an arm was down, its emptiness is unprovable:
	// the query may have needed exactly that arm, so the failure wins
	// over a misleading "no match".
	if seriesErr != nil {
		return ResolveResponse{}, seriesErr
	}
	return ResolveResponse{}, movieErr
}

// resolveMovieFirst is the absent-type order when a TMDB id was supplied:
// the movie arm evaluates the id BEFORE any series title match can win. A
// title supplied alongside the id is checked against the movie candidates
// inside the arm — title resolving to a different movie answers the
// resolve_conflict 400, while a title matching no movie never overrides
// the id (selectByIdentity's ID-precedence rule).
//
// When the movie arm finds nothing, the supplied id matched nothing, and a
// bare title must not rescue it via the series arm (mirroring the in-arm
// rule that an unmatched stable ID answers empty despite a title match).
// The series arm therefore runs only when imdb — a series-matchable stable
// ID — was also supplied.
func resolveMovieFirst(ctx context.Context, ls *LiveState, p *ResolveQueryParams) (ResolveResponse, error) {
	movieRes, movieErr := resolveMovieArm(ctx, ls.RadarrLib, p, false)
	if errors.Is(movieErr, errResolveConflict) {
		return ResolveResponse{}, movieErr
	}
	if movieErr == nil && movieRes.decisive() {
		return movieRes, nil
	}
	if p.Imdb == "" {
		return ResolveResponse{}, movieErr
	}
	seriesRes, seriesErr := resolveSeriesArm(ctx, ls.SonarrLib, p, false)
	if errors.Is(seriesErr, errResolveConflict) {
		return ResolveResponse{}, seriesErr
	}
	if seriesErr == nil && seriesRes.decisive() {
		return seriesRes, nil
	}
	// Nothing matched: a downed movie arm wins over a misleading
	// "no match" (the tmdb id needed exactly that arm).
	if movieErr != nil {
		return ResolveResponse{}, movieErr
	}
	return ResolveResponse{}, seriesErr
}

// decisive reports whether the arm produced an answer (items or an
// ambiguity) rather than an empty fall-through.
func (r *ResolveResponse) decisive() bool {
	return len(r.Items) > 0 || len(r.Candidates) > 0
}

// armCandidate is the arm-internal match unit before item expansion: the
// identity fields the precedence/conflict/ambiguity rules operate on.
type armCandidate struct {
	title string
	imdb  string
	id    int // arr ID
	tmdb  int // 0 in the series arm (not a series matching criterion)
	tvdb  int // 0 in the movie arm
	year  int
}

// resolveSeriesArm matches the query against the Sonarr library and expands
// the single match into its file-bearing episodes. required marks an
// explicit type=series query, for which an unconfigured Sonarr is an error
// rather than an empty fallback arm.
func resolveSeriesArm(ctx context.Context, sonarr ResolveSonarrClient, p *ResolveQueryParams, required bool) (ResolveResponse, error) {
	if sonarr == nil {
		if required {
			return ResolveResponse{}, errors.New("sonarr is not configured")
		}
		return ResolveResponse{}, nil
	}
	allSeries, err := sonarr.GetSeries(ctx)
	if err != nil {
		return ResolveResponse{}, fmt.Errorf("sonarr: %w", err)
	}
	cands := make([]armCandidate, 0, len(allSeries))
	for i := range allSeries {
		s := &allSeries[i]
		cands = append(cands, armCandidate{
			id: s.ID, title: s.Title, year: s.Year, imdb: s.ImdbID, tvdb: s.TvdbID,
		})
	}
	// tmdb is not a series matching criterion (matchSeries matched on imdb
	// and title only); a tmdb-only query therefore matches nothing here and
	// falls through to the movie arm.
	matched, err := selectByIdentity(cands, p.Imdb, 0, p.Title)
	if err != nil {
		return ResolveResponse{}, err
	}
	switch len(matched) {
	case 0:
		return ResolveResponse{}, nil
	case 1:
		return expandSeries(ctx, sonarr, &matched[0], p.Season, p.Episode)
	default:
		return ambiguous(matched, mediaTypeSeries), nil
	}
}

// expandSeries lists the matched series' file-bearing episodes, narrowed by
// the optional season/episode filters — the same expansion bound as the
// deleted resolver (library-bounded, no artificial cap). A nil filter means
// "no narrowing"; a supplied filter compares by value, so an explicit
// season=0 expands exactly the specials season (and an episode filter still
// applies within it).
func expandSeries(ctx context.Context, sonarr ResolveSonarrClient, s *armCandidate, seasonFilter, episodeFilter *int) (ResolveResponse, error) {
	episodes, err := sonarr.GetEpisodes(ctx, s.id)
	if err != nil {
		return ResolveResponse{}, fmt.Errorf("sonarr: episodes for series %d: %w", s.id, err)
	}
	var items []ResolvedItem
	for i := range episodes {
		ep := &episodes[i]
		if !ep.HasFile || ep.EpisodeFile == nil {
			continue
		}
		if seasonFilter != nil && ep.SeasonNumber != *seasonFilter {
			continue
		}
		if episodeFilter != nil && ep.EpisodeNumber != *episodeFilter {
			continue
		}
		items = append(items, ResolvedItem{
			MediaType: api.MediaTypeEpisode,
			MediaID:   s.id,
			Title:     s.title,
			Year:      s.year,
			Season:    ep.SeasonNumber,
			Episode:   ep.EpisodeNumber,
			SearchIDs: ResolveSearchIDs{Imdb: s.imdb, Tvdb: s.tvdb},
		})
	}
	return ResolveResponse{Resolved: len(items) > 0, Items: items}, nil
}

// resolveMovieArm matches the query against the Radarr library. Only
// file-bearing movies participate (the deleted resolver's first
// file-bearing match preserved as a pre-filter). required marks an explicit
// type=movie query.
func resolveMovieArm(ctx context.Context, radarr ResolveRadarrClient, p *ResolveQueryParams, required bool) (ResolveResponse, error) {
	if radarr == nil {
		if required {
			return ResolveResponse{}, errors.New("radarr is not configured")
		}
		return ResolveResponse{}, nil
	}
	movies, err := radarr.GetMovies(ctx)
	if err != nil {
		return ResolveResponse{}, fmt.Errorf("radarr: %w", err)
	}
	cands := make([]armCandidate, 0, len(movies))
	for i := range movies {
		m := &movies[i]
		if !m.HasFile || m.MovieFile == nil {
			continue
		}
		cands = append(cands, armCandidate{
			id: m.ID, title: m.Title, year: m.Year, imdb: m.ImdbID, tmdb: m.TmdbID,
		})
	}
	matched, err := selectByIdentity(cands, p.Imdb, p.Tmdb, p.Title)
	if err != nil {
		return ResolveResponse{}, err
	}
	switch len(matched) {
	case 0:
		return ResolveResponse{}, nil
	case 1:
		m := &matched[0]
		return ResolveResponse{
			Resolved: true,
			Items: []ResolvedItem{{
				MediaType: api.MediaTypeMovie,
				MediaID:   m.id,
				Title:     m.title,
				Year:      m.year,
				SearchIDs: ResolveSearchIDs{Imdb: m.imdb, Tmdb: m.tmdb},
			}},
		}, nil
	default:
		return ambiguous(matched, api.MediaTypeMovie), nil
	}
}

// selectByIdentity applies the settled selection rules to an arm's
// candidates: each supplied criterion (imdb, tmdb, title) is matched
// exactly (EqualFold for titles, equality for IDs); two supplied criteria
// that both matched but share no candidate are a contradiction (400); and
// when any stable ID was supplied the ID matches win — a title match is
// never used to override or rescue a supplied ID.
func selectByIdentity(cands []armCandidate, imdb string, tmdb int, title string) ([]armCandidate, error) {
	byImdb, byTmdb, byTitle := matchSets(cands, imdb, tmdb, title)

	var supplied []matchCriterion
	if imdb != "" {
		supplied = append(supplied, matchCriterion{"imdb", byImdb})
	}
	if tmdb > 0 {
		supplied = append(supplied, matchCriterion{"tmdb", byTmdb})
	}
	if title != "" {
		supplied = append(supplied, matchCriterion{"title", byTitle})
	}
	if err := checkCriterionConflicts(supplied); err != nil {
		return nil, err
	}

	if imdb != "" || tmdb > 0 {
		return unionByID(byImdb, byTmdb), nil
	}
	return byTitle, nil
}

// matchSets applies the exact-match primitive per supplied criterion:
// equality for stable IDs, EqualFold for titles (moved near-verbatim from
// the deleted clisearch matchSeries/matchMovie).
func matchSets(cands []armCandidate, imdb string, tmdb int, title string) (byImdb, byTmdb, byTitle []armCandidate) {
	for i := range cands {
		c := &cands[i]
		if imdb != "" && c.imdb == imdb {
			byImdb = append(byImdb, *c)
		}
		if tmdb > 0 && c.tmdb == tmdb {
			byTmdb = append(byTmdb, *c)
		}
		if title != "" && strings.EqualFold(c.title, title) {
			byTitle = append(byTitle, *c)
		}
	}
	return byImdb, byTmdb, byTitle
}

// matchCriterion pairs a supplied identifier with its match set for the
// pairwise contradiction check.
type matchCriterion struct {
	name    string
	matches []armCandidate
}

// checkCriterionConflicts errors when two supplied criteria both matched
// but share no candidate — the "ID resolves to A, title to B" 400 case.
func checkCriterionConflicts(supplied []matchCriterion) error {
	for i := range supplied {
		for j := i + 1; j < len(supplied); j++ {
			a, b := &supplied[i], &supplied[j]
			if len(a.matches) > 0 && len(b.matches) > 0 && !shareCandidate(a.matches, b.matches) {
				return fmt.Errorf("%w: %s and %s match different items",
					errResolveConflict, a.name, b.name)
			}
		}
	}
	return nil
}

// shareCandidate reports whether the two match sets have any arr ID in
// common.
func shareCandidate(a, b []armCandidate) bool {
	ids := make(map[int]struct{}, len(a))
	for i := range a {
		ids[a[i].id] = struct{}{}
	}
	for i := range b {
		if _, ok := ids[b[i].id]; ok {
			return true
		}
	}
	return false
}

// unionByID merges match sets, deduplicating by arr ID and preserving
// encounter order.
func unionByID(sets ...[]armCandidate) []armCandidate {
	var out []armCandidate
	seen := make(map[int]struct{})
	for _, set := range sets {
		for i := range set {
			if _, ok := seen[set[i].id]; ok {
				continue
			}
			seen[set[i].id] = struct{}{}
			out = append(out, set[i])
		}
	}
	return out
}

// ambiguous builds the typed ambiguity result: resolved=false plus the
// candidate list (year included so equal titles are tellable apart).
func ambiguous(matched []armCandidate, mt api.MediaType) ResolveResponse {
	out := make([]ResolveCandidate, len(matched))
	for i := range matched {
		out[i] = ResolveCandidate{
			MediaType: mt,
			MediaID:   matched[i].id,
			Title:     matched[i].title,
			Year:      matched[i].year,
		}
	}
	return ResolveResponse{Resolved: false, Candidates: out}
}
