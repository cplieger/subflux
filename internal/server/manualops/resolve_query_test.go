package manualops

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cplieger/arrapi"
	"github.com/cplieger/subflux/internal/api"
)

// --- fakes ---

// resolveFakeSonarr serves a fixed series list and per-series episodes.
type resolveFakeSonarr struct {
	series    []arrapi.Series
	episodes  map[int][]arrapi.Episode
	seriesErr error
	epErr     error
}

func (f *resolveFakeSonarr) GetSeries(context.Context) ([]arrapi.Series, error) {
	return f.series, f.seriesErr
}

func (f *resolveFakeSonarr) GetEpisodes(_ context.Context, seriesID int) ([]arrapi.Episode, error) {
	if f.epErr != nil {
		return nil, f.epErr
	}
	return f.episodes[seriesID], nil
}

// resolveFakeRadarr serves a fixed movie list.
type resolveFakeRadarr struct {
	movies []arrapi.Movie
	err    error
}

func (f *resolveFakeRadarr) GetMovies(context.Context) ([]arrapi.Movie, error) {
	return f.movies, f.err
}

// epFile returns a file-bearing episode.
func epFile(season, episode int) arrapi.Episode {
	return arrapi.Episode{
		SeasonNumber:  season,
		EpisodeNumber: episode,
		HasFile:       true,
		EpisodeFile:   &arrapi.EpisodeFile{Path: "/media/tv/ep.mkv", SceneName: "Ep.Scene"},
	}
}

// movieWithFile returns a file-bearing movie.
func movieWithFile(id int, title string, year, tmdb int, imdb string) arrapi.Movie {
	return arrapi.Movie{
		ID: id, Title: title, Year: year, TmdbID: tmdb, ImdbID: imdb,
		HasFile:   true,
		MovieFile: &arrapi.MovieFile{Path: "/media/movies/m.mkv"},
	}
}

func resolveLS(sonarr ResolveSonarrClient, radarr ResolveRadarrClient) *LiveState {
	ls := &LiveState{}
	// Assign only non-nil concretes: a nil *resolveFakeSonarr stored in the
	// interface would defeat the nil checks (typed-nil trap).
	if sonarr != nil {
		ls.SonarrLib = sonarr
	}
	if radarr != nil {
		ls.RadarrLib = radarr
	}
	return ls
}

// --- parseResolveParams ---

func TestParseResolveParams(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		query   string
		wantMsg string
	}{
		{name: "title alone is enough", query: "title=Breaking+Bad"},
		{name: "imdb alone is enough", query: "imdb=tt0903747"},
		{name: "tmdb alone is enough", query: "tmdb=550"},
		{name: "no identifier", query: "type=movie", wantMsg: "at least one of title, imdb, or tmdb is required"},
		{name: "whitespace-only title is absent", query: "title=%20%20", wantMsg: "at least one of title, imdb, or tmdb is required"},
		{name: "bad type", query: "title=x&type=album", wantMsg: "type must be series or movie"},
		{name: "episode type is not a resolve arm", query: "title=x&type=episode", wantMsg: "type must be series or movie"},
		{name: "garbage tmdb", query: "tmdb=abc", wantMsg: "tmdb must be a positive integer"},
		{name: "negative tmdb", query: "tmdb=-5", wantMsg: "tmdb must be a positive integer"},
		{name: "garbage season", query: "title=x&season=abc", wantMsg: "season must be a non-negative integer"},
		{name: "negative episode", query: "title=x&episode=-1", wantMsg: "episode must be a non-negative integer"},
		{name: "valid narrowing", query: "title=x&season=2&episode=5"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			q, err := url.ParseQuery(c.query)
			if err != nil {
				t.Fatalf("bad test query: %v", err)
			}
			_, msg := parseResolveParams(q)
			if msg != c.wantMsg {
				t.Errorf("parseResolveParams(%q) msg = %q, want %q", c.query, msg, c.wantMsg)
			}
		})
	}
}

