package coveragehandlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/arrapi/v2"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/subflux"
)

// summarySonarrFake is the sonarr double for the summary and collection
// tests: canned rows answered through the same by-id semantics as the cached
// wrapper, canned exclude-tag ids on both resolution forms, and a record of
// whether each read's ctx carried the recovery marker.
type summarySonarrFake struct {
	listErr      error
	byIDErr      error
	tagsErr      error
	tagIDs       map[int]struct{}
	series       []arrapi.Series
	listCalls    int
	byIDCalls    int
	tagsCalls    int // fail-open form
	tagsErrCalls int // error-returning form
	listMarked   bool
	byIDMarked   bool
	tagsMarked   bool
}

func (f *summarySonarrFake) Series(ctx context.Context) ([]arrapi.Series, error) {
	f.listCalls++
	f.listMarked = arrsvc.RecoveryMarked(ctx)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.series, nil
}

func (f *summarySonarrFake) SeriesByTvdbID(ctx context.Context, tvdbID int) (arrapi.Series, bool, error) {
	f.byIDCalls++
	f.byIDMarked = arrsvc.RecoveryMarked(ctx)
	if f.byIDErr != nil {
		return arrapi.Series{}, false, f.byIDErr
	}
	for i := range f.series {
		if f.series[i].TvdbID == tvdbID {
			return f.series[i], true, nil
		}
	}
	return arrapi.Series{}, false, nil
}

func (f *summarySonarrFake) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	f.tagsCalls++
	return f.tagIDs
}

func (f *summarySonarrFake) ResolveExcludeTagIDsErr(ctx context.Context, _ []string, _ bool) (map[int]struct{}, error) {
	f.tagsErrCalls++
	f.tagsMarked = arrsvc.RecoveryMarked(ctx)
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	return f.tagIDs, nil
}

// summaryRadarrFake mirrors summarySonarrFake for the radarr side.
type summaryRadarrFake struct {
	listErr      error
	byIDErr      error
	tagsErr      error
	tagIDs       map[int]struct{}
	movies       []arrapi.Movie
	listCalls    int
	byIDCalls    int
	tagsCalls    int
	tagsErrCalls int
	listMarked   bool
	byIDMarked   bool
	tagsMarked   bool
}

func (f *summaryRadarrFake) Movies(ctx context.Context) ([]arrapi.Movie, error) {
	f.listCalls++
	f.listMarked = arrsvc.RecoveryMarked(ctx)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.movies, nil
}

func (f *summaryRadarrFake) MovieByTmdbID(ctx context.Context, tmdbID int) (arrapi.Movie, bool, error) {
	f.byIDCalls++
	f.byIDMarked = arrsvc.RecoveryMarked(ctx)
	if f.byIDErr != nil {
		return arrapi.Movie{}, false, f.byIDErr
	}
	for i := range f.movies {
		if f.movies[i].TmdbID == tmdbID {
			return f.movies[i], true, nil
		}
	}
	return arrapi.Movie{}, false, nil
}

func (f *summaryRadarrFake) ResolveExcludeTagIDs(_ context.Context, _ []string, _ bool) map[int]struct{} {
	f.tagsCalls++
	return f.tagIDs
}

func (f *summaryRadarrFake) ResolveExcludeTagIDsErr(ctx context.Context, _ []string, _ bool) (map[int]struct{}, error) {
	f.tagsErrCalls++
	f.tagsMarked = arrsvc.RecoveryMarked(ctx)
	if f.tagsErr != nil {
		return nil, f.tagsErr
	}
	return f.tagIDs, nil
}

// prefixStore serves canned subtitle rows honoring the mediaIDPrefix bound —
// the production store is a prefix scan — and records each call's prefix and
// recovery marker (scan-state reads record theirs into scanMarked).
type prefixStore struct {
	err        error
	rows       []subflux.SubtitleEntry
	prefixes   []string
	marked     []bool
	scanMarked []bool
}

