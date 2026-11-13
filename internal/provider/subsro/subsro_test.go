package subsro_test

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/episodematch"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subsro"
	"github.com/cplieger/subflux/internal/rank"
)

// recorded serves the recorded searches and downloads.
func recorded() *site {
	return &site{routes: map[string]route{
		"/v1.0/search/imdbid/tt1375666":     {file: "search-imdbid-tt1375666.json"},
		"/v1.0/search/imdbid/tt0903747":     {file: "search-imdbid-tt0903747.json"},
		"/v1.0/search/imdbid/tt0000001":     {file: "search-imdbid-tt0000001.json"},
		"/v1.0/subtitle/50829/download":     {file: "dl-50829.zip"},
		"/v1.0/subtitle/80010/download":     {file: "dl-80010.rar"},
		"/v1.0/subtitle/999999999/download": {file: "dl-404.json", status: http.StatusNotFound},
		"/v1.0/quota":                       {file: "quota.json"},
	}}
}

func failure(t *testing.T, err error) *provider.Failure {
	t.Helper()
	f, ok := errors.AsType[*provider.Failure](err)
	if !ok {
		t.Fatalf("%v, want a *provider.Failure", err)
	}
	return f
}

// TestSubsRo429OneHour: a 429 without Retry-After pauses Subs.ro an hour,
// its quota being daily with no published reset; with one, 5.1's 429 row
// reads it.
func TestSubsRo429OneHour(t *testing.T) {
	for _, c := range []struct {
		name, retryAfter string
		want             time.Duration
	}{
		{"no Retry-After", "", time.Hour},
		{"Retry-After", "120", 2 * time.Minute},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					if c.retryAfter != "" {
						w.Header().Set("Retry-After", c.retryAfter)
					}
					w.WriteHeader(http.StatusTooManyRequests)
					_, _ = w.Write([]byte(`{"message":"Too many requests","status":429}`))
				}), host)
				_, err := adapter(t).Search(ctx, inception("ro"))
				if f := failure(t, err); f.Kind != provider.RateLimited || f.RetryAfter != c.want {
					t.Errorf("429: %v for %v, want RateLimited for %v", f, f.RetryAfter, c.want)
				}
			})
		})
	}
}

// candidate is what a recorded item's candidate must hold.
type candidate struct {
	ref, lang, release string
	year               int
}

// TestSubsRoByIMDb pins the lookup over the recorded answers: one search by
// IMDb id with the key in its header, then a candidate per item in an
// asked language, named by its first listed release or else its title with
// the season in English.
func TestSubsRoByIMDb(t *testing.T) {
	for _, c := range []struct {
		name string
		q    provider.Query
		path string
		want []candidate
	}{
		{"film", inception("ro", "en"), "/v1.0/search/imdbid/tt1375666", []candidate{
			{"50829", "en", "Inception (2010) - Br-Rip", 2010},
			{"28955", "ro", "Inception", 2010},
			{"27318", "ro", "Inception", 2010},
		}},
		{"film in Romanian", inception("ro"), "/v1.0/search/imdbid/tt1375666", []candidate{
			{"28955", "ro", "Inception", 2010},
			{"27318", "ro", "Inception", 2010},
		}},
		{"film filed twice", provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1}, Langs: tags("ro", "en")}, "/v1.0/search/imdbid/tt0000001", []candidate{
			{"50997", "en", "Carmencita", 1894},
			{"32452", "ro", "End of Liberty", 2010},
		}},
		{"series", breakingBad("ro", "en", "fr", "es"), "/v1.0/search/imdbid/tt0903747", []candidate{
			{"80014", "ro", "Breaking Bad - Season 5", 2008},
			{"80013", "ro", "Breaking Bad - Season 4", 2008},
			{"80012", "ro", "Breaking Bad - Season 3", 2008},
			{"80011", "ro", "Breaking Bad - Season 2", 2008},
			{"80010", "ro", "Breaking Bad - Season 1", 2008},
			{"69327", "en", "breaking.bad.s01e06.720p.brrip.x264-visionx", 2008},
			{"55580", "fr", "Breaking.Bad.S01.1080p.Blu-ray.AVC.DTS-HD.MA.5.1-BluHD", 2008},
			{"48543", "es", "Breaking Bad Season 1 Complete 720p.BRrip.Sujaidr", 2008},
			{"46281", "en", "BDRip DVDRip HDTV TSV", 2008},
			{"41544", "ro", "Breaking Bad - Season 5", 2008},
			{"35878", "ro", "Breaking Bad - Season 5", 2008},
			{"31601", "ro", "Breaking Bad - Season 4", 2008},
			{"25326", "ro", "Breaking Bad - Season 3", 2008},
			{"23408", "ro", "Breaking Bad - Season 1", 2008},
			{"18624", "ro", "Breaking Bad - Season 2", 2008},
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := recorded()
				ctx := providertest.Serve(t, t.Context(), s, host)
				got, err := adapter(t).Search(ctx, c.q)
				if err != nil {
					t.Fatalf("search: %v", err)
				}
				reqs := s.seen()
				if len(reqs) != 1 || reqs[0].URL.Path != c.path {
					t.Fatalf("asked %q, want only %s", s.paths(), c.path)
				}
				if r := reqs[0]; r.Header.Get("X-Subs-Api-Key") != key || r.URL.RawQuery != "" || strings.Contains(r.URL.String(), key) {
					t.Errorf("%s: X-Subs-Api-Key %q, query %q; want the key in its header and never in the URL", r.URL.Path, r.Header.Get("X-Subs-Api-Key"), r.URL.RawQuery)
				}
				if len(got) != len(c.want) {
					t.Fatalf("%d candidates, want %d", len(got), len(c.want))
				}
				for i, w := range c.want {
					g := &got[i]
					if g.Ref.Token() != w.ref || g.Lang != tags(w.lang)[0] || g.Release != w.release || g.Year != w.year || g.MatchedBy != rank.ID || g.Covers != nil || g.Title != "" {
						t.Errorf("candidate %d: %s %v %q %d matched by %v covers %v, want %s %s %q %d matched by id with no Covers", i, g.Ref.Token(), g.Lang, g.Release, g.Year, g.MatchedBy, g.Covers, w.ref, w.lang, w.release, w.year)
					}
				}
			})
		})
	}
}