// Season/episode parsing must preserve PRESENCE: an explicit season=0 (the
// specials season) is a supplied narrowing value, distinct from an absent
// or empty parameter.
func TestParseResolveParams_season_zero_presence(t *testing.T) {
	t.Parallel()
	cases := []struct {
		wantSeason  *int
		wantEpisode *int
		name        string
		query       string
	}{
		{name: "absent params are nil", query: "title=x"},
		{name: "explicit season zero is present", query: "title=x&season=0", wantSeason: new(0)},
		{name: "explicit episode zero is present", query: "title=x&episode=0", wantEpisode: new(0)},
		{name: "empty value counts as absent", query: "title=x&season="},
		{name: "season zero with episode", query: "title=x&season=0&episode=2", wantSeason: new(0), wantEpisode: new(2)},
	}
	checkPresence := func(t *testing.T, field string, got, want *int) {
		t.Helper()
		switch {
		case want == nil && got != nil:
			t.Errorf("%s = %d, want absent (nil)", field, *got)
		case want != nil && got == nil:
			t.Errorf("%s = nil, want %d (presence lost)", field, *want)
		case want != nil && got != nil && *got != *want:
			t.Errorf("%s = %d, want %d", field, *got, *want)
		}
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			q, err := url.ParseQuery(c.query)
			if err != nil {
				t.Fatalf("bad test query: %v", err)
			}
			p, msg := parseResolveParams(q)
			if msg != "" {
				t.Fatalf("parseResolveParams(%q) msg = %q, want valid", c.query, msg)
			}
			checkPresence(t, "Season", p.Season, c.wantSeason)
			checkPresence(t, "Episode", p.Episode, c.wantEpisode)
		})
	}
}

// --- ResolveQuery: movie arm ---

func TestResolveQuery_movie_by_title(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Fight Club", 1999, 550, "tt0137523"),
	}}
	res, err := ResolveQuery(context.Background(), resolveLS(nil, radarr),
		&ResolveQueryParams{Title: "fight club", Type: resolveTypeMovie})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if !res.Resolved || len(res.Items) != 1 {
		t.Fatalf("ResolveQuery() = %+v, want one resolved item", res)
	}
	item := res.Items[0]
	if item.MediaType != api.MediaTypeMovie || item.MediaID != 7 {
		t.Errorf("item identity = (%s, %d), want (movie, 7)", item.MediaType, item.MediaID)
	}
	if item.SearchIDs.Imdb != "tt0137523" || item.SearchIDs.Tmdb != 550 {
		t.Errorf("item search ids = %+v, want imdb tt0137523 + tmdb 550", item.SearchIDs)
	}
	if item.Year != 1999 {
		t.Errorf("item year = %d, want 1999", item.Year)
	}
}

func TestResolveQuery_movie_fileless_is_invisible(t *testing.T) {
	t.Parallel()
	fileless := arrapi.Movie{ID: 8, Title: "Fight Club", Year: 1999, HasFile: false}
	withFile := movieWithFile(9, "Fight Club", 2026, 999, "tt999")
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{fileless, withFile}}

	// The fileless equal-titled movie neither resolves nor triggers
	// ambiguity: only file-bearing movies participate (preserving the old
	// resolver's first file-bearing match).
	res, err := ResolveQuery(context.Background(), resolveLS(nil, radarr),
		&ResolveQueryParams{Title: "Fight Club", Type: resolveTypeMovie})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if !res.Resolved || len(res.Items) != 1 || res.Items[0].MediaID != 9 {
		t.Fatalf("ResolveQuery() = %+v, want the single file-bearing movie (id 9)", res)
	}
}

func TestResolveQuery_movie_year_disambiguation(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(1, "Dune", 1984, 841, "tt0087182"),
		movieWithFile(2, "Dune", 2021, 438631, "tt1160419"),
	}}
	res, err := ResolveQuery(context.Background(), resolveLS(nil, radarr),
		&ResolveQueryParams{Title: "Dune", Type: resolveTypeMovie})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if res.Resolved || len(res.Items) != 0 {
		t.Fatalf("ResolveQuery(equal titles) = %+v, want unresolved ambiguity", res)
	}
	if len(res.Candidates) != 2 {
		t.Fatalf("candidates = %d, want 2", len(res.Candidates))
	}
	years := map[int]bool{res.Candidates[0].Year: true, res.Candidates[1].Year: true}
	if !years[1984] || !years[2021] {
		t.Errorf("candidate years = %+v, want 1984 and 2021 (year disambiguates)", res.Candidates)
	}
}

