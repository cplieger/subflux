package subx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subx"
	"github.com/cplieger/subflux/internal/rank"
)

func failure(t *testing.T, err error) *provider.Failure {
	t.Helper()
	f, ok := errors.AsType[*provider.Failure](err)
	if !ok {
		t.Fatalf("%v, want a *provider.Failure", err)
	}
	return f
}

// TestSubXIsException: SubX is a named exception (design 5.8) sending
// subflux's own agent, so its first search reads robots.txt, a path that
// file disallows is refused unsent, and a search or download 403 pauses it
// an hour with the one request sent.
func TestSubXIsException(t *testing.T) {
	if e := subx.NewEntry(); !e.Exception || e.UserAgent != "" {
		t.Fatalf("Exception %t, UserAgent %q; want an exception with subflux's agent", e.Exception, e.UserAgent)
	}
	t.Run("robots first", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded()
			ctx := providertest.Serve(t, t.Context(), s, host)
			if _, err := adapter(t).Search(ctx, inception("es-419")); err != nil {
				t.Fatalf("search: %v", err)
			}
			reqs := s.seen()
			if len(reqs) != 2 || reqs[0].URL.Path != "/robots.txt" || reqs[1].URL.Path != search {
				t.Errorf("asked %d requests, want robots.txt then the search", len(reqs))
			}
		})
	})
	t.Run("disallowed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded()
			ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/robots.txt" {
					_, _ = w.Write([]byte("User-agent: *\nDisallow: /api/subtitles/\n"))
					return
				}
				s.ServeHTTP(w, r)
			}), host)
			a := adapter(t)
			_, err := a.Search(ctx, inception("es-419"))
			if f := failure(t, err); f.Kind != provider.Permanent {
				t.Errorf("disallowed search: %v, want Permanent", f)
			}
			_, err = a.Fetch(ctx, provider.NewRef(rar))
			if f := failure(t, err); f.Kind != provider.Permanent {
				t.Errorf("disallowed download: %v, want Permanent", f)
			}
			if p := s.asked(); len(p) != 0 {
				t.Errorf("asked %q, want nothing past robots.txt", p)
			}
		})
	})
	for _, c := range []struct {
		name string
		call func(context.Context, provider.Adapter) error
	}{
		{"search 403", func(ctx context.Context, a provider.Adapter) error {
			_, err := a.Search(ctx, inception("es-419"))
			return err
		}},
		{"download 403", func(ctx context.Context, a provider.Adapter) error {
			_, err := a.Fetch(ctx, provider.NewRef(rar))
			return err
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &site{routes: map[string]route{
					"/robots.txt":                         {file: "robots-404.html", status: http.StatusNotFound},
					search:                                {file: "search-401.json", status: http.StatusForbidden},
					"/api/subtitles/" + rar + "/download": {file: "search-401.json", status: http.StatusForbidden},
				}}
				ctx := providertest.Serve(t, t.Context(), s, host)
				err := c.call(ctx, adapter(t))
				if f := failure(t, err); f.Kind != provider.RateLimited || f.RetryAfter < time.Hour {
					t.Errorf("403: %v for %v, want RateLimited for at least an hour", f, f.RetryAfter)
				}
				if p := s.asked(); len(p) != 1 {
					t.Errorf("asked %q, want the one request", p)
				}
			})
		})
	}
}

// candidate is what one item's candidate must hold.
type candidate struct {
	ref, lang, release, title string
	year                      int
	byID                      bool
}

