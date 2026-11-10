package subsource_test

import (
	"cmp"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subsource"
	"github.com/cplieger/subflux/internal/rank"
)

const (
	host   = "api.subsource.net"
	key    = "subsource-key-0123456789"
	values = "api_key: " + key
)

func tag(t *testing.T, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

// recorded serves the recorded search and subtitle lists of Inception.
type recorded struct {
	mu   sync.Mutex
	urls []string
}

func (r *recorded) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	r.urls = append(r.urls, req.URL.Path+"?"+req.URL.RawQuery)
	r.mu.Unlock()
	if req.URL.Query().Get("api_key") != key {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	var file string
	switch q := req.URL.Query(); {
	case req.URL.Path == "/api/v1/movies/search" && q.Get("imdb") == "tt1375666":
		file = "testdata/search-imdb-tt1375666.json"
	case req.URL.Path == "/api/v1/subtitles" && q.Get("movieId") == "46839":
		file = "testdata/subtitles-46839-" + q.Get("language") + ".json"
	default:
		_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
		return
	}
	b, err := os.ReadFile(file)
	if err != nil {
		http.NotFound(w, req)
		return
	}
	_, _ = w.Write(b)
}

func movie(t *testing.T, langs ...string) provider.Query {
	q := provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666}, Titles: []string{"Inception"}, Year: 2010}
	for _, l := range langs {
		q.Langs = append(q.Langs, tag(t, l))
	}
	return q
}

func TestSearchRecorded(t *testing.T) {
	rec := &recorded{}
	ctx := providertest.Serve(t, t.Context(), rec, host)
	got, err := providertest.Build(t, subsource.NewEntry(), values).Search(ctx, movie(t, "en", "fr"))
	if err != nil {
		t.Fatal(err)
	}
	// One candidate per release name: 168 English (subtitle 1062555, whose
	// comment says its foreign parts are hardcoded, a full track) and 12
	// French.
	var en, fr int
	for _, c := range got {
		if c.Flags.Forced {
			t.Errorf("candidate %s marked forced; no recorded file is", c.Ref.Token())
		}
		switch c.Lang {
		case tag(t, "en"):
			en++
		case tag(t, "fr"):
			fr++
		}
		if c.MatchedBy != rank.ID || c.Ref.Token() == "" {
			t.Errorf("candidate %+v", c)
		}
	}
	if en != 168 || fr != 12 {
		t.Errorf("%d English and %d French candidates, want 168 and 12", en, fr)
	}
	if got[0].Ref.Token() != "10215904" || got[0].Release != "Inception.2010.x264 YIFY" {
		t.Errorf("first candidate %+v", got[0])
	}
	if len(rec.urls) != 3 {
		t.Errorf("requests %q, want the title search and two lists", rec.urls)
	}
}

// TestProviderMaps checks the map against the recorded answers and the
// recorded language probe: every label an answer carried maps, and no label
// the filter answered nothing for, and no answer carried, is sent.
func TestProviderMaps(t *testing.T) {
	m := subsource.NewEntry().Languages
	for _, file := range []string{"testdata/subtitles-46839-english.json", "testdata/subtitles-46839-french.json"} {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		var r struct {
			Data []struct {
				Language string `json:"language"`
			} `json:"data"`
		}
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		for _, d := range r.Data {
			if _, ok := m.Tag(d.Language); !ok {
				t.Errorf("%s: label %q maps to no tag", file, d.Language)
			}
		}
	}
	b, err := os.ReadFile("testdata/languages-probe.json")
	if err != nil {
		t.Fatal(err)
	}
	var probe struct {
		Answered map[string]int            `json:"answered_labels"`
		Filter   map[string]map[string]int `json:"filter_by_movie"`
	}
	if err := json.Unmarshal(b, &probe); err != nil {
		t.Fatal(err)
	}
	for label := range probe.Answered {
		if _, ok := m.Tag(label); !ok {
			t.Errorf("answered label %q maps to no tag", label)
		}
	}
	for wire, hits := range probe.Filter {
		found := 0
		for _, n := range hits {
			found += n
		}
		if _, ok := m.Tag(wire); ok && found == 0 && probe.Answered[wire] == 0 {
			t.Errorf("the map sends %q, which filtered nothing and no answer carried", wire)
		}
	}
	for tg, want := range map[string][]string{
		"pt-BR":   {"brazilian_portuguese"},
		"zh-Hant": {"chinese_traditional", "big_5_code"},
		"es-419":  {"spanish_latin_america"},
	} {
		if w := m.Wires(tag(t, tg)); !slices.Equal(w, want) {
			t.Errorf("%s asks %q, want %q", tg, w, want)
		}
	}
}

