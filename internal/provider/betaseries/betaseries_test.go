package betaseries_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/betaseries"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/rank"
)

const (
	apiHost  = "api.betaseries.com"
	siteHost = "www.betaseries.com"
	subHost  = "subtitles.betaseries.com"
	key      = "beta-key-0123456789"
	values   = "token: " + key
)

func tag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

func episode(t *testing.T, langs ...string) provider.Query {
	q := provider.Query{Kind: media.Episode, External: media.ExternalIDs{TVDB: 81189}, Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}}
	for _, l := range langs {
		q.Langs = append(q.Langs, tag(t, l))
	}
	return q
}

func serveFile(t *testing.T, status int, file string, got *http.Request) http.HandlerFunc {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	return func(w http.ResponseWriter, r *http.Request) {
		*got = *r
		w.WriteHeader(status)
		_, _ = w.Write(b)
	}
}

func TestSearchRecorded(t *testing.T) {
	var req http.Request
	ctx := providertest.Serve(t, t.Context(), serveFile(t, http.StatusOK, "testdata/episodes-81189-s01e01.json", &req), apiHost)
	got, err := providertest.Build(t, betaseries.NewEntry(), values).Search(ctx, episode(t, "en"))
	if err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/shows/episodes" || req.URL.RawQuery != "episode=1&season=1&subtitles=1&thetvdb_id=81189&v=3.0" || req.Header.Get("X-BetaSeries-Key") != key {
		t.Errorf("request %s with key %q", req.URL, req.Header.Get("X-BetaSeries-Key"))
	}
	// 15 VO entries, none from the dead seriessub source; VOVF maps to nothing.
	if len(got) != 15 {
		t.Fatalf("%d candidates, want 15", len(got))
	}
	c := got[0]
	if c.Ref.Token() != "748977" || c.Release != "breaking.bad.s01.e01.pilot.(2008).eng.1cd.(7580711).zip" || c.Lang != tag(t, "en") || c.MatchedBy != rank.ID {
		t.Errorf("first candidate %+v", c)
	}
	fr, err := providertest.Build(t, betaseries.NewEntry(), values).Search(ctx, episode(t, "fr"))
	if err != nil || len(fr) != 9 {
		t.Errorf("French: %d candidates, %v; want 9, the seriessub one dropped", len(fr), err)
	}
}

func TestSearchSkips(t *testing.T) {
	var req http.Request
	ctx := providertest.Serve(t, t.Context(), serveFile(t, http.StatusOK, "testdata/episodes-81189-s01e01.json", &req), apiHost)
	a := providertest.Build(t, betaseries.NewEntry(), values)
	movie := episode(t, "en")
	movie.Kind = media.Movie
	noID := episode(t, "en")
	noID.External.TVDB = 0
	for _, q := range []provider.Query{movie, noID} {
		if _, err := a.Search(ctx, q); func() bool { _, ok := errors.AsType[*provider.Skipped](err); return !ok }() {
			t.Errorf("%+v: err %v, want *Skipped", q, err)
		}
	}
	if req.URL != nil {
		t.Errorf("sent %s", req.URL)
	}
}