// TestSubXSearch pins the lookups over the recorded answers: by IMDb id
// with the key as a Bearer token, else by the first title and year; an
// episode by its season, keeping the episode's items and the season's
// packs; a candidate per item in an asked variety, es-ES when its
// description names Spain's Spanish, matched by id only when filed under
// the query's IMDb id.
func TestSubXSearch(t *testing.T) {
	film := []candidate{
		{ref: "5bd14610-cfce-4075-a93a-5295b3ea21d1", lang: "es-419", release: "Inception.2010.DVDRip.XviD.AC3-ViSiON", year: 2010, byID: true},
		{ref: "0417e91c-1589-46ce-8d91-a13659ab53fa", lang: "es-ES", release: "Inception.2010.720p.BluRay.DTS.x264-ESiR", year: 2010, byID: true},
		{ref: "3892f7d9-a05d-42ae-9bb5-ba941a487279", lang: "es-419", release: "Inception (2010)", year: 2010, byID: true},
	}
	season := []candidate{
		{ref: "904ce529-2881-4b04-ada2-e3665e9de0a6", lang: "es-419", release: "Breaking Bad S01", year: 2008, byID: true},
		{ref: rar, lang: "es-419", release: "Breaking.Bad.S01E03.DVDRip.XviD-ORPHEUS", title: "Breaking Bad", year: 2008},
		{ref: "11e90fab-e81b-49b0-b0cb-a1cc81c4f381", lang: "es-419", release: "Breaking Bad S01E03", title: "Breaking Bad", year: 2008},
		{ref: "752d5856-aa6e-4a22-8241-70bdfffc6f4c", lang: "es-ES", release: "Breaking Bad S01E03", title: "Breaking Bad", year: 2008},
		{ref: "bd9cb977-b99a-4dbc-bb8e-56e61e31cfff", lang: "es-419", release: "Breaking Bad S01", year: 2008, byID: true},
	}
	byName := []candidate{
		{ref: "5bd14610-cfce-4075-a93a-5295b3ea21d1", lang: "es-419", release: "Inception.2010.DVDRip.XviD.AC3-ViSiON", title: "Inception", year: 2010},
		{ref: "28bc5502-86dd-49d4-bb7c-534673591568", lang: "es-419", release: "Inception.The.Cobol.Job.2010.720p.BDRip.XviD.AC3-ViSiON", title: "Inception: The Cobol Job", year: 2010},
		{ref: "0ce339cb-5a9a-4044-9ff5-3a6e01f7d5b8", lang: "es-419", release: "Inception (2010) - Teaser Trailer", title: "Inception (2010) - Teaser Trailer", year: 2010},
		{ref: "341bed59-491f-4882-bb43-aa5fba3c6f5a", lang: "es-419", release: "Inception RERiP DVDRiP XviD-ARROW", title: "Inception RERiP DVDRiP XviD-ARROW"},
	}
	for _, c := range []struct {
		name  string
		q     provider.Query
		asked []string
		n     int
		some  []candidate
	}{
		{"film by IMDb id", inception("es-ES", "es-419"), []string{search + "?" + filmByID}, 84, film},
		{"film in es-ES", inception("es-ES"), []string{search + "?" + filmByID}, 1, film[1:2]},
		{"episode by IMDb id", breakingBad("es-ES", "es-419"), []string{search + "?" + seasonByID}, 21, season},
		{"film by title", noIMDb(inception("es-ES", "es-419")), []string{search + "?" + filmByName}, 91, byName},
		{"episode by title", noIMDb(breakingBad("es-ES", "es-419")), []string{search + "?" + seasonByName}, 31, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := recorded()
				ctx := providertest.Serve(t, t.Context(), s, host)
				got, err := adapter(t).Search(ctx, c.q)
				if err != nil {
					t.Fatalf("search: %v", err)
				}
				if p := s.asked(); !slices.Equal(p, c.asked) {
					t.Errorf("asked %q, want %q", p, c.asked)
				}
				for _, r := range s.seen() {
					if r.URL.Path == search && (r.Header.Get("Authorization") != "Bearer "+key || strings.Contains(r.URL.String(), key)) {
						t.Errorf("search sent Authorization %q to %s; want the key as a Bearer token and never in the URL", r.Header.Get("Authorization"), r.URL)
					}
				}
				if len(got) != c.n {
					t.Errorf("%d candidates, want %d", len(got), c.n)
				}
				for _, w := range c.some {
					i := slices.IndexFunc(got, func(g provider.Candidate) bool { return g.Ref.Token() == w.ref })
					if i < 0 {
						t.Errorf("no candidate %s", w.ref)
						continue
					}
					g := &got[i]
					wantBy := rank.Title
					if w.byID {
						wantBy = rank.ID
					}
					if g.Lang != tags(w.lang)[0] || g.Release != w.release || g.Title != w.title || g.Year != w.year || g.MatchedBy != wantBy || g.Covers != nil {
						t.Errorf("candidate %s: %v %q titled %q, %d, matched by %v, covers %v; want %s %q titled %q, %d, matched by %v, no Covers",
							w.ref, g.Lang, g.Release, g.Title, g.Year, g.MatchedBy, g.Covers, w.lang, w.release, w.title, w.year, wantBy)
					}
				}
			})
		})
	}
}

func noIMDb(q provider.Query) provider.Query {
	q.External.IMDb = 0
	return q
}