// TestSubsRoNoIMDbNoRequest: a query with no IMDb id is skipped unsent.
func TestSubsRoNoIMDbNoRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := recorded()
		ctx := providertest.Serve(t, t.Context(), s, host)
		q := breakingBad("ro")
		q.External.IMDb = 0
		if _, err := adapter(t).Search(ctx, q); !isSkipped(err) {
			t.Errorf("no IMDb id: %v, want a Skipped", err)
		}
		if n := len(s.seen()); n != 0 {
			t.Errorf("%d requests sent, want none", n)
		}
	})
}

func isSkipped(err error) bool {
	_, ok := errors.AsType[*provider.Skipped](err)
	return ok
}

// TestSubsRoAnswers: the recorded 400 is Permanent, an undecodable 200 too,
// and a recorded 404 download NotFound.
func TestSubsRoAnswers(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), &site{routes: map[string]route{
			"/v1.0/search/imdbid/tt1375666":     {file: "search-imdbid-bad.json", status: http.StatusBadRequest},
			"/v1.0/search/imdbid/tt0903747":     {file: "dl-50829.zip"},
			"/v1.0/subtitle/999999999/download": {file: "dl-404.json", status: http.StatusNotFound},
		}}, host)
		a := adapter(t)
		_, err := a.Search(ctx, inception("ro"))
		if f := failure(t, err); f.Kind != provider.Permanent {
			t.Errorf("400: %v, want Permanent", f)
		}
		_, err = a.Search(ctx, breakingBad("ro"))
		if f := failure(t, err); f.Kind != provider.Permanent {
			t.Errorf("undecodable 200: %v, want Permanent", f)
		}
		_, err = a.Fetch(ctx, provider.NewRef("999999999"))
		if f := failure(t, err); f.Kind != provider.NotFound {
			t.Errorf("404 download: %v, want NotFound", f)
		}
	})
}

