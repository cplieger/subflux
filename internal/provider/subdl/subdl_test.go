package subdl_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subdl"
	"github.com/cplieger/subflux/internal/rank"
)

const (
	apiHost = "api.subdl.com"
	dlHost  = "dl.subdl.com"
	key     = "test-key-0123456789"
	values  = "api_key: " + key
)

func tags(t *testing.T, ss ...string) []language.Tag {
	t.Helper()
	var out []language.Tag
	for _, s := range ss {
		tg, ok := language.Parse(s)
		if !ok {
			t.Fatalf("tag %q", s)
		}
		out = append(out, tg)
	}
	return out
}

// recorder serves one file for every request and keeps the last request.
type recorder struct {
	mu     sync.Mutex
	last   *http.Request
	n      int
	file   string
	status int
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.last, r.n = req, r.n+1
	file, code := r.file, r.status
	r.mu.Unlock()
	if code != 0 {
		w.WriteHeader(code)
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			panic(err)
		}
		_, _ = w.Write(b)
	}
}

func (r *recorder) query() url.Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last.URL.Query()
}

func adapter(t *testing.T) provider.Adapter {
	return providertest.Build(t, subdl.NewEntry(), values)
}

func TestSearchRecordedMovie(t *testing.T) {
	rec := &recorder{file: "testdata/search-movie-tt1375666.json"}
	ctx := providertest.Serve(t, t.Context(), rec, apiHost)
	got, err := adapter(t).Search(ctx, provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666, TMDB: 27205}, Titles: []string{"Inception"}, Langs: tags(t, "en", "fr")})
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{
		"api_key": {key}, "comment": {"1"}, "imdb_id": {"tt1375666"},
		"languages": {"EN,FR"}, "releases": {"1"}, "subs_per_page": {"30"}, "type": {"movie"},
	}
	if q := rec.query(); q.Encode() != want.Encode() {
		t.Errorf("query %s, want %s", q.Encode(), want.Encode())
	}
	if len(got) != 59 {
		t.Fatalf("%d candidates, want the 30 files' 59 release names", len(got))
	}
	// The first file is not flagged hi, but its release name says SDH; the
	// answer flags 9 of its 30 files, the 48th release is the French file's.
	c := got[0]
	if c.Ref.Token() != "/subtitle/3467330-8390389.zip" || c.Release != "Inception.2010.1080p.BluRay.x265-YAWNTiC_eng SDH" ||
		c.Lang != tags(t, "en")[0] || !c.Flags.HI || c.MatchedBy != rank.ID {
		t.Errorf("first candidate %+v", c)
	}
	if got[47].Lang != tags(t, "fr")[0] {
		t.Errorf("48th candidate's language %v, want fr", got[47].Lang)
	}
	var hi int
	for _, c := range got {
		if c.Flags.HI {
			hi++
		}
	}
	if hi < 9 || hi == len(got) {
		t.Errorf("%d of %d candidates HI, want the 9 the answer flags and the release names add, not every file", hi, len(got))
	}
	for _, c := range got {
		if strings.Contains(c.Ref.Token(), key) || strings.Contains(c.Ref.Token(), "api_key") {
			t.Fatalf("a token keeps the answer's query: %q", c.Ref.Token())
		}
	}
}

func TestSearchRecordedEpisode(t *testing.T) {
	rec := &recorder{file: "testdata/search-tv-tt0903747-s01e01.json"}
	ctx := providertest.Serve(t, t.Context(), rec, apiHost)
	got, err := adapter(t).Search(ctx, provider.Query{
		Kind: media.Episode, External: media.ExternalIDs{IMDb: 903747, TVDB: 81189},
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}, Langs: tags(t, "en", "fr"),
	})
	if err != nil {
		t.Fatal(err)
	}
	q := rec.query()
	if q.Get("type") != "tv" || q.Get("season_number") != "1" || q.Get("episode_number") != "1" || q.Get("imdb_id") != "tt0903747" {
		t.Errorf("query %s", q.Encode())
	}
	want := []string{
		"Breaking.Bad.S01E01.480p.BRRip.x264-BoB", "breaking.bad.s01e01.720p.brrip.x264-visionx",
		"---Breaking Bad S1 EP01--", "breaking.bad.s1e1.720p.brrip.x264-visionx",
	}
	var rels []string
	for _, c := range got {
		rels = append(rels, c.Release)
	}
	if !slices.Equal(rels, want) {
		t.Errorf("releases %q, want the three single-episode files' four release names, the 25 season packs dropped", rels)
	}
}