// TestSubXEpisodeAnswerOrder: over the recorded season, the episode's items
// and the season's packs keep the answer's order, and no other episode's
// item is offered.
func TestSubXEpisodeAnswerOrder(t *testing.T) {
	want := []string{
		"904ce529-2881-4b04-ada2-e3665e9de0a6", rar, "205decf0-fc29-45ae-b021-2e7204ec7174",
		"54e4aad8-0760-4a72-bb69-ae7075af6bb3", "11e90fab-e81b-49b0-b0cb-a1cc81c4f381", "6383f994-1e89-4bad-9943-584e3576e935",
		"9eb61b07-3489-4474-a111-6561ec423b35", "cb49923b-b795-4e7d-a507-35daed7146a0", "bd9cb977-b99a-4dbc-bb8e-56e61e31cfff",
		"07ca4019-def0-466a-bc13-7dd094bde44a", "752d5856-aa6e-4a22-8241-70bdfffc6f4c", "391a7c5b-bf84-4c99-8f9a-f40270b0c8c1",
		"9305130f-c750-48db-80b2-d308bf35057e", "e2a32a4e-11aa-4b55-bf96-f785ad2228fa", "71959b88-3f0d-4581-8948-09e880e8d98c",
		"8aaf9cf6-5ecb-4115-93cb-4c9369e2b3a5", "b3168cb1-0913-4c3c-90a0-1a23822e8dcc", "69ea656c-f743-4bf6-83df-a2cf4d21e5bb",
		"d69e0835-7e0d-4cee-9f4d-5bbf0b8e50cd", "c8115454-9928-430f-abd7-4468558fc582", "05d22523-d969-451a-9757-1db5ce18cc3b",
	}
	synctest.Test(t, func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), recorded(), host)
		got, err := adapter(t).Search(ctx, breakingBad("es-ES", "es-419"))
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		var refs []string
		for _, c := range got {
			refs = append(refs, c.Ref.Token())
		}
		if !slices.Equal(refs, want) {
			t.Errorf("candidates %q, want %q", refs, want)
		}
	})
}

// fullSeason is a season answer of 200 items, the API's ceiling: 198 of
// episode 5, one of episode 3 and one pack.
func fullSeason() []byte {
	type it struct {
		Season  *int   `json:"season"`
		Episode *int   `json:"episode"`
		ID      string `json:"id"`
		Title   string `json:"title"`
		IMDb    string `json:"imdb_id"`
		Year    int    `json:"year"`
	}
	one, three, five := 1, 3, 5
	items := make([]it, 0, 200)
	for i := range 198 {
		items = append(items, it{&one, &five, fmt.Sprintf("00000000-0000-4000-8000-%012d", i), "Breaking Bad S01E05", "tt1054727", 2008})
	}
	items = append(items,
		it{&one, &three, "00000000-0000-4000-8000-000000000198", "Breaking Bad S01E03", "tt1054725", 2008},
		it{&one, nil, "00000000-0000-4000-8000-000000000199", "Breaking Bad S01", "tt0903747", 2008})
	b, err := json.Marshal(map[string]any{"items": items, "total": len(items)})
	if err != nil {
		panic(err)
	}
	return b
}

// TestSubXFullSeason: a season answer at the 200-item ceiling may have left
// the episode's items out, so a second request asks the episode alone, and
// the season answer adds only its packs; when that request fails, the
// season answer's candidates come back with its failure.
func TestSubXFullSeason(t *testing.T) {
	for _, c := range []struct {
		name    string
		episode route
		want    int
		fails   bool
	}{
		{"episode asked", route{file: "search-imdb-tt0903747-s1e3.json"}, 16 + 1, false},
		{"episode request fails", route{file: "search-401.json", status: http.StatusServiceUnavailable}, 2, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := recorded()
				s.routes[search+"?"+seasonByID] = route{body: fullSeason()}
				s.routes[search+"?"+episodeByID] = c.episode
				ctx := providertest.Serve(t, t.Context(), s, host)
				got, err := adapter(t).Search(ctx, breakingBad("es-ES", "es-419"))
				if c.fails {
					if f := failure(t, err); f.Kind != provider.RateLimited {
						t.Errorf("failed episode request: %v, want its RateLimited failure", f)
					}
				} else if err != nil {
					t.Fatalf("search: %v", err)
				}
				if p := s.asked(); !slices.Equal(p, []string{search + "?" + seasonByID, search + "?" + episodeByID}) {
					t.Errorf("asked %q, want the season then the episode", p)
				}
				if len(got) != c.want {
					t.Fatalf("%d candidates, want %d", len(got), c.want)
				}
				if last := got[len(got)-1].Ref.Token(); last != "00000000-0000-4000-8000-000000000199" {
					t.Errorf("last candidate %s, want the season's pack", last)
				}
				if c.fails && got[0].Ref.Token() != "00000000-0000-4000-8000-000000000198" {
					t.Errorf("first candidate %s, want the season answer's episode 3", got[0].Ref.Token())
				}
			})
		})
	}
}