func (m *prefixStore) SubtitleFiles(ctx context.Context, _ subflux.MediaType, prefix string) ([]subflux.SubtitleEntry, error) {
	m.prefixes = append(m.prefixes, prefix)
	m.marked = append(m.marked, arrsvc.RecoveryMarked(ctx))
	if m.err != nil {
		return nil, m.err
	}
	out := make([]subflux.SubtitleEntry, 0, len(m.rows))
	for i := range m.rows {
		if strings.HasPrefix(m.rows[i].MediaID, prefix) {
			out = append(out, m.rows[i])
		}
	}
	return out, nil
}

func (m *prefixStore) ScanStates(ctx context.Context, _ subflux.MediaType, _ string) ([]subflux.ScanStateRow, error) {
	m.scanMarked = append(m.scanMarked, arrsvc.RecoveryMarked(ctx))
	return nil, nil
}

// summaryRequest builds a GET request with the {id} wildcard populated the
// way the mux would.
func summaryRequest(target, wildcard, value string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue(wildcard, value)
	return req
}

// summarySeriesFixture is the R7.1 seed: series 81189's title, rule, targets,
// excluded flag and episode count are all meaningful (non-zero), so a
// deep-equal against the collection can fail on any of them; series 99999 is
// the exclusion boundary (zero episode files).
func summarySeriesFixture() []arrapi.Series {
	return []arrapi.Series{
		{
			ID:               1,
			Title:            "Test Show",
			Year:             2024,
			TvdbID:           81189,
			ImdbID:           "tt1234567",
			FirstAired:       "2024-01-01",
			OriginalLanguage: &arrapi.Language{Name: "English"},
			Statistics:       &arrapi.SeriesStatistics{EpisodeFileCount: 3},
			Tags:             []int{1},
		},
		{
			ID:         2,
			Title:      "No Episodes",
			TvdbID:     99999,
			Statistics: &arrapi.SeriesStatistics{EpisodeFileCount: 0},
		},
	}
}