// TestSubSourceIMDbNoTitleMatch: the IMDb search's hit is taken whatever
// the query's titles, and cached by IMDb id and season, so a second search
// of the same media under other titles sends no title search.
func TestSubSourceIMDbNoTitleMatch(t *testing.T) {
	rec := &recorded{}
	ctx := providertest.Serve(t, t.Context(), rec, host)
	a := providertest.Build(t, subsource.NewEntry(), values)
	q := movie(t, "fr")
	q.Titles = []string{"Origen"}
	got, err := a.Search(ctx, q)
	if err != nil || len(got) != 12 {
		t.Fatalf("%d candidates, %v; want the 12 French ones", len(got), err)
	}
	q.Titles = []string{"El origen", "Inception"}
	if got, err := a.Search(ctx, q); err != nil || len(got) != 12 {
		t.Fatalf("second search: %d candidates, %v; want the 12 French ones", len(got), err)
	}
	var searches int
	for _, u := range rec.urls {
		if strings.HasPrefix(u, "/api/v1/movies/search?") {
			searches++
			if !strings.Contains(u, "searchType=imdb") {
				t.Errorf("title search %q, want the IMDb search only", u)
			}
		}
	}
	if searches != 1 || len(rec.urls) != 3 {
		t.Errorf("requests %q, want one IMDb search and a list per search", rec.urls)
	}
}

// TestSearchTriesEachTitle: an IMDb search finding nothing hands the turn to
// each title's text search, the first title matching no hit giving it to the
// next.
func TestSearchTriesEachTitle(t *testing.T) {
	rec := &recorded{}
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		switch {
		case r.URL.Path != "/api/v1/movies/search":
		case q.Get("searchType") == "imdb":
			_, _ = w.Write([]byte(`{"success":true,"data":[]}`))
			return
		case q.Get("searchType") == "text":
			_, _ = w.Write([]byte(`{"success":true,"data":[{"movieId":46839,"title":"Inception","releaseYear":2010}]}`))
			return
		}
		rec.ServeHTTP(w, r)
	}), host)
	q := movie(t, "fr")
	q.Titles = []string{"Origen", "Inception"}
	got, err := providertest.Build(t, subsource.NewEntry(), values).Search(ctx, q)
	if err != nil || len(got) != 12 {
		t.Fatalf("%d candidates, %v; want the 12 French ones", len(got), err)
	}
	if len(rec.urls) != 1 {
		t.Errorf("lists %q, want one", rec.urls)
	}
}

// TestForcedKept: a file SubSource flags foreignParts, or whose comment
// calls it forced, is kept and marked Forced.
func TestForcedKept(t *testing.T) {
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/subtitles" {
			_, _ = w.Write([]byte(`{"success":true,"data":[
				{"subtitleId":1,"releaseInfo":["Inception.2010.1080p"],"foreignParts":true},
				{"subtitleId":2,"releaseInfo":["Inception.2010.720p"],"commentary":"forced subtitle"},
				{"subtitleId":3,"releaseInfo":["Inception.2010.480p"],"commentary":"full track"}]}`))
			return
		}
		(&recorded{}).ServeHTTP(w, r)
	}), host)
	got, err := providertest.Build(t, subsource.NewEntry(), values).Search(ctx, movie(t, "en"))
	if err != nil || len(got) != 3 {
		t.Fatalf("%d candidates, %v; want all 3", len(got), err)
	}
	for i, want := range []bool{true, true, false} {
		if got[i].Flags.Forced != want {
			t.Errorf("subtitle %s: Forced %v, want %v", got[i].Ref.Token(), got[i].Flags.Forced, want)
		}
	}
}

// TestSearchKeepsReadLists: a list failing after another was read answers
// the read candidates beside its failure.
func TestSearchKeepsReadLists(t *testing.T) {
	rec := &recorded{}
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("language") == "french" {
			_, _ = w.Write([]byte(`{"success":false,"error":"internal error"}`))
			return
		}
		rec.ServeHTTP(w, r)
	}), host)
	got, err := providertest.Build(t, subsource.NewEntry(), values).Search(ctx, movie(t, "en", "fr"))
	if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent || len(got) != 168 {
		t.Errorf("%d candidates, %v; want the 168 English ones and a Permanent failure", len(got), err)
	}
}

// TestSuccessFalseClassified: a 200 list answer with success false is a
// refused key, no results, or a failure, never quoting the key.
func TestSuccessFalseClassified(t *testing.T) {
	for _, tc := range []struct {
		body string
		kind provider.FailureKind // 0: no results
	}{
		{`{"success":false,"error":"Invalid API key ` + key + `"}`, provider.Auth},
		{`{"success":false,"error":"No subtitles found"}`, 0},
		{`{"success":false,"error":"internal error"}`, provider.Permanent},
		{`{"data":[]}`, provider.Permanent},
	} {
		rec := &recorded{}
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/v1/subtitles" {
				_, _ = w.Write([]byte(tc.body))
				return
			}
			rec.ServeHTTP(w, r)
		}), host)
		got, err := providertest.Build(t, subsource.NewEntry(), values).Search(ctx, movie(t, "en"))
		f, _ := errors.AsType[*provider.Failure](err)
		switch {
		case len(got) != 0:
			t.Errorf("%s: %d candidates", tc.body, len(got))
		case tc.kind == 0 && err != nil, tc.kind != 0 && (f == nil || f.Kind != tc.kind):
			t.Errorf("%s: %v, want kind %v", tc.body, err, tc.kind)
		case err != nil && strings.Contains(err.Error(), key):
			t.Errorf("%s: the failure quotes the key: %v", tc.body, err)
		}
	}
}