// TestSubXSkips: a query with neither an IMDb id nor a title, and an
// episode outside SubX's numbering from season 1, send nothing.
func TestSubXSkips(t *testing.T) {
	special := breakingBad("es-419")
	special.Numbering.Season = 0
	untitled := noIMDb(inception("es-419"))
	untitled.Titles = []string{" "}
	for name, q := range map[string]provider.Query{"no id or title": untitled, "season 0": special} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := recorded()
				ctx := providertest.Serve(t, t.Context(), s, host)
				_, err := adapter(t).Search(ctx, q)
				if _, ok := errors.AsType[*provider.Skipped](err); !ok {
					t.Errorf("%v, want a Skipped", err)
				}
				if n := len(s.seen()); n != 0 {
					t.Errorf("%d requests sent, want none", n)
				}
			})
		})
	}
}

// TestSubXYearOutOfRange: a year SubX refuses (outside 1900 to 2100) is not
// sent, so the title search still runs.
func TestSubXYearOutOfRange(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := recorded()
		ctx := providertest.Serve(t, t.Context(), s, host)
		q := noIMDb(inception("es-419"))
		q.Year = 1800
		_, _ = adapter(t).Search(ctx, q)
		if p := s.asked(); !slices.Equal(p, []string{search + "?limit=200&title=Inception&video_type=movie"}) {
			t.Errorf("asked %q, want the title alone", p)
		}
	})
}