// summarySeriesRows carries usable, ignored-codec, and other-series rows so
// Have, HaveIgnored and the prefix bound all discriminate.
func summarySeriesRows() []subflux.SubtitleEntry {
	return []subflux.SubtitleEntry{
		{MediaID: "tvdb-81189-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
		{MediaID: "tvdb-81189-s01e02", Language: "fr", Variant: "standard", Source: "embedded", Codec: "pgs"},
		{MediaID: "tvdb-55555-s01e01", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
	}
}

func summarySeriesCfg() *fakeCoverageCfg {
	return &fakeCoverageCfg{
		targets:   []subflux.SubtitleTarget{{Code: "fr"}},
		embedded:  subflux.EmbeddedPolicy{IgnorePGS: true},
		searchCfg: subflux.SearchConfig{ExcludeArrTags: []string{"skip"}},
	}
}

func TestHandleCoverageSeriesSummary_deep_equals_collection_item(t *testing.T) {
	t.Parallel()
	cfg := summarySeriesCfg()
	sonarr := &summarySonarrFake{series: summarySeriesFixture(), tagIDs: map[int]struct{}{1: {}}}

	colH := newCoverageHandler(&prefixStore{rows: summarySeriesRows()}, cfg, sonarr, nil)
	colRec := httptest.NewRecorder()
	colH.HandleCoverageSeries(colRec, httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil))
	if colRec.Code != http.StatusOK {
		t.Fatalf("collection status = %d, want %d", colRec.Code, http.StatusOK)
	}
	var col []SeriesItem
	if err := json.Unmarshal(colRec.Body.Bytes(), &col); err != nil {
		t.Fatalf("decode collection: %v", err)
	}
	var want *SeriesItem
	for i := range col {
		if col[i].TvdbID == 81189 {
			want = &col[i]
		}
	}
	if want == nil {
		t.Fatal("collection does not serialize series 81189")
	}
	// The seed is meaningful: every field the R7.1 parity pin varies must be
	// non-zero here, or the deep-equal below could pass vacuously.
	if want.Title != "Test Show" || want.Rule != "en" || want.Episodes != 3 || !want.Excluded {
		t.Fatalf("seed item lost its meaningful fields: %+v", want)
	}
	if len(want.Targets) != 1 || want.Targets[0].Have != 1 || want.Targets[0].HaveIgnored != 1 {
		t.Fatalf("seed targets not meaningful: %+v", want.Targets)
	}

	store := &prefixStore{rows: summarySeriesRows()}
	sumH := newCoverageHandler(store, cfg, sonarr, nil)
	sumRec := httptest.NewRecorder()
	sumH.HandleCoverageSeriesSummary(sumRec,
		summaryRequest("/api/coverage/series/81189/summary", "tvdbId", "81189"))
	if sumRec.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want %d (body %s)", sumRec.Code, http.StatusOK, sumRec.Body)
	}
	var got SeriesItem
	if err := json.Unmarshal(sumRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	if !reflect.DeepEqual(got, *want) {
		t.Errorf("summary item = %+v, want the collection's row %+v", got, *want)
	}
	// The recipe is one PREFIX-BOUNDED subtitle_files scan, never the full
	// inventory.
	if len(store.prefixes) != 1 || store.prefixes[0] != "tvdb-81189-" {
		t.Errorf("summary store prefixes = %q, want one bounded scan [\"tvdb-81189-\"]", store.prefixes)
	}
}

func TestHandleCoverageMovieSummary_deep_equals_collection_item(t *testing.T) {
	t.Parallel()
	cfg := summarySeriesCfg()
	radarr := &summaryRadarrFake{movies: summaryMoviesFixture(), tagIDs: map[int]struct{}{2: {}}}
	rows := []subflux.SubtitleEntry{
		{MediaID: "tmdb-12345", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
		{MediaID: "tmdb-123450", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
	}

	colH := newCoverageHandler(&prefixStore{rows: rows}, cfg, nil, radarr)
	colRec := httptest.NewRecorder()
	colH.HandleCoverageMovies(colRec, httptest.NewRequest(http.MethodGet, "/api/coverage/movies", nil))
	if colRec.Code != http.StatusOK {
		t.Fatalf("collection status = %d, want %d", colRec.Code, http.StatusOK)
	}
	var col []MovieItem
	if err := json.Unmarshal(colRec.Body.Bytes(), &col); err != nil {
		t.Fatalf("decode collection: %v", err)
	}
	var want *MovieItem
	for i := range col {
		if col[i].TmdbID == 12345 {
			want = &col[i]
		}
	}
	if want == nil {
		t.Fatal("collection does not serialize movie 12345")
	}
	if want.Title != "Test Movie" || want.Rule != "en" || !want.Excluded ||
		len(want.Targets) != 1 || want.Targets[0].Have != 1 {
		t.Fatalf("seed item lost its meaningful fields: %+v", want)
	}

	store := &prefixStore{rows: rows}
	sumH := newCoverageHandler(store, cfg, nil, radarr)
	sumRec := httptest.NewRecorder()
	sumH.HandleCoverageMovieSummary(sumRec,
		summaryRequest("/api/coverage/movies/12345/summary", "tmdbId", "12345"))
	if sumRec.Code != http.StatusOK {
		t.Fatalf("summary status = %d, want %d (body %s)", sumRec.Code, http.StatusOK, sumRec.Body)
	}
	var got MovieItem
	if err := json.Unmarshal(sumRec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode summary: %v", err)
	}

	// FULL equality (R7.1): with MovieItem.Subs gone, the summary is the
	// collection's row, byte for byte.
	if !reflect.DeepEqual(got, *want) {
		t.Errorf("summary item = %+v, want the collection's row %+v", got, *want)
	}
	if len(store.prefixes) != 1 || store.prefixes[0] != "tmdb-12345" {
		t.Errorf("summary store prefixes = %q, want one bounded scan [\"tmdb-12345\"]", store.prefixes)
	}
}

// summaryMoviesFixture: movie 12345 is the meaningful seed, 99999 the
// file-less exclusion boundary.
func summaryMoviesFixture() []arrapi.Movie {
	return []arrapi.Movie{
		{
			ID:               1,
			Title:            "Test Movie",
			Year:             2024,
			TmdbID:           12345,
			ImdbID:           "tt9876543",
			InCinemas:        "2024-06-01",
			DigitalRelease:   "2024-09-01",
			HasFile:          true,
			OriginalLanguage: &arrapi.Language{Name: "English"},
			MovieFile:        &arrapi.MovieFile{Path: "/movies/test.mkv", SceneName: "Test.Movie.2024"},
			Tags:             []int{2},
		},
		{
			ID:      2,
			Title:   "No File Movie",
			TmdbID:  99999,
			HasFile: false,
		},
	}
}

func TestHandleCoverageSeriesSummary_exclusion_boundary_404(t *testing.T) {
	t.Parallel()
	cases := []struct {
		sonarr CoverageSonarrClient
		name   string
		tvdbID string
	}{
		{name: "zero_episode_series", sonarr: &summarySonarrFake{series: summarySeriesFixture()}, tvdbID: "99999"},
		{name: "vanished_id", sonarr: &summarySonarrFake{series: summarySeriesFixture()}, tvdbID: "31337"},
		{name: "nil_sonarr", sonarr: nil, tvdbID: "81189"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), tc.sonarr, nil)
			rec := httptest.NewRecorder()
			h.HandleCoverageSeriesSummary(rec,
				summaryRequest("/api/coverage/series/"+tc.tvdbID+"/summary", "tvdbId", tc.tvdbID))
			if rec.Code != http.StatusNotFound {
				t.Errorf("series summary (%s) status = %d, want %d", tc.name, rec.Code, http.StatusNotFound)
			}
		})
	}
}