// TestTitleSearchWithoutSuccessField: the title search's answer may omit
// success, which is then an answer.
func TestTitleSearchWithoutSuccessField(t *testing.T) {
	rec := &recorded{}
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/movies/search" {
			_, _ = w.Write([]byte(`{"data":[{"movieId":46839,"title":"Inception","releaseYear":"2010"}]}`))
			return
		}
		rec.ServeHTTP(w, r)
	}), host)
	got, err := providertest.Build(t, subsource.NewEntry(), values).Search(ctx, movie(t, "fr"))
	if err != nil || len(got) != 12 {
		t.Errorf("%d candidates, %v; want the 12 French ones", len(got), err)
	}
}

func TestSearchSkipsWithoutIMDb(t *testing.T) {
	rec := &recorded{}
	ctx := providertest.Serve(t, t.Context(), rec, host)
	q := movie(t, "en")
	q.External.IMDb = 0
	_, err := providertest.Build(t, subsource.NewEntry(), values).Search(ctx, q)
	if _, ok := errors.AsType[*provider.Skipped](err); !ok || len(rec.urls) != 0 {
		t.Errorf("err %v after %d requests, want *Skipped and none", err, len(rec.urls))
	}
}

func TestRefusedKey(t *testing.T) {
	ctx := providertest.Serve(t, t.Context(), &recorded{}, host)
	a := providertest.Build(t, subsource.NewEntry(), "api_key: wrong")
	_, err := a.Search(ctx, movie(t, "en"))
	if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Auth {
		t.Errorf("search: %v, want Auth", err)
	}
	if err := a.(provider.Tester).Test(ctx); err == nil {
		t.Error("Test passed a refused key")
	}
	if err := providertest.Build(t, subsource.NewEntry(), values).(provider.Tester).Test(ctx); err != nil {
		t.Errorf("Test refused a good key: %v", err)
	}
}

func TestFetch(t *testing.T) {
	var path, gotKey string
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, gotKey = r.URL.Path, r.URL.Query().Get("api_key")
		w.Header().Set("Content-Disposition", `attachment; filename="inception-2010_english_10215904-[SubSource].zip"`)
		_, _ = w.Write([]byte("PK\x03\x04"))
	}), host)
	a := providertest.Build(t, subsource.NewEntry(), values)
	p, err := a.Fetch(ctx, provider.NewRef("10215904"))
	if err != nil {
		t.Fatal(err)
	}
	if path != "/api/v1/subtitles/10215904/download" || gotKey != key || p.Name != "inception-2010_english_10215904-[SubSource].zip" {
		t.Errorf("asked %s with key %q, payload %q", path, gotKey, p.Name)
	}
	for _, tok := range []string{"https://elsewhere.example/x", "../x", "-1", ""} {
		_, err := a.Fetch(ctx, provider.NewRef(tok))
		if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent {
			t.Errorf("token %q: %v, want Permanent", tok, err)
		}
	}
}

// TestSubSourceAsksEveryTraditionalLabel: SubSource files Traditional
// Chinese under two labels, so a zh-Hant search asks each once, and a file
// listed under both is one candidate.
func TestSubSourceAsksEveryTraditionalLabel(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]int{}
	lists := map[string]string{
		"chinese_traditional": `{"success":true,"data":[{"subtitleId":7,"releaseInfo":["Inception.2010.BluRay"]},{"subtitleId":8,"releaseInfo":["Inception.2010.WEB"]}]}`,
		"big_5_code":          `{"success":true,"data":[{"subtitleId":7,"releaseInfo":["Inception.2010.BluRay"]},{"subtitleId":9,"releaseInfo":["Inception.2010.HDTV"]}]}`,
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/api/v1/movies/search" {
			b, err := os.ReadFile("testdata/search-imdb-tt1375666.json")
			if err != nil {
				http.NotFound(w, req)
				return
			}
			_, _ = w.Write(b)
			return
		}
		label := req.URL.Query().Get("language")
		mu.Lock()
		asked[label]++
		mu.Unlock()
		_, _ = w.Write([]byte(cmp.Or(lists[label], `{"success":true,"data":[]}`)))
	})
	ctx := providertest.Serve(t, t.Context(), h, host)
	got, err := providertest.Build(t, subsource.NewEntry(), values).Search(ctx, movie(t, "zh-Hant"))
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]int{"chinese_traditional": 1, "big_5_code": 1}; !maps.Equal(asked, want) {
		t.Errorf("subtitle lists asked %v, want %v", asked, want)
	}
	var ids []string
	for _, c := range got {
		ids = append(ids, c.Ref.Token())
		if c.Lang != tag(t, "zh-Hant") {
			t.Errorf("candidate %s has language %v, want zh-Hant", c.Ref.Token(), c.Lang)
		}
	}
	if want := []string{"7", "8", "9"}; !slices.Equal(ids, want) {
		t.Errorf("candidates %q, want %q: one per file", ids, want)
	}
}