func TestResolveQuery_id_precedence_over_title(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(1, "Dune", 1984, 841, "tt0087182"),
		movieWithFile(2, "Dune", 2021, 438631, "tt1160419"),
	}}
	// Title alone is ambiguous; the stable ID picks one.
	res, err := ResolveQuery(context.Background(), resolveLS(nil, radarr),
		&ResolveQueryParams{Title: "Dune", Tmdb: 438631, Type: resolveTypeMovie})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if !res.Resolved || len(res.Items) != 1 || res.Items[0].MediaID != 2 {
		t.Fatalf("ResolveQuery(title+tmdb) = %+v, want the tmdb-picked movie (id 2)", res)
	}
}

func TestResolveQuery_unmatched_id_is_empty_despite_title_match(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(1, "Dune", 1984, 841, "tt0087182"),
	}}
	// A supplied stable ID that matches nothing is authoritative: the title
	// match does not rescue the query.
	res, err := ResolveQuery(context.Background(), resolveLS(nil, radarr),
		&ResolveQueryParams{Title: "Dune", Tmdb: 999999, Type: resolveTypeMovie})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if res.Resolved || len(res.Items) != 0 || len(res.Candidates) != 0 {
		t.Fatalf("ResolveQuery(unmatched id) = %+v, want empty", res)
	}
}

func TestResolveQuery_conflicting_identifiers(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(1, "Dune", 1984, 841, "tt0087182"),
		movieWithFile(2, "Alien", 1979, 348, "tt0078748"),
	}}
	// tmdb resolves to Dune, title to Alien: contradiction.
	_, err := ResolveQuery(context.Background(), resolveLS(nil, radarr),
		&ResolveQueryParams{Title: "Alien", Tmdb: 841, Type: resolveTypeMovie})
	if !errors.Is(err, errResolveConflict) {
		t.Fatalf("ResolveQuery(conflict) error = %v, want errResolveConflict", err)
	}
}

func TestResolveQuery_two_ids_conflicting(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(1, "Dune", 1984, 841, "tt0087182"),
		movieWithFile(2, "Alien", 1979, 348, "tt0078748"),
	}}
	_, err := ResolveQuery(context.Background(), resolveLS(nil, radarr),
		&ResolveQueryParams{Imdb: "tt0078748", Tmdb: 841, Type: resolveTypeMovie})
	if !errors.Is(err, errResolveConflict) {
		t.Fatalf("ResolveQuery(imdb vs tmdb) error = %v, want errResolveConflict", err)
	}
}

// --- ResolveQuery: series arm ---

func testSeries() []arrapi.Series {
	return []arrapi.Series{
		{ID: 11, Title: "Breaking Bad", Year: 2008, ImdbID: "tt0903747", TvdbID: 81189},
		{ID: 12, Title: "The Wire", Year: 2002, ImdbID: "tt0306414", TvdbID: 79126},
	}
}

func TestResolveQuery_series_expands_file_bearing_episodes(t *testing.T) {
	t.Parallel()
	noFile := arrapi.Episode{SeasonNumber: 1, EpisodeNumber: 3, HasFile: false}
	sonarr := &resolveFakeSonarr{
		series: testSeries(),
		episodes: map[int][]arrapi.Episode{
			11: {epFile(1, 1), epFile(1, 2), noFile, epFile(2, 1)},
		},
	}
	res, err := ResolveQuery(context.Background(), resolveLS(sonarr, nil),
		&ResolveQueryParams{Title: "breaking bad", Type: resolveTypeSeries})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if !res.Resolved || len(res.Items) != 3 {
		t.Fatalf("ResolveQuery() = %+v, want 3 file-bearing episode items", res)
	}
	for i := range res.Items {
		item := &res.Items[i]
		if item.MediaType != api.MediaTypeEpisode || item.MediaID != 11 {
			t.Errorf("item[%d] identity = (%s, %d), want (episode, 11)", i, item.MediaType, item.MediaID)
		}
		if item.SearchIDs.Tvdb != 81189 || item.SearchIDs.Imdb != "tt0903747" {
			t.Errorf("item[%d] search ids = %+v, want tvdb 81189 + imdb", i, item.SearchIDs)
		}
	}
}