// TestSubX429AnHour: a 429, Retry-After 45 as SubX documents it, pauses
// SubX at least an hour, the named exceptions' back-off.
func TestSubX429AnHour(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/robots.txt" {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Retry-After", "45")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"detail":"Rate limit exceeded"}`))
		}), host)
		_, err := adapter(t).Search(ctx, inception("es-419"))
		if f := failure(t, err); f.Kind != provider.RateLimited || f.RetryAfter < time.Hour {
			t.Errorf("429: %v for %v, want RateLimited for at least an hour", f, f.RetryAfter)
		}
	})
}

// TestSubXFetch: Fetch asks the id's download path with the key and returns
// the file as served; a redirect off SubX's host is refused unsent, and a
// Ref that is no SubX id sends nothing.
func TestSubXFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		away := &site{}
		ctx := providertest.Serve(t, t.Context(), away, foreign)
		s := recorded()
		var redirected atomic.Int32
		ctx = providertest.Serve(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/subtitles/"+gone[:35]+"1/download" {
				redirected.Add(1)
				http.Redirect(w, r, "https://"+foreign+"/x.rar", http.StatusFound)
				return
			}
			s.ServeHTTP(w, r)
		}), host)
		a := adapter(t)
		got, err := a.Search(ctx, breakingBad("es-419"))
		if err != nil || len(got) < 2 || got[1].Ref.Token() != rar {
			t.Fatalf("search: %d candidates, %v; want the recorded rar second", len(got), err)
		}
		b, err := a.Fetch(ctx, got[1].Ref)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		want, err := os.ReadFile("testdata/dl-daedbf8a.rar")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(b, want) {
			t.Errorf("fetch: %d bytes, want the %d recorded", len(b), len(want))
		}
		reqs := s.seen()
		if r := reqs[len(reqs)-1]; r.URL.Path != "/api/subtitles/"+rar+"/download" || r.Header.Get("Authorization") != "Bearer "+key {
			t.Errorf("asked %s with Authorization %q, want the id's download with the key", r.URL.Path, r.Header.Get("Authorization"))
		}
		z, err := a.Fetch(ctx, provider.NewRef(zip))
		if err != nil {
			t.Fatalf("fetch the zip: %v", err)
		}
		var listed []string
		if _, err := archive.Extract(z, func(names []string) (int, error) { listed = names; return 0, nil }); err != nil ||
			!slices.Equal(listed, []string{"Inception (2010).spa.srt", "Inception (2010).eng.srt"}) {
			t.Errorf("the zip offers %q (%v), want its two members", listed, err)
		}
		before := len(s.seen())
		_, err = a.Fetch(ctx, provider.NewRef(gone[:35]+"1"))
		if f := failure(t, err); f.Kind != provider.Permanent {
			t.Errorf("redirect off the host: %v, want Permanent", f)
		}
		for _, tok := range []string{"https://" + foreign + "/x", "../search", strings.ToUpper(rar), rar + "/", rar[:35], ""} {
			_, err := a.Fetch(ctx, provider.NewRef(tok))
			if f := failure(t, err); f.Kind != provider.Permanent {
				t.Errorf("fetch %q: %v, want Permanent", tok, f)
			}
		}
		if n := len(s.seen()) - before; n != 0 || redirected.Load() != 1 {
			t.Errorf("%d more requests and %d redirected downloads reached SubX, want the one redirected download", n, redirected.Load())
		}
		if n := len(away.seen()); n != 0 {
			t.Errorf("the foreign host answered %d requests", n)
		}
	})
}

// TestSubXFetchAnswers: the recorded 404 is NotFound and an empty file
// Permanent.
func TestSubXFetchAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := recorded()
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/subtitles/"+zip+"/download" {
				return
			}
			s.ServeHTTP(w, r)
		}), host)
		a := adapter(t)
		_, err := a.Fetch(ctx, provider.NewRef(gone))
		if f := failure(t, err); f.Kind != provider.NotFound {
			t.Errorf("404: %v, want NotFound", f)
		}
		_, err = a.Fetch(ctx, provider.NewRef(zip))
		if f := failure(t, err); f.Kind != provider.Permanent {
			t.Errorf("empty download: %v, want Permanent", f)
		}
	})
}

// TestSubXTester: Test searches one fixed IMDb id for one item, and the
// recorded refusal of a wrong key is Auth.
func TestSubXTester(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := recorded()
		ctx := providertest.Serve(t, t.Context(), s, host)
		tester, ok := adapter(t).(provider.Tester)
		if !ok {
			t.Fatal("no Tester")
		}
		if err := tester.Test(ctx); err != nil {
			t.Fatalf("test: %v", err)
		}
		if p := s.asked(); !slices.Equal(p, []string{search + "?" + testerSearch}) {
			t.Errorf("asked %q, want one search for one item", p)
		}
		refused := providertest.Serve(t, t.Context(), &site{routes: map[string]route{search: {file: "search-401.json", status: http.StatusUnauthorized}}}, host)
		if f := failure(t, adapter(t).(provider.Tester).Test(refused)); f.Kind != provider.Auth {
			t.Errorf("wrong key: %v, want Auth", f)
		}
	})
}

// TestProviderMaps: SubX's map holds its two Spanish varieties.
func TestProviderMaps(t *testing.T) {
	got := subx.NewEntry().Languages.Tags()
	want := tags("es-ES", "es-419")
	if len(got) != 2 || !slices.Contains(got, want[0]) || !slices.Contains(got, want[1]) {
		t.Errorf("tags %v, want es-ES and es-419", got)
	}
}

// TestSubXVariety: the Spanish variety comes from the description, es-ES
// when it names Spain's Spanish by one of the catalog's keywords or
// "peninsular", es-419 otherwise, "castellano neutro" included. The
// descriptions are the capture's (testdata/README.md).
func TestSubXVariety(t *testing.T) {
	cases := []struct{ description, want string }{
		{"Extraídos de blu-ray - Español Europeo", "es-ES"},
		{"¡Que los disfruten! (Español de España)", "es-ES"},
		{"Los de argenteam transformados a español peninsular", "es-ES"},
		{"Traducción en CASTELLANO para la versión DVDRip", "es-ES"},
		{"Ripeados del DVD original. Español latino, cero errores OCR.", "es-419"},
		{"Corregidos todos los errores de traducción. Castellano neutro.", "es-419"},
		{"Castellano neutro, revisado; doblaje de Castilla", "es-ES"},
		{"", "es-419"},
	}
	type it struct {
		ID          string `json:"id"`
		IMDb        string `json:"imdb_id"`
		Description string `json:"description"`
	}
	items := make([]it, len(cases))
	for i, c := range cases {
		items[i] = it{fmt.Sprintf("00000000-0000-4000-8000-%012d", i), "tt1375666", c.description}
	}
	body, err := json.Marshal(map[string]any{"items": items})
	if err != nil {
		t.Fatal(err)
	}
	synctest.Test(t, func(t *testing.T) {
		s := recorded()
		s.routes[search+"?"+filmByID] = route{body: body}
		ctx := providertest.Serve(t, t.Context(), s, host)
		got, err := adapter(t).Search(ctx, inception("es-ES", "es-419"))
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != len(cases) {
			t.Fatalf("%d candidates, want %d", len(got), len(cases))
		}
		for i, c := range cases {
			if got[i].Lang != tags(c.want)[0] {
				t.Errorf("%q: %v, want %s", c.description, got[i].Lang, c.want)
			}
		}
	})
}
