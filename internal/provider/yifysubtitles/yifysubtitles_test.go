package yifysubtitles_test

import (
	"errors"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/yifysubtitles"
	"github.com/cplieger/subflux/internal/rank"
)

const host = "yifysubtitles.ch"

func tag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

func file(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		b, err := os.ReadFile(name)
		if err != nil {
			panic(err)
		}
		_, _ = w.Write(b)
	}
}

func movie(t *testing.T, langs ...string) provider.Query {
	q := provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666}}
	for _, l := range langs {
		q.Langs = append(q.Langs, tag(t, l))
	}
	return q
}

func TestSearchRecorded(t *testing.T) {
	var path, ua string
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", file("testdata/robots.txt"))
	mux.HandleFunc("/movie-imdb/tt1375666", func(w http.ResponseWriter, r *http.Request) {
		path, ua = r.URL.Path, r.UserAgent()
		file("testdata/movie-imdb-tt1375666.html")(w, r)
	})
	ctx := providertest.Serve(t, t.Context(), mux, host)
	got, err := providertest.Build(t, yifysubtitles.NewEntry(), "").Search(ctx, movie(t, "en", "pt-BR"))
	if err != nil {
		t.Fatal(err)
	}
	if path == "" || !strings.HasPrefix(ua, "Mozilla/5.0") {
		t.Errorf("asked %q with agent %q, want the movie page with the browser agent", path, ua)
	}
	var en, br int
	for _, c := range got {
		switch c.Lang {
		case tag(t, "en"):
			en++
		case tag(t, "pt-BR"):
			br++
		default:
			t.Errorf("a candidate in %v, which was not asked", c.Lang)
		}
		if c.MatchedBy != rank.ID || c.Ref.Token() == "" || c.Ref.Token()[0] != '/' {
			t.Errorf("candidate %+v", c)
		}
	}
	// Three English rows, and three Brazilian Portuguese ones with two more
	// under its misspelt label.
	if en != 3 || br != 5 {
		t.Errorf("%d English and %d Brazilian candidates, want 3 and 5", en, br)
	}
}

func TestSearchSkipsAndMisses(t *testing.T) {
	ctx := providertest.Serve(t, t.Context(), http.NotFoundHandler(), host)
	a := providertest.Build(t, yifysubtitles.NewEntry(), "")
	ep := movie(t, "en")
	ep.Kind = media.Episode
	noID := movie(t, "en")
	noID.External.IMDb = 0
	for _, q := range []provider.Query{ep, noID} {
		if _, err := a.Search(ctx, q); func() bool { _, ok := errors.AsType[*provider.Skipped](err); return !ok }() {
			t.Errorf("%+v: err %v, want *Skipped", q, err)
		}
	}
	got, err := a.Search(ctx, movie(t, "en"))
	if err != nil || len(got) != 0 {
		t.Errorf("a missing movie page: %v, %v; want no answer and no error", got, err)
	}
}

func TestFetchReadsTheDownloadButton(t *testing.T) {
	var referer, ua string
	mux := http.NewServeMux()
	mux.HandleFunc("/robots.txt", file("testdata/robots.txt"))
	mux.HandleFunc("/subtitles/inception-2010-english-yify-244676", file("testdata/subtitle-page.html"))
	mux.HandleFunc("/subtitle/inception-2010-english-yify-244676.zip", func(w http.ResponseWriter, r *http.Request) {
		referer, ua = r.Referer(), r.UserAgent()
		_, _ = w.Write([]byte("PK\x03\x04"))
	})
	ctx := providertest.Serve(t, t.Context(), mux, host)
	a := providertest.Build(t, yifysubtitles.NewEntry(), "")
	p, err := a.Fetch(ctx, provider.NewRef("/subtitles/inception-2010-english-yify-244676"))
	if err != nil {
		t.Fatal(err)
	}
	if p.Name != "inception-2010-english-yify-244676.zip" || string(p.Bytes) != "PK\x03\x04" ||
		referer != "https://"+host+"/subtitles/inception-2010-english-yify-244676" || !strings.HasPrefix(ua, "Mozilla/5.0") {
		t.Errorf("payload %q, referer %q, agent %q", p.Name, referer, ua)
	}
	_, err = a.Fetch(ctx, provider.NewRef("/subtitles/gone"))
	if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.NotFound {
		t.Errorf("a missing subtitle page: %v, want NotFound", err)
	}
}

func TestProviderMaps(t *testing.T) {
	m := yifysubtitles.NewEntry().Languages
	b, err := os.ReadFile("testdata/movie-imdb-tt1375666.html")
	if err != nil {
		t.Fatal(err)
	}
	labels := regexp.MustCompile(`<span class="sub-lang">([^<]+)</span>`).FindAllStringSubmatch(string(b), -1)
	if len(labels) == 0 {
		t.Fatal("the recording holds no language labels")
	}
	for _, l := range labels {
		if _, ok := m.Tag(l[1]); !ok && l[1] != "Brazillian Portuguese" {
			t.Errorf("label %q maps to no tag", l[1])
		}
	}
	for _, tg := range m.Tags() {
		w, ok := m.Wire(tg)
		if back, _ := m.Tag(w); !ok || back != tg {
			t.Errorf("%v does not round-trip through %q", tg, w)
		}
	}
	if w, _ := m.Wire(tag(t, "zh-Hant")); w != "Big 5 code" {
		t.Errorf("zh-Hant asks %q", w)
	}
}