func TestSearchSkipsWithoutIdentifier(t *testing.T) {
	rec := &recorder{}
	ctx := providertest.Serve(t, t.Context(), rec, apiHost)
	_, err := adapter(t).Search(ctx, provider.Query{Kind: media.Movie, Langs: tags(t, "en")})
	if _, ok := errors.AsType[*provider.Skipped](err); !ok || rec.n != 0 {
		t.Errorf("err %v after %d requests, want *Skipped and none", err, rec.n)
	}
	got, err := adapter(t).Search(ctx, provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1}, Langs: tags(t, "tlh")})
	if err != nil || len(got) != 0 || rec.n != 0 {
		t.Errorf("an unmapped language: %v, %v after %d requests", got, err, rec.n)
	}
}

func TestStatusFalse(t *testing.T) {
	cases := []struct {
		body string
		kind provider.FailureKind
	}{
		{`{"status":false,"error":"Not Authorized"}`, provider.Auth},
		{`{"status":false,"error":"invalid API key"}`, provider.Auth},
		{`{"status":false,"error":"Can't find movie"}`, 0},
		{`{"status":false,"error":""}`, 0},
		{`{"status":false,"error":"server overloaded"}`, provider.Permanent},
		{`{"status":true,"error":"Not Authorized","subtitles":[]}`, 0},
		{`not json`, provider.Permanent},
	}
	for _, tc := range cases {
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(tc.body))
		}), apiHost)
		got, err := adapter(t).Search(ctx, provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1}, Langs: tags(t, "en")})
		f, isFailure := errors.AsType[*provider.Failure](err)
		switch {
		case tc.kind == 0 && (err != nil || len(got) != 0):
			t.Errorf("%s: %v, %v; want no answer and no error", tc.body, got, err)
		case tc.kind != 0 && (!isFailure || f.Kind != tc.kind || strings.Contains(f.Msg, key)):
			t.Errorf("%s: err %v, want %s", tc.body, err, tc.kind)
		}
	}
}

func TestRefusedKeyIsAuth(t *testing.T) {
	ctx := providertest.Serve(t, t.Context(), &recorder{file: "testdata/search-403.json", status: http.StatusForbidden}, apiHost)
	_, err := adapter(t).Search(ctx, provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666}, Langs: tags(t, "en")})
	if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Auth || f.Status != http.StatusForbidden {
		t.Errorf("err %v, want Auth 403", err)
	}
}

func TestDownloadLimitQuirk(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, tc := range []struct {
			body string
			kind provider.FailureKind
		}{{`{"error":"limit"}`, provider.NotFound}, {strings.Repeat("x", 100), provider.Transient}} {
			ctx := providertest.Serve(t, t.Context(), &recorder{status: http.StatusInternalServerError, file: writeTemp(t, tc.body)}, dlHost)
			_, err := adapter(t).Fetch(ctx, provider.NewRef("/subtitle/1-2.zip"))
			if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != tc.kind {
				t.Errorf("a 500 of %d bytes: err %v, want %s", len(tc.body), err, tc.kind)
			}
		}
	})
}