func TestHandleCoverageMovieSummary_exclusion_boundary_404(t *testing.T) {
	t.Parallel()
	cases := []struct {
		radarr CoverageRadarrClient
		name   string
		tmdbID string
	}{
		{name: "file_less_movie", radarr: &summaryRadarrFake{movies: summaryMoviesFixture()}, tmdbID: "99999"},
		{name: "vanished_id", radarr: &summaryRadarrFake{movies: summaryMoviesFixture()}, tmdbID: "31337"},
		{name: "nil_radarr", radarr: nil, tmdbID: "12345"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), nil, tc.radarr)
			rec := httptest.NewRecorder()
			h.HandleCoverageMovieSummary(rec,
				summaryRequest("/api/coverage/movies/"+tc.tmdbID+"/summary", "tmdbId", tc.tmdbID))
			if rec.Code != http.StatusNotFound {
				t.Errorf("movie summary (%s) status = %d, want %d", tc.name, rec.Code, http.StatusNotFound)
			}
		})
	}
}

func TestCoverageSummaries_zero_canonical_id_rows_unreachable(t *testing.T) {
	t.Parallel()
	// The A2 exclusion parity for the rows the collections stopped
	// serializing (A3): a fixture holding ONLY rows without a positive
	// canonical id answers 404 for every positive-id request — no id can
	// reach such a row — and the zero id itself is malformed (400).
	sonarr := &summarySonarrFake{series: []arrapi.Series{{
		ID: 1, Title: "Zero Id", TvdbID: 0, ImdbID: "tt0000001",
		Statistics: &arrapi.SeriesStatistics{EpisodeFileCount: 5},
	}}}
	radarr := &summaryRadarrFake{movies: []arrapi.Movie{{
		ID: 1, Title: "Imdb Only", TmdbID: 0, ImdbID: "tt0000002", HasFile: true,
	}}}
	h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, radarr)

	rec := httptest.NewRecorder()
	h.HandleCoverageSeriesSummary(rec,
		summaryRequest("/api/coverage/series/81189/summary", "tvdbId", "81189"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("series summary status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	rec = httptest.NewRecorder()
	h.HandleCoverageMovieSummary(rec,
		summaryRequest("/api/coverage/movies/12345/summary", "tmdbId", "12345"))
	if rec.Code != http.StatusNotFound {
		t.Errorf("movie summary status = %d, want %d", rec.Code, http.StatusNotFound)
	}

	rec = httptest.NewRecorder()
	h.HandleCoverageSeriesSummary(rec,
		summaryRequest("/api/coverage/series/0/summary", "tvdbId", "0"))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("series summary (id 0) status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestCoverageSummaryHandlers_malformed_id_400(t *testing.T) {
	t.Parallel()
	handlers := []struct {
		serve    func(h *Handler, rec *httptest.ResponseRecorder, id string)
		name     string
		wildcard string
	}{
		{name: "series_summary", wildcard: "tvdbId", serve: func(h *Handler, rec *httptest.ResponseRecorder, id string) {
			h.HandleCoverageSeriesSummary(rec, summaryRequest("/api/coverage/series/"+id+"/summary", "tvdbId", id))
		}},
		{name: "movie_summary", wildcard: "tmdbId", serve: func(h *Handler, rec *httptest.ResponseRecorder, id string) {
			h.HandleCoverageMovieSummary(rec, summaryRequest("/api/coverage/movies/"+id+"/summary", "tmdbId", id))
		}},
		{name: "movie_subs", wildcard: "tmdbId", serve: func(h *Handler, rec *httptest.ResponseRecorder, id string) {
			h.HandleCoverageMovieSubs(rec, summaryRequest("/api/coverage/movies/"+id+"/subs", "tmdbId", id))
		}},
	}
	for _, tc := range handlers {
		for _, id := range []string{"abc", "-5", "0", ""} {
			t.Run(tc.name+"_"+id, func(t *testing.T) {
				t.Parallel()
				h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(),
					&summarySonarrFake{}, &summaryRadarrFake{})
				rec := httptest.NewRecorder()
				tc.serve(h, rec, id)
				if rec.Code != http.StatusBadRequest {
					t.Errorf("%s(%q) status = %d, want %d", tc.name, id, rec.Code, http.StatusBadRequest)
				}
			})
		}
	}
}

func TestHandleCoverageMovieSubs_store_only(t *testing.T) {
	t.Parallel()

	t.Run("rows_for_excluded_or_vanished_movie", func(t *testing.T) {
		t.Parallel()
		// The movie is in NO arr list (vanished) and the store still answers
		// its rows: /subs claims no exclusion parity and reads no arr. The
		// tmdb-123450 row pins the exact-match bound ("tmdb-12345" is a
		// prefix of it).
		store := &prefixStore{rows: []subflux.SubtitleEntry{
			{MediaID: "tmdb-12345", Language: "fr", Variant: "standard", Source: "external", Codec: "srt"},
			{MediaID: "tmdb-123450", Language: "de", Variant: "standard", Source: "external", Codec: "srt"},
		}}
		radarr := &summaryRadarrFake{}
		h := newCoverageHandler(store, summarySeriesCfg(), nil, radarr)
		rec := httptest.NewRecorder()
		h.HandleCoverageMovieSubs(rec,
			summaryRequest("/api/coverage/movies/12345/subs?recovery=1", "tmdbId", "12345"))
		if rec.Code != http.StatusOK {
			t.Fatalf("subs status = %d, want %d", rec.Code, http.StatusOK)
		}
		var rows []subflux.SubtitleEntry
		if err := json.Unmarshal(rec.Body.Bytes(), &rows); err != nil {
			t.Fatalf("decode subs: %v", err)
		}
		if len(rows) != 1 || rows[0].MediaID != "tmdb-12345" {
			t.Errorf("subs rows = %+v, want exactly the tmdb-12345 row (exact-match bound)", rows)
		}
		if radarr.byIDCalls != 0 || radarr.tagsCalls != 0 || radarr.tagsErrCalls != 0 {
			t.Errorf("subs made arr reads (byID %d, tags %d/%d), want none (store-only)",
				radarr.byIDCalls, radarr.tagsCalls, radarr.tagsErrCalls)
		}
		// ?recovery=1 is NOT interpreted here: the store read's ctx carries
		// no recovery marker.
		if len(store.marked) != 1 || store.marked[0] {
			t.Errorf("subs store reads marked = %v, want one unmarked read", store.marked)
		}
		if len(store.prefixes) != 1 || store.prefixes[0] != "tmdb-12345" {
			t.Errorf("subs store prefixes = %q, want one bounded scan [\"tmdb-12345\"]", store.prefixes)
		}
	})

	t.Run("no_rows_answers_empty_list", func(t *testing.T) {
		t.Parallel()
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), nil, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageMovieSubs(rec,
			summaryRequest("/api/coverage/movies/777/subs", "tmdbId", "777"))
		if rec.Code != http.StatusOK {
			t.Fatalf("subs status = %d, want %d", rec.Code, http.StatusOK)
		}
		if body := strings.TrimSpace(rec.Body.String()); body != "[]" {
			t.Errorf("subs body = %q, want empty list []", body)
		}
	})
}

func TestCoverageSummaryHandlers_recovery_honoring(t *testing.T) {
	t.Parallel()

	t.Run("series_summary_interprets_recovery_1", func(t *testing.T) {
		t.Parallel()
		sonarr := &summarySonarrFake{series: summarySeriesFixture()}
		store := &prefixStore{}
		h := newCoverageHandler(store, summarySeriesCfg(), sonarr, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageSeriesSummary(rec,
			summaryRequest("/api/coverage/series/81189/summary?recovery=1", "tvdbId", "81189"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if !sonarr.byIDMarked {
			t.Error("marked request: SeriesByTvdbID ctx carried no recovery marker")
		}
		// A marked read resolves exclude tags through the ERROR-RETURNING
		// form, so a wave failure can never fail open into an
		// empty-exclusion 200.
		if sonarr.tagsErrCalls != 1 || !sonarr.tagsMarked || sonarr.tagsCalls != 0 {
			t.Errorf("marked tag reads: err-form %d (marked %v), fail-open %d; want 1 (marked), 0",
				sonarr.tagsErrCalls, sonarr.tagsMarked, sonarr.tagsCalls)
		}
	})

	t.Run("movie_summary_interprets_recovery_1", func(t *testing.T) {
		t.Parallel()
		radarr := &summaryRadarrFake{movies: summaryMoviesFixture()}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), nil, radarr)
		rec := httptest.NewRecorder()
		h.HandleCoverageMovieSummary(rec,
			summaryRequest("/api/coverage/movies/12345/summary?recovery=1", "tmdbId", "12345"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if !radarr.byIDMarked || radarr.tagsErrCalls != 1 || !radarr.tagsMarked {
			t.Errorf("marked movie summary: byID marked %v, err-form calls %d (marked %v); want all marked",
				radarr.byIDMarked, radarr.tagsErrCalls, radarr.tagsMarked)
		}
	})

	t.Run("plain_read_stays_unmarked_and_fails_open", func(t *testing.T) {
		t.Parallel()
		sonarr := &summarySonarrFake{series: summarySeriesFixture()}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageSeriesSummary(rec,
			summaryRequest("/api/coverage/series/81189/summary", "tvdbId", "81189"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if sonarr.byIDMarked {
			t.Error("plain request: SeriesByTvdbID ctx carried a recovery marker")
		}
		if sonarr.tagsCalls != 1 || sonarr.tagsErrCalls != 0 {
			t.Errorf("plain tag reads: fail-open %d, err-form %d; want 1, 0",
				sonarr.tagsCalls, sonarr.tagsErrCalls)
		}
	})

	t.Run("only_the_literal_1_marks", func(t *testing.T) {
		t.Parallel()
		sonarr := &summarySonarrFake{series: summarySeriesFixture()}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageSeriesSummary(rec,
			summaryRequest("/api/coverage/series/81189/summary?recovery=0", "tvdbId", "81189"))
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
		}
		if sonarr.byIDMarked {
			t.Error("recovery=0 marked the read; only recovery=1 may")
		}
	})
}

func TestCoverageSummaryHandlers_sentinel_mapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		err  error
		name string
		want int
	}{
		{name: "refused_maps_to_429", err: fmt.Errorf("%w: budget", arrsvc.ErrRecoveryRefused), want: http.StatusTooManyRequests},
		{name: "failed_maps_to_502", err: fmt.Errorf("%w: upstream", arrsvc.ErrRecoveryFailed), want: http.StatusBadGateway},
		{name: "unknown_series_maps_to_404", err: fmt.Errorf("%w: series 81189", arrsvc.ErrUnknownSeries), want: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run("series_by_id_"+tc.name, func(t *testing.T) {
			t.Parallel()
			sonarr := &summarySonarrFake{byIDErr: tc.err}
			h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
			rec := httptest.NewRecorder()
			h.HandleCoverageSeriesSummary(rec,
				summaryRequest("/api/coverage/series/81189/summary?recovery=1", "tvdbId", "81189"))
			if rec.Code != tc.want {
				t.Errorf("series summary status = %d, want %d (err %v)", rec.Code, tc.want, tc.err)
			}
		})
		t.Run("movie_by_id_"+tc.name, func(t *testing.T) {
			t.Parallel()
			radarr := &summaryRadarrFake{byIDErr: tc.err}
			h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), nil, radarr)
			rec := httptest.NewRecorder()
			h.HandleCoverageMovieSummary(rec,
				summaryRequest("/api/coverage/movies/12345/summary?recovery=1", "tmdbId", "12345"))
			if rec.Code != tc.want {
				t.Errorf("movie summary status = %d, want %d (err %v)", rec.Code, tc.want, tc.err)
			}
		})
	}

	t.Run("marked_tag_leg_refusal_maps_to_429", func(t *testing.T) {
		t.Parallel()
		sonarr := &summarySonarrFake{
			series:  summarySeriesFixture(),
			tagsErr: fmt.Errorf("%w: budget", arrsvc.ErrRecoveryRefused),
		}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageSeriesSummary(rec,
			summaryRequest("/api/coverage/series/81189/summary?recovery=1", "tvdbId", "81189"))
		if rec.Code != http.StatusTooManyRequests {
			t.Errorf("marked tag refusal status = %d, want %d (never a silent empty-exclusion 200)",
				rec.Code, http.StatusTooManyRequests)
		}
	})
}

func TestCoverageSummaryHandlers_reject_non_get(t *testing.T) {
	t.Parallel()
	h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), &summarySonarrFake{}, &summaryRadarrFake{})
	serves := map[string]http.HandlerFunc{
		"series_summary": h.HandleCoverageSeriesSummary,
		"movie_summary":  h.HandleCoverageMovieSummary,
		"movie_subs":     h.HandleCoverageMovieSubs,
	}
	for name, serve := range serves {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/coverage/x", nil)
			req.SetPathValue("tvdbId", "1")
			req.SetPathValue("tmdbId", "1")
			serve(rec, req)
			if rec.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s(POST) status = %d, want %d", name, rec.Code, http.StatusMethodNotAllowed)
			}
		})
	}
}

