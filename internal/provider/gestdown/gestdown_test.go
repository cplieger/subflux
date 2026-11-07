package gestdown_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/gestdown"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/rank"
)

const (
	host = "api.gestdown.info"
	show = "1WLU6zJky4nlJQa5oojv5R"
)

func tag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

// fake serves the recorded answers, logging each request path.
type fake struct {
	mu     sync.Mutex
	paths  []string
	french int // status for the French season; 0 serves the recording
}

func (f *fake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.paths = append(f.paths, r.URL.EscapedPath())
	french := f.french
	f.mu.Unlock()
	file := map[string]string{
		"/shows/external/tvdb/81189":  "testdata/shows-tvdb-81189.json",
		"/shows/" + show + "/1/en":    "testdata/season-1-English.json",
		"/shows/" + show + "/1/fr":    "testdata/season-1-French.json",
		"/shows/" + show + "/1/pt-BR": "testdata/season-1-pt-BR.json",
	}[r.URL.EscapedPath()]
	switch {
	case r.URL.EscapedPath() == "/shows/"+show+"/1/fr" && french != 0:
		w.WriteHeader(french)
	case file != "":
		b, err := os.ReadFile(file)
		if err != nil {
			panic(err)
		}
		_, _ = w.Write(b)
	default:
		http.NotFound(w, r)
	}
}

func (f *fake) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.paths)
}

func episode(t *testing.T, langs ...string) provider.Query {
	q := provider.Query{
		Kind:      media.Episode,
		External:  media.ExternalIDs{TVDB: 81189},
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1},
	}
	for _, l := range langs {
		q.Langs = append(q.Langs, tag(t, l))
	}
	return q
}

func TestSearchRecorded(t *testing.T) {
	f := &fake{}
	ctx := providertest.Serve(t, t.Context(), f, host)
	a := providertest.Build(t, gestdown.NewEntry(), "")
	got, err := a.Search(ctx, episode(t, "en"))
	if err != nil {
		t.Fatal(err)
	}
	var refs []string
	for _, c := range got {
		refs = append(refs, c.Ref.Token())
		if c.Lang != tag(t, "en") || c.MatchedBy != rank.ID || c.Title != "" || c.Covers != nil {
			t.Errorf("candidate %+v", c)
		}
	}
	want := []string{"3Df15bUM1njyzL2GHv0PaX", "5pkWMgtuHKfnWq8LTFbTpI", "72Dx379lNuYnCBzdNgKWVg", "5p45mvzz1jBNntcbV3ghQX", "4ENSuReK0y1Q6YfUJRVSbr", "6vEvlp5H52eh9gzLFREXgk"}
	if !slices.Equal(refs, want) {
		t.Errorf("refs %q, want %q", refs, want)
	}
	if got[1].Release != "DVDRip ORPHEUS" {
		t.Errorf("release %q", got[1].Release)
	}
	wantPaths := []string{"/shows/external/tvdb/81189", "/shows/" + show + "/1/en"}
	if p := f.requests(); !slices.Equal(p, wantPaths) {
		t.Errorf("requests %q, want %q", p, wantPaths)
	}
	t.Run("cached", func(t *testing.T) {
		if _, err := a.Search(ctx, episode(t, "en")); err != nil {
			t.Fatal(err)
		}
		if p := f.requests(); len(p) != 2 {
			t.Errorf("a repeated search sent %q", p[2:])
		}
	})
}

func TestSearchSkips(t *testing.T) {
	f := &fake{}
	ctx := providertest.Serve(t, t.Context(), f, host)
	a := providertest.Build(t, gestdown.NewEntry(), "")
	movie := episode(t, "en")
	movie.Kind = media.Movie
	noID := episode(t, "en")
	noID.External.TVDB = 0
	tmdb := episode(t, "en")
	tmdb.Numbering.Scheme = media.TMDB
	for _, q := range []provider.Query{movie, noID, tmdb} {
		_, err := a.Search(ctx, q)
		if _, ok := errors.AsType[*provider.Skipped](err); !ok {
			t.Errorf("%+v: err %v, want *Skipped", q, err)
		}
	}
	if p := f.requests(); len(p) != 0 {
		t.Errorf("sent %q", p)
	}
}

