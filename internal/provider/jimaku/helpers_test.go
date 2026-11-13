package jimaku_test

import (
	"net/http"
	"net/url"
	"os"
	"slices"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/jimaku"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

const (
	host    = "jimaku.cc"
	foreign = "foreign.example.test"
	key     = "test-key-0123456789"
	values  = "api_key: " + key
)

var ja = mustTag("ja")

func mustTag(s string) language.Tag {
	tg, ok := language.Parse(s)
	if !ok {
		panic("tag " + s)
	}
	return tg
}

func adapter(t *testing.T) provider.Adapter {
	return providertest.Build(t, jimaku.NewEntry(), values)
}

// episode is a query for S01E03 of the series TMDB id tmdb.
func episode(tmdb uint64) provider.Query {
	return provider.Query{
		Kind: media.Episode, External: media.ExternalIDs{TMDB: tmdb},
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 3}, Langs: []language.Tag{ja},
	}
}

func movie(tmdb uint64) provider.Query {
	return provider.Query{Kind: media.Movie, External: media.ExternalIDs{TMDB: tmdb}, Langs: []language.Tag{ja}}
}

// site answers each request whose path and query it lists with a testdata
// file, any other with 404, and records every request.
type site struct {
	routes map[string]string
	reqs   []*http.Request
	mu     sync.Mutex
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r)
	s.mu.Unlock()
	file, ok := s.routes[uri(r)]
	if !ok {
		file, ok = s.routes[r.URL.Path]
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		panic(err)
	}
	_, _ = w.Write(b)
}

func (s *site) seen() []*http.Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// uris are the requests s answered, in order, each as uri reads it.
func (s *site) uris() []string {
	var out []string
	for _, r := range s.seen() {
		out = append(out, uri(r))
	}
	return out
}

// uri is r's path and its query decoded with its keys in order, so a route
// names a request however its query is escaped or ordered.
func uri(r *http.Request) string {
	if r.URL.RawQuery == "" {
		return r.URL.Path
	}
	q, err := url.QueryUnescape(r.URL.Query().Encode())
	if err != nil {
		panic(err)
	}
	return r.URL.Path + "?" + q
}

// search is the route of a TMDB search with the anime filter.
func search(id, anime string) string {
	return "/api/entries/search?anime=" + anime + "&tmdb_id=" + id
}