func TestResolveQuery_series_season_episode_narrowing(t *testing.T) {
	t.Parallel()
	// Season 0 is the specials season: its episodes participate in the
	// unnarrowed expansion and an explicit season=0 narrows to exactly it.
	sonarr := &resolveFakeSonarr{
		series: testSeries(),
		episodes: map[int][]arrapi.Episode{
			11: {epFile(0, 1), epFile(0, 2), epFile(1, 1), epFile(1, 2), epFile(2, 1), epFile(2, 2)},
		},
	}
	cases := []struct {
		season  *int
		episode *int
		name    string
		want    int
	}{
		{name: "no narrowing expands everything", want: 6},
		{name: "season only", season: new(2), want: 2},
		{name: "season and episode", season: new(1), episode: new(2), want: 1},
		{name: "episode only filters across seasons", episode: new(1), want: 3},
		{name: "no such season", season: new(9), want: 0},
		{name: "season zero expands only the specials", season: new(0), want: 2},
		{name: "episode narrows within season zero", season: new(0), episode: new(2), want: 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res, err := ResolveQuery(context.Background(), resolveLS(sonarr, nil),
				&ResolveQueryParams{
					Title: "Breaking Bad", Type: resolveTypeSeries,
					Season: c.season, Episode: c.episode,
				})
			if err != nil {
				t.Fatalf("ResolveQuery() error = %v, want nil", err)
			}
			if len(res.Items) != c.want {
				t.Errorf("items = %d, want %d", len(res.Items), c.want)
			}
			for i := range res.Items {
				item := &res.Items[i]
				if c.season != nil && item.Season != *c.season {
					t.Errorf("item[%d].Season = %d, want %d", i, item.Season, *c.season)
				}
				if c.episode != nil && item.Episode != *c.episode {
					t.Errorf("item[%d].Episode = %d, want %d", i, item.Episode, *c.episode)
				}
			}
		})
	}
}

// --- ResolveQuery: type fallback (series then movie) ---

func TestResolveQuery_type_fallback_series_first(t *testing.T) {
	t.Parallel()
	sonarr := &resolveFakeSonarr{
		series:   testSeries(),
		episodes: map[int][]arrapi.Episode{11: {epFile(1, 1)}},
	}
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Breaking Bad", 2008, 999, "ttmovie"),
	}}
	// A title present in BOTH libraries resolves to the series (series arm
	// runs first, matching the deleted resolver's order).
	res, err := ResolveQuery(context.Background(), resolveLS(sonarr, radarr),
		&ResolveQueryParams{Title: "Breaking Bad"})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if len(res.Items) != 1 || res.Items[0].MediaType != api.MediaTypeEpisode {
		t.Fatalf("ResolveQuery() = %+v, want the series expansion to win", res)
	}
}

func TestResolveQuery_type_fallback_to_movie(t *testing.T) {
	t.Parallel()
	sonarr := &resolveFakeSonarr{series: testSeries()}
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Fight Club", 1999, 550, "tt0137523"),
	}}
	res, err := ResolveQuery(context.Background(), resolveLS(sonarr, radarr),
		&ResolveQueryParams{Title: "Fight Club"})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if len(res.Items) != 1 || res.Items[0].MediaType != api.MediaTypeMovie {
		t.Fatalf("ResolveQuery() = %+v, want the movie arm to satisfy the fallback", res)
	}
}

func TestResolveQuery_narrowing_suppresses_movie_fallback(t *testing.T) {
	t.Parallel()
	sonarr := &resolveFakeSonarr{series: testSeries()}
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Fight Club", 1999, 550, "tt0137523"),
	}}
	// Season/episode narrowing is a series-only query: the movie arm never
	// runs even though it would match. Season 0 (specials) counts as
	// narrowing — presence, not value, marks the query episodic.
	cases := []struct {
		season  *int
		episode *int
		name    string
	}{
		{name: "season one", season: new(1)},
		{name: "season zero", season: new(0)},
		{name: "episode zero", episode: new(0)},
		{name: "season zero episode two", season: new(0), episode: new(2)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			res, err := ResolveQuery(context.Background(), resolveLS(sonarr, radarr),
				&ResolveQueryParams{Title: "Fight Club", Season: c.season, Episode: c.episode})
			if err != nil {
				t.Fatalf("ResolveQuery() error = %v, want nil", err)
			}
			if len(res.Items) != 0 || len(res.Candidates) != 0 {
				t.Fatalf("ResolveQuery(narrowed) = %+v, want empty (no movie fallback)", res)
			}
		})
	}
}

