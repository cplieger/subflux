package subx_test

import (
	"net/http"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subx"
)

const (
	host    = "subx-api.duckdns.org"
	foreign = "foreign.example.test"
	key     = "test-key-0123456789"
	values  = "api_key: " + key
	search  = "/api/subtitles/search"
)

func tags(ss ...string) []language.Tag {
	var out []language.Tag
	for _, s := range ss {
		tg, ok := language.Parse(s)
		if !ok {
			panic("tag " + s)
		}
		out = append(out, tg)
	}
	return out
}

func adapter(t *testing.T) provider.Adapter {
	return providertest.Build(t, subx.NewEntry(), values)
}

// inception is the recorded film's query.
func inception(langs ...string) provider.Query {
	return provider.Query{
		Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666, TMDB: 27205},
		Titles: []string{"Inception"}, Year: 2010, Langs: tags(langs...),
	}
}

// breakingBad is a query for S01E03 of the recorded series.
func breakingBad(langs ...string) provider.Query {
	return provider.Query{
		Kind: media.Episode, External: media.ExternalIDs{IMDb: 903747, TVDB: 81189},
		Titles: []string{"Breaking Bad"}, Year: 2008,
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 3}, Langs: tags(langs...),
	}
}

// The recorded searches, by their sorted query.
const (
	filmByID     = "imdb_id=tt1375666&limit=200&video_type=movie"
	seasonByID   = "imdb_id=tt0903747&limit=200&season=1&video_type=episode"
	episodeByID  = "episode=3&imdb_id=tt0903747&limit=200&season=1&video_type=episode"
	filmByName   = "limit=200&title=Inception&video_type=movie&year=2010"
	seasonByName = "limit=200&season=1&title=Breaking+Bad&video_type=episode&year=2008"
	testerSearch = "imdb_id=tt1375666&limit=1&video_type=movie"
)

// route is one answer: a testdata file, else body, and its status, 0 for
// 200.
type route struct {
	file   string
	body   []byte
	status int
}

// site answers each request whose path, or path and sorted query, it lists
// with its route, any other with 404, and records every request.
type site struct {
	routes map[string]route
	reqs   []*http.Request
	mu     sync.Mutex
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r)
	s.mu.Unlock()
	rt, ok := s.routes[r.URL.Path+"?"+r.URL.Query().Encode()]
	if !ok {
		rt, ok = s.routes[r.URL.Path]
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	b := rt.body
	if rt.file != "" {
		var err error
		if b, err = os.ReadFile("testdata/" + rt.file); err != nil {
			panic(err)
		}
	}
	if rt.status != 0 {
		w.WriteHeader(rt.status)
	}
	_, _ = w.Write(b)
}

func (s *site) seen() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// asked is each request's path and query, robots.txt's left out.
func (s *site) asked() []string {
	var out []string
	for _, r := range s.seen() {
		if r.URL.Path != "/robots.txt" {
			out = append(out, r.URL.Path+"?"+r.URL.Query().Encode())
		}
	}
	return out
}

// recorded serves the recorded answers, robots.txt's 404 among them.
func recorded() *site {
	return &site{routes: map[string]route{
		"/robots.txt":                          {file: "robots-404.html", status: http.StatusNotFound},
		search + "?" + filmByID:                {file: "search-imdb-tt1375666.json"},
		search + "?" + seasonByID:              {file: "search-imdb-tt0903747-s1.json"},
		search + "?" + episodeByID:             {file: "search-imdb-tt0903747-s1e3.json"},
		search + "?" + filmByName:              {file: "search-title-inception-2010.json"},
		search + "?" + seasonByName:            {file: "search-title-breakingbad-2008-s1.json"},
		search + "?" + testerSearch:            {file: "search-tester.json"},
		"/api/subtitles/" + film + "/download": {file: "dl-5bd14610.rar"},
		"/api/subtitles/" + rar + "/download":  {file: "dl-daedbf8a.rar"},
		"/api/subtitles/" + zip + "/download":  {file: "dl-e47eda2c.zip"},
		"/api/subtitles/" + gone + "/download": {file: "dl-404.json", status: http.StatusNotFound},
	}}
}

// The recorded downloads' ids.
const (
	film = "5bd14610-cfce-4075-a93a-5295b3ea21d1"
	rar  = "daedbf8a-3603-46bb-b237-0a9d9df7d77a"
	zip  = "e47eda2c-de8b-46e8-bad1-c25c1b09511c"
	gone = "00000000-0000-4000-8000-000000000000"
)