func writeTemp(t *testing.T, s string) string {
	t.Helper()
	p := t.TempDir() + "/body"
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestFetch(t *testing.T) {
	var got *http.Request
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		w.Header().Set("Content-Disposition", `attachment; filename*=UTF-8''SUBDL%3A%3Ainception-2714566.zip`)
		_, _ = w.Write([]byte("PK\x03\x04"))
	}), dlHost)
	a := adapter(t)
	p, err := a.Fetch(ctx, provider.NewRef("/subtitle/2812133-2714566.zip"))
	if err != nil {
		t.Fatal(err)
	}
	if got.URL.Path != "/subtitle/2812133-2714566.zip" || got.URL.Query().Get("api_key") != key || p.Name != "SUBDL::inception-2714566.zip" || string(p.Bytes) != "PK\x03\x04" {
		t.Errorf("request %s, payload %q %q", got.URL, p.Name, p.Bytes)
	}
	if _, err := a.Fetch(ctx, provider.NewRef("//elsewhere.example/x")); err != nil {
		t.Fatalf("a token naming another host left dl.subdl.com: %v", err)
	}
	if got.Host != dlHost || got.URL.Path != "//elsewhere.example/x" {
		t.Errorf("request %s on %s", got.URL, got.Host)
	}
}

func TestTester(t *testing.T) {
	for _, tc := range []struct {
		status int
		kind   provider.FailureKind
	}{{http.StatusOK, 0}, {http.StatusForbidden, provider.Auth}, {http.StatusUnauthorized, provider.Auth}} {
		var path, gotKey string
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			path, gotKey = r.URL.Path, r.URL.Query().Get("api_key")
			w.WriteHeader(tc.status)
		}), apiHost)
		tester, ok := adapter(t).(provider.Tester)
		if !ok {
			t.Fatal("SubDL's adapter is no Tester")
		}
		err := tester.Test(ctx)
		if path != "/api/v1/me" || gotKey != key {
			t.Errorf("asked %s with key %q", path, gotKey)
		}
		if f, isFailure := errors.AsType[*provider.Failure](err); (tc.kind == 0) != (err == nil) || (tc.kind != 0 && (!isFailure || f.Kind != tc.kind)) {
			t.Errorf("status %d: err %v, want kind %d", tc.status, err, tc.kind)
		}
	}
}

func TestProviderMaps(t *testing.T) {
	m := subdl.NewEntry().Languages
	for _, file := range []string{"testdata/search-movie-tt1375666.json", "testdata/search-tv-tt0903747-s01e01.json"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Subtitles []struct {
				Language string `json:"language"`
			} `json:"subtitles"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		for _, s := range r.Subtitles {
			if _, ok := m.Tag(s.Language); !ok {
				t.Errorf("%s: label %q maps to no tag", file, s.Language)
			}
		}
	}
	for _, tg := range m.Tags() {
		w, ok := m.Wire(tg)
		if back, _ := m.Tag(w); !ok || back != tg {
			t.Errorf("%v does not round-trip through %q", tg, w)
		}
	}
	if w, _ := m.Wire(tags(t, "pt-BR")[0]); w != "BR_PT" {
		t.Errorf("pt-BR asks %q, want BR_PT", w)
	}
}

// TestForcedKept: a file whose comment calls it forced is kept and marked.
func TestForcedKept(t *testing.T) {
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":true,"subtitles":[
			{"language":"EN","url":"/subtitle/1-1.zip","releases":["Inception.2010.1080p"],"comment":"forced subs only"},
			{"language":"EN","url":"/subtitle/2-2.zip","releases":["Inception.2010.720p"],"comment":"full track"}]}`))
	}), apiHost)
	got, err := adapter(t).Search(ctx, provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666}, Langs: tags(t, "en")})
	if err != nil || len(got) != 2 {
		t.Fatalf("%d candidates, %v; want both", len(got), err)
	}
	if !got[0].Flags.Forced || got[1].Flags.Forced {
		t.Errorf("Forced %v and %v, want true and false", got[0].Flags.Forced, got[1].Flags.Forced)
	}
}