// A specials query must never fall through to the movie arm even when the
// tmdb-supplied movie-first order would otherwise apply: narrowing marks
// the query episodic and episodic queries are series-only.
func TestResolveQuery_season_zero_with_tmdb_stays_series_only(t *testing.T) {
	t.Parallel()
	sonarr := &resolveFakeSonarr{
		series:   testSeries(),
		episodes: map[int][]arrapi.Episode{11: {epFile(0, 1), epFile(1, 1)}},
	}
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Fight Club", 1999, 550, "tt0137523"),
	}}
	// The title matches the series; season 0 narrows to its specials. The
	// tmdb id (which would resolve the movie) must not divert an episodic
	// query to the movie arm.
	res, err := ResolveQuery(context.Background(), resolveLS(sonarr, radarr),
		&ResolveQueryParams{Title: "Breaking Bad", Tmdb: 550, Season: new(0)})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if len(res.Items) != 1 || res.Items[0].MediaType != api.MediaTypeEpisode || res.Items[0].Season != 0 {
		t.Fatalf("ResolveQuery(tmdb + season 0) = %+v, want the single specials episode", res)
	}
}

// countingSonarr wraps the sonarr fake and counts GetSeries calls, so tests
// can assert whether the series arm was consulted at all.
type countingSonarr struct {
	resolveFakeSonarr

	calls atomic.Int32
}

func (c *countingSonarr) GetSeries(ctx context.Context) ([]arrapi.Series, error) {
	c.calls.Add(1)
	return c.resolveFakeSonarr.GetSeries(ctx)
}

// A tmdb-only query resolves the movie identity FIRST: tmdb is a movie-only
// criterion, so the movie arm answers before the series arm is consulted at
// all (the pre-fix order ran the series arm first).
func TestResolveQuery_tmdb_only_resolves_movie_first(t *testing.T) {
	t.Parallel()
	sonarr := &countingSonarr{resolveFakeSonarr: resolveFakeSonarr{series: testSeries()}}
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Fight Club", 1999, 550, "tt0137523"),
	}}
	res, err := ResolveQuery(context.Background(), resolveLS(sonarr, radarr),
		&ResolveQueryParams{Tmdb: 550})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if len(res.Items) != 1 || res.Items[0].MediaID != 7 {
		t.Fatalf("ResolveQuery(tmdb only) = %+v, want the movie", res)
	}
	if got := sonarr.calls.Load(); got != 0 {
		t.Errorf("sonarr consulted %d times, want 0 (movie arm resolves the tmdb id first)", got)
	}
}

// A supplied stable movie id outranks a series TITLE match (R2.3): the
// pre-fix series-first order let the series title win and the wrong media
// could be searched or downloaded. The title matches no movie, so it never
// overrides the id inside the arm either.
func TestResolveQuery_tmdb_outranks_conflicting_series_title(t *testing.T) {
	t.Parallel()
	sonarr := &resolveFakeSonarr{
		series:   testSeries(),
		episodes: map[int][]arrapi.Episode{11: {epFile(1, 1)}},
	}
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Fight Club", 1999, 550, "tt0137523"),
	}}
	res, err := ResolveQuery(context.Background(), resolveLS(sonarr, radarr),
		&ResolveQueryParams{Tmdb: 550, Title: "Breaking Bad"})
	if err != nil {
		t.Fatalf("ResolveQuery() error = %v, want nil", err)
	}
	if len(res.Items) != 1 || res.Items[0].MediaType != api.MediaTypeMovie || res.Items[0].MediaID != 7 {
		t.Fatalf("ResolveQuery(tmdb + series title) = %+v, want the tmdb-resolved movie, not the series", res)
	}
}

// When the title resolves to a DIFFERENT movie than the supplied tmdb id,
// the absent-type path answers the resolve_conflict 400 exactly like the
// explicit-type arm does.
func TestResolveQuery_tmdb_title_conflict_without_type(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(1, "Dune", 1984, 841, "tt0087182"),
		movieWithFile(2, "Alien", 1979, 348, "tt0078748"),
	}}
	_, err := ResolveQuery(context.Background(), resolveLS(nil, radarr),
		&ResolveQueryParams{Tmdb: 841, Title: "Alien"})
	if !errors.Is(err, errResolveConflict) {
		t.Fatalf("ResolveQuery(no type, tmdb vs title) error = %v, want errResolveConflict", err)
	}
}