func TestHandleCoverageSeriesSummary_store_error_returns_500(t *testing.T) {
	t.Parallel()
	sonarr := &summarySonarrFake{series: summarySeriesFixture()}
	h := newCoverageHandler(&prefixStore{err: errMock}, summarySeriesCfg(), sonarr, nil)
	rec := httptest.NewRecorder()
	h.HandleCoverageSeriesSummary(rec,
		summaryRequest("/api/coverage/series/81189/summary", "tvdbId", "81189"))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("series summary (store error) status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

// TestCoverageHandlers_client_abort_is_silent pins the walk-away arm on both
// coverage error writers: a read aborted by the client (waveRead returns the
// request context's error, and only r.Context().Err() reports the walk-away)
// produces no ERROR-level log and no 502 write — nobody is left to read
// either. Serial: captures the process-global slog default.
func TestCoverageHandlers_client_abort_is_silent(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the client walked away mid-wave-wait

	t.Run("summary", func(t *testing.T) {
		buf.Reset()
		sonarr := &summarySonarrFake{byIDErr: ctx.Err()}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageSeriesSummary(rec,
			summaryRequest("/api/coverage/series/81189/summary?recovery=1", "tvdbId", "81189").WithContext(ctx))
		if buf.Len() != 0 {
			t.Errorf("aborted summary request logged: %s", buf.String())
		}
		if rec.Body.Len() != 0 {
			t.Errorf("aborted summary request got %d %q written, want nothing", rec.Code, rec.Body.String())
		}
	})

	t.Run("collection", func(t *testing.T) {
		buf.Reset()
		sonarr := &summarySonarrFake{listErr: ctx.Err()}
		h := newCoverageHandler(&prefixStore{}, summarySeriesCfg(), sonarr, nil)
		rec := httptest.NewRecorder()
		h.HandleCoverageSeries(rec,
			httptest.NewRequest(http.MethodGet, "/api/coverage/series", nil).WithContext(ctx))
		if buf.Len() != 0 {
			t.Errorf("aborted collection request logged: %s", buf.String())
		}
		if rec.Body.Len() != 0 {
			t.Errorf("aborted collection request got %d %q written, want nothing", rec.Code, rec.Body.String())
		}
	})
}