// TestSubsRoFetch: Fetch asks the id's download on api.subs.ro with the key
// and returns the archive as served, so Extract hands 8.11's choice the
// members; a downloadLink naming another host is never followed, and a Ref
// that is no subtitle id sends nothing.
func TestSubsRoFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		away := &site{}
		ctx := providertest.Serve(t, t.Context(), away, foreign)
		s := recorded()
		ctx = providertest.Serve(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/v1.0/search/imdbid/tt0903747" {
				_, _ = w.Write([]byte(`{"count":1,"items":[{"id":80010,"title":"Breaking Bad - Sezonul 1","language":"ro",` +
					`"downloadLink":"https://` + foreign + `/v1.0/subtitle/80010/download"}],"status":200}`))
				return
			}
			s.ServeHTTP(w, r)
		}), host)
		a := adapter(t)
		got, err := a.Search(ctx, breakingBad("ro"))
		if err != nil || len(got) != 1 {
			t.Fatalf("search: %d candidates, %v", len(got), err)
		}
		rar, err := a.Fetch(ctx, got[0].Ref)
		if err != nil {
			t.Fatalf("fetch: %v", err)
		}
		reqs := s.seen()
		if len(reqs) != 1 || reqs[0].URL.Path != "/v1.0/subtitle/80010/download" || reqs[0].Header.Get("X-Subs-Api-Key") != key {
			t.Errorf("asked %q, want the id's download with the key", s.paths())
		}
		want, err := os.ReadFile("testdata/dl-80010.rar")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(rar, want) {
			t.Fatalf("fetch: %d bytes, want the %d recorded", len(rar), len(want))
		}
		var chosen string
		_, err = archive.Extract(rar, func(names []string) (int, error) {
			ch := episodematch.Select(&episodematch.Input{Series: season(1, 7), Members: members(names), Item: 2, Variant: language.Standard})
			if ch.Member < 0 {
				return 0, errors.New("no member")
			}
			chosen = names[ch.Member]
			return ch.Member, nil
		})
		if err != nil || !strings.HasPrefix(chosen, "Breaking.Bad.S01E03.") {
			t.Errorf("the rar for S01E03: took %q (%v), want an S01E03 member", chosen, err)
		}
		zip, err := a.Fetch(ctx, provider.NewRef("50829"))
		if err != nil {
			t.Fatalf("fetch the zip: %v", err)
		}
		var listed []string
		if _, err := archive.Extract(zip, func(names []string) (int, error) { listed = names; return 0, nil }); err != nil || !slices.Equal(listed, []string{"Inception (2010).Eng.srt"}) {
			t.Errorf("the zip offers %q (%v), want its one member", listed, err)
		}
		for _, tok := range []string{"https://" + foreign + "/v1.0/subtitle/1/download", "1/../../quota", "0", "-1", "080010", ""} {
			_, err := a.Fetch(ctx, provider.NewRef(tok))
			if f := failure(t, err); f.Kind != provider.Permanent {
				t.Errorf("fetch %q: %v, want Permanent", tok, f)
			}
		}
		if n := len(s.seen()); n != 2 {
			t.Errorf("%d requests reached api.subs.ro, want the two downloads", n)
		}
		if n := len(away.seen()); n != 0 {
			t.Errorf("the foreign host answered %d requests", n)
		}
	})
}

// TestSubsRoEmptyArchive: a download answering no bytes is Permanent.
func TestSubsRoEmptyArchive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), host)
		_, err := adapter(t).Fetch(ctx, provider.NewRef("80010"))
		if f := failure(t, err); f.Kind != provider.Permanent {
			t.Errorf("empty download: %v, want Permanent", f)
		}
	})
}

// TestSubsRoTester: Test asks the key's quota, which spends none of it, and
// the recorded refusal of a wrong key is Auth.
func TestSubsRoTester(t *testing.T) {
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
		if p := s.paths(); !slices.Equal(p, []string{"/v1.0/quota"}) || s.seen()[0].Header.Get("X-Subs-Api-Key") != key {
			t.Errorf("asked %q, want one keyed quota request", p)
		}
		refused := providertest.Serve(t, t.Context(), &site{routes: map[string]route{"/v1.0/quota": {file: "quota-403.json", status: http.StatusForbidden}}}, host)
		if f := failure(t, adapter(t).(provider.Tester).Test(refused)); f.Kind != provider.Auth {
			t.Errorf("wrong key: %v, want Auth", f)
		}
	})
}

// TestProviderMaps: every label Subs.ro's OpenAPI document lists maps but
// "alt", and every recorded item's label is among them.
func TestProviderMaps(t *testing.T) {
	m := subsro.NewEntry().Languages
	for label, want := range map[string]string{"ro": "ro", "en": "en", "ita": "it", "fra": "fr", "ger": "de", "ung": "hu", "gre": "el", "por": "pt", "spa": "es"} {
		if tg, ok := m.Tag(label); !ok || tg != tags(want)[0] {
			t.Errorf("label %q maps to %v, %t, want %s", label, tg, ok, want)
		}
	}
	if _, ok := m.Tag("alt"); ok {
		t.Error(`"alt" maps to a tag`)
	}
	for _, f := range []string{"search-imdbid-tt1375666.json", "search-imdbid-tt0903747.json"} {
		b, err := os.ReadFile("testdata/" + f)
		if err != nil {
			t.Fatal(err)
		}
		for _, l := range recordedLabel.FindAllSubmatch(b, -1) {
			if _, ok := m.Tag(string(l[1])); !ok {
				t.Errorf("%s: recorded label %q is unmapped", f, l[1])
			}
		}
	}
}

var recordedLabel = regexp.MustCompile(`"language":"([^"]*)"`)

// season is a series of one season of n episodes numbered s.
func season(s, n int) episodematch.Series {
	items := make([]mediasource.Item, n)
	for i := range items {
		items[i].Numbering = media.Numbering{Scheme: media.TVDB, Season: s, Episode: i + 1}
		items[i].SeriesNumbering = items[i].Numbering
	}
	return episodematch.Series{Items: items}
}

func members(names []string) []episodematch.Member {
	out := make([]episodematch.Member, len(names))
	for i, n := range names {
		out[i] = episodematch.Member{Name: n}
	}
	return out
}