// An unmatched supplied tmdb id is authoritative across arms: a bare title
// must not rescue it via the series arm (mirroring the in-arm rule that an
// unmatched stable ID answers empty despite a title match). imdb — a
// series-matchable stable ID — still reaches the series arm.
func TestResolveQuery_unmatched_tmdb_never_title_rescued(t *testing.T) {
	t.Parallel()
	sonarr := &resolveFakeSonarr{
		series:   testSeries(),
		episodes: map[int][]arrapi.Episode{11: {epFile(1, 1)}},
	}
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Fight Club", 1999, 550, "tt0137523"),
	}}

	// tmdb matches nothing + title matches a series: empty, not the series.
	res, err := ResolveQuery(context.Background(), resolveLS(sonarr, radarr),
		&ResolveQueryParams{Tmdb: 999999, Title: "Breaking Bad"})
	if err != nil {
		t.Fatalf("ResolveQuery(unmatched tmdb + title) error = %v, want nil", err)
	}
	if res.Resolved || len(res.Items) != 0 || len(res.Candidates) != 0 {
		t.Fatalf("ResolveQuery(unmatched tmdb + title) = %+v, want empty (title never rescues an id)", res)
	}

	// tmdb matches nothing but imdb matches the series: the series arm
	// still answers by the matched stable ID.
	res, err = ResolveQuery(context.Background(), resolveLS(sonarr, radarr),
		&ResolveQueryParams{Tmdb: 999999, Imdb: "tt0903747"})
	if err != nil {
		t.Fatalf("ResolveQuery(unmatched tmdb + imdb) error = %v, want nil", err)
	}
	if len(res.Items) != 1 || res.Items[0].MediaType != api.MediaTypeEpisode || res.Items[0].MediaID != 11 {
		t.Fatalf("ResolveQuery(unmatched tmdb + imdb) = %+v, want the imdb-matched series expansion", res)
	}
}

// --- ResolveQuery: partial arr failure + unconfigured arms ---

func TestResolveQuery_partial_arr_failure(t *testing.T) {
	t.Parallel()
	downSonarr := &resolveFakeSonarr{seriesErr: errors.New("sonarr boom")}
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(7, "Fight Club", 1999, 550, "tt0137523"),
	}}

	// Healthy arm satisfied the query: the downed arm is not an error.
	res, err := ResolveQuery(context.Background(), resolveLS(downSonarr, radarr),
		&ResolveQueryParams{Title: "Fight Club"})
	if err != nil {
		t.Fatalf("ResolveQuery(healthy arm satisfied) error = %v, want nil", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("ResolveQuery() = %+v, want the movie", res)
	}

	// Nothing matched and an arm was down: the emptiness is unprovable, so
	// the failure surfaces.
	_, err = ResolveQuery(context.Background(), resolveLS(downSonarr, radarr),
		&ResolveQueryParams{Title: "Unknown Title"})
	if err == nil || !strings.Contains(err.Error(), "sonarr") {
		t.Fatalf("ResolveQuery(down arm, no match) error = %v, want the sonarr failure", err)
	}
}

func TestResolveQuery_explicit_type_unconfigured_arm_errors(t *testing.T) {
	t.Parallel()
	_, err := ResolveQuery(context.Background(), resolveLS(nil, nil),
		&ResolveQueryParams{Title: "x", Type: resolveTypeSeries})
	if err == nil || !strings.Contains(err.Error(), "sonarr is not configured") {
		t.Fatalf("ResolveQuery(type=series, no sonarr) error = %v, want not-configured error", err)
	}
	_, err = ResolveQuery(context.Background(), resolveLS(nil, nil),
		&ResolveQueryParams{Title: "x", Type: resolveTypeMovie})
	if err == nil || !strings.Contains(err.Error(), "radarr is not configured") {
		t.Fatalf("ResolveQuery(type=movie, no radarr) error = %v, want not-configured error", err)
	}
}

func TestResolveQuery_unconfigured_arms_fallback_empty(t *testing.T) {
	t.Parallel()
	// No type + neither arr configured: empty result, not an error (the
	// deleted resolver skipped unconfigured arrs silently).
	res, err := ResolveQuery(context.Background(), resolveLS(nil, nil),
		&ResolveQueryParams{Title: "x"})
	if err != nil {
		t.Fatalf("ResolveQuery(unconfigured) error = %v, want nil", err)
	}
	if res.Resolved || len(res.Items) != 0 || len(res.Candidates) != 0 {
		t.Fatalf("ResolveQuery(unconfigured) = %+v, want empty", res)
	}
}