func TestSearchKeepsEarlierLanguages(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		f := &fake{french: http.StatusServiceUnavailable}
		ctx := providertest.Serve(t, t.Context(), f, host)
		got, err := providertest.Build(t, gestdown.NewEntry(), "").Search(ctx, episode(t, "en", "fr"))
		if len(got) != 6 {
			t.Errorf("%d candidates, want the 6 English ones", len(got))
		}
		if fl, ok := errors.AsType[*provider.Failure](err); !ok || fl.Kind != provider.Transient || fl.Status != http.StatusServiceUnavailable {
			t.Errorf("err %v, want the French season's Transient 503", err)
		}
	})
}

func TestLockedIsRateLimited(t *testing.T) {
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusLocked)
	}), host)
	_, err := providertest.Build(t, gestdown.NewEntry(), "").Search(ctx, episode(t, "en"))
	if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.RateLimited || f.RetryAfter != time.Minute {
		t.Errorf("err %v, want RateLimited for 1m", err)
	}
}

func TestFetch(t *testing.T) {
	var path string
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.EscapedPath()
		w.Header().Set("Content-Disposition", `attachment; filename=Breaking.Bad.S01E01.0tv.en.srt`)
		_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
	}), host)
	a := providertest.Build(t, gestdown.NewEntry(), "")
	p, err := a.Fetch(ctx, provider.NewRef("3Df15bUM1njyzL2GHv0PaX"))
	if err != nil {
		t.Fatal(err)
	}
	if path != "/subtitles/download/3Df15bUM1njyzL2GHv0PaX" || p.Name != "Breaking.Bad.S01E01.0tv.en.srt" || len(p.Bytes) == 0 {
		t.Errorf("path %q, payload %q %d bytes", path, p.Name, len(p.Bytes))
	}
	if _, err := a.Fetch(ctx, provider.NewRef("../../shows/x")); err != nil {
		t.Fatal(err)
	}
	if path != "/subtitles/download/..%2F..%2Fshows%2Fx" {
		t.Errorf("a token left its path: %q", path)
	}
}

// TestProviderMaps checks the map against the recorded probe of Gestdown's
// language segment: every code it sends answered 200, and every code the
// probe saw refused is absent.
func TestProviderMaps(t *testing.T) {
	m := gestdown.NewEntry().Languages
	b, err := os.ReadFile("testdata/languages-probe.json")
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Status map[string]int `json:"status"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatal(err)
	}
	for _, tg := range m.Tags() {
		w, ok := m.Wire(tg)
		if back, _ := m.Tag(w); !ok || back != tg {
			t.Errorf("%v does not round-trip through %q", tg, w)
		}
		if probe.Status[w] != http.StatusOK {
			t.Errorf("%v sends %q, which the probe saw answer %d", tg, w, probe.Status[w])
		}
	}
	// The probe also accepted nb and pt-PT, the languages no and pt already
	// ask, and bare zh, which names no script.
	left := []string{"nb", "pt-PT", "zh"}
	for code, st := range probe.Status {
		_, ok := m.Tag(code)
		switch {
		case ok && st != http.StatusOK:
			t.Errorf("the map holds %q, which answered %d", code, st)
		case !ok && st == http.StatusOK && !slices.Contains(left, code):
			t.Errorf("the probe accepted %q, which the map leaves out", code)
		}
	}
}

func TestGestdownSendsBCP47(t *testing.T) {
	f := &fake{}
	ctx := providertest.Serve(t, t.Context(), f, host)
	got, err := providertest.Build(t, gestdown.NewEntry(), "").Search(ctx, episode(t, "pt-BR"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/shows/external/tvdb/81189", "/shows/" + show + "/1/pt-BR"}
	if p := f.requests(); !slices.Equal(p, want) {
		t.Errorf("requests %q, want %q", p, want)
	}
	if len(got) != 2 {
		t.Errorf("%d candidates, want the recorded 2", len(got))
	}
	for _, c := range got {
		if c.Lang != tag(t, "pt-BR") {
			t.Errorf("candidate in %v", c.Lang)
		}
	}
}