func TestErrorAnswers(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		kind   provider.FailureKind
	}{
		{"a refused key", http.StatusBadRequest, "testdata/error-1001.json", provider.Auth},
		{"an unknown series", http.StatusBadRequest, "", provider.NotFound},
	}
	bodies := map[string]string{
		"an unknown series": `{"errors":[{"code":4001,"text":"Série introuvable."}]}`,
	}
	for _, tc := range cases {
		file := tc.body
		if file == "" {
			file = t.TempDir() + "/body"
			if err := os.WriteFile(file, []byte(bodies[tc.name]), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var req http.Request
		ctx := providertest.Serve(t, t.Context(), serveFile(t, tc.status, file, &req), apiHost)
		_, err := providertest.Build(t, betaseries.NewEntry(), values).Search(ctx, episode(t, "en"))
		f, ok := errors.AsType[*provider.Failure](err)
		if !ok || f.Kind != tc.kind || strings.Contains(f.Error(), key) {
			t.Errorf("%s: err %v, want %s with the key redacted", tc.name, err, tc.kind)
		}
	}
}

func TestBetaSeriesErrorsArrayFails(t *testing.T) {
	var req http.Request
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req = *r
		_, _ = w.Write([]byte(`{"episodes":[],"errors":[{"code":2001,"text":"Argument manquant: ` + key + `"}]}`))
	}), apiHost)
	got, err := providertest.Build(t, betaseries.NewEntry(), values).Search(ctx, episode(t, "en"))
	f, ok := errors.AsType[*provider.Failure](err)
	if !ok || f.Kind != provider.Permanent || len(got) != 0 {
		t.Fatalf("got %d candidates, err %v; want a Permanent failure", len(got), err)
	}
	if !strings.Contains(f.Msg, "2001") || strings.Contains(f.Error(), key) {
		t.Errorf("failure %q, want the error code with the key redacted", f.Error())
	}
	if req.URL == nil {
		t.Error("no request reached BetaSeries")
	}
}

func TestTester(t *testing.T) {
	var req http.Request
	ctx := providertest.Serve(t, t.Context(), serveFile(t, http.StatusBadRequest, "testdata/error-1001.json", &req), apiHost)
	tester, ok := providertest.Build(t, betaseries.NewEntry(), values).(provider.Tester)
	if !ok {
		t.Fatal("BetaSeries' adapter is no Tester")
	}
	err := tester.Test(ctx)
	if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Auth || req.URL.Path != "/status" {
		t.Errorf("err %v on %s, want Auth on /status", err, req.URL.Path)
	}
}

func TestFetchFollowsSrtLink(t *testing.T) {
	const file = "/opensubtitles/breaking.bad.s01.e01.pilot.(2008).eng.1cd.(7580711).zip"
	var hops []string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hops = append(hops, r.Host+r.URL.Path)
		if r.Header.Get("X-BetaSeries-Key") != "" {
			t.Error("the key went to a download host")
		}
		if r.Host == siteHost {
			http.Redirect(w, r, "https://"+subHost+file, http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("PK\x03\x04"))
	})
	ctx := providertest.Serve(t, t.Context(), h, siteHost, subHost)
	p, err := providertest.Build(t, betaseries.NewEntry(), values).Fetch(ctx, provider.NewRef("748977"))
	if err != nil {
		t.Fatal(err)
	}
	if len(hops) != 2 || hops[0] != siteHost+"/srt/748977" || hops[1] != subHost+file || p.Name != "breaking.bad.s01.e01.pilot.(2008).eng.1cd.(7580711).zip" {
		t.Errorf("hops %q, payload %q", hops, p.Name)
	}
	t.Run("a redirect off the recorded hosts is refused", func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://dl.elsewhere.example/x.zip", http.StatusFound)
		}), siteHost)
		_, err := providertest.Build(t, betaseries.NewEntry(), values).Fetch(ctx, provider.NewRef("1"))
		if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent || f.Host != "dl.elsewhere.example" {
			t.Errorf("err %v, want Permanent on dl.elsewhere.example", err)
		}
	})
}

func TestProviderMaps(t *testing.T) {
	m := betaseries.NewEntry().Languages
	b, err := os.ReadFile("testdata/episodes-81189-s01e01.json")
	if err != nil {
		t.Fatal(err)
	}
	var r struct {
		Episodes []struct {
			Subtitles []struct {
				Language string `json:"language"`
			} `json:"subtitles"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatal(err)
	}
	for _, s := range r.Episodes[0].Subtitles {
		// VOVF marks a file holding both languages, which no one tag names.
		if _, ok := m.Tag(s.Language); !ok && s.Language != "VOVF" {
			t.Errorf("label %q maps to no tag", s.Language)
		}
	}
}