// --- HandleSearchResolve (HTTP mapping) ---

// resolveHarness builds a Handler whose LiveState carries the given arr
// fakes; only the resolve endpoint's dependencies are populated.
func resolveHarness(sonarr ResolveSonarrClient, radarr ResolveRadarrClient) *Handler {
	ls := resolveLS(sonarr, radarr)
	return NewHandler(HandlerDeps{
		StateFunc: func() *LiveState { return ls },
	})
}

func TestHandleSearchResolve_rejects_non_get(t *testing.T) {
	t.Parallel()
	h := resolveHarness(nil, nil)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodPost, "/api/search/resolve", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleSearchResolve(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("HandleSearchResolve(POST) status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleSearchResolve_missing_identifiers_400(t *testing.T) {
	t.Parallel()
	h := resolveHarness(nil, nil)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/resolve?type=movie", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleSearchResolve(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("HandleSearchResolve(no identifiers) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleSearchResolve_conflict_400_with_machine_code(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(1, "Dune", 1984, 841, "tt0087182"),
		movieWithFile(2, "Alien", 1979, 348, "tt0078748"),
	}}
	h := resolveHarness(nil, radarr)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/resolve?type=movie&title=Alien&tmdb=841", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleSearchResolve(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("HandleSearchResolve(conflict) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
	if !strings.Contains(rec.Body.String(), "resolve_conflict") {
		t.Errorf("HandleSearchResolve(conflict) body = %q, want the resolve_conflict machine code", rec.Body.String())
	}
}

func TestHandleSearchResolve_arr_failure_502(t *testing.T) {
	t.Parallel()
	h := resolveHarness(&resolveFakeSonarr{seriesErr: errors.New("boom")}, nil)
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/resolve?type=series&title=x", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleSearchResolve(rec, req)
	if rec.Code != http.StatusBadGateway {
		t.Errorf("HandleSearchResolve(arr down) status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
}

func TestHandleSearchResolve_happy_and_ambiguous_200(t *testing.T) {
	t.Parallel()
	radarr := &resolveFakeRadarr{movies: []arrapi.Movie{
		movieWithFile(1, "Dune", 1984, 841, "tt0087182"),
		movieWithFile(2, "Dune", 2021, 438631, "tt1160419"),
		movieWithFile(3, "Alien", 1979, 348, "tt0078748"),
	}}
	h := resolveHarness(nil, radarr)

	// Happy: unique title.
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/resolve?type=movie&title=Alien", http.NoBody)
	rec := httptest.NewRecorder()
	h.HandleSearchResolve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSearchResolve(happy) status = %d, want 200", rec.Code)
	}
	var happy ResolveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &happy); err != nil {
		t.Fatalf("decode happy response: %v", err)
	}
	if !happy.Resolved || len(happy.Items) != 1 || happy.Items[0].MediaID != 3 {
		t.Fatalf("happy response = %+v, want the Alien item", happy)
	}

	// Ambiguous: equal titles answer 200 with a typed candidates result.
	req = httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/resolve?type=movie&title=Dune", http.NoBody)
	rec = httptest.NewRecorder()
	h.HandleSearchResolve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSearchResolve(ambiguous) status = %d, want 200", rec.Code)
	}
	var amb ResolveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &amb); err != nil {
		t.Fatalf("decode ambiguous response: %v", err)
	}
	if amb.Resolved || len(amb.Candidates) != 2 {
		t.Fatalf("ambiguous response = %+v, want 2 candidates", amb)
	}

	// Empty: unknown title answers 200 with an empty result.
	req = httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/api/search/resolve?type=movie&title=Nope", http.NoBody)
	rec = httptest.NewRecorder()
	h.HandleSearchResolve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("HandleSearchResolve(empty) status = %d, want 200", rec.Code)
	}
	var empty ResolveResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &empty); err != nil {
		t.Fatalf("decode empty response: %v", err)
	}
	if empty.Resolved || len(empty.Items) != 0 || len(empty.Candidates) != 0 {
		t.Fatalf("empty response = %+v, want all-empty", empty)
	}
}
