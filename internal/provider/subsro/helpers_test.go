package subsro_test

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
	"github.com/cplieger/subflux/internal/provider/subsro"
)

const (
	host    = "api.subs.ro"
	foreign = "foreign.example.test"
	key     = "test-key-0123456789"
	values  = "api_key: " + key
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
	return providertest.Build(t, subsro.NewEntry(), values)
}

// inception is the recorded film's query.
func inception(langs ...string) provider.Query {
	return provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666, TMDB: 27205}, Langs: tags(langs...)}
}

// breakingBad is a query for S01E03 of the recorded series.
func breakingBad(langs ...string) provider.Query {
	return provider.Query{
		Kind: media.Episode, External: media.ExternalIDs{IMDb: 903747, TVDB: 81189},
		Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 3}, Langs: tags(langs...),
	}
}

// route is one recorded answer: a testdata file and its status, 0 for 200.
type route struct {
	file   string
	status int
}

// site answers each path it lists with its route, any other with 404, and
// records every request.
type site struct {
	routes map[string]route
	reqs   []*http.Request
	mu     sync.Mutex
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs = append(s.reqs, r)
	s.mu.Unlock()
	rt, ok := s.routes[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	b, err := os.ReadFile("testdata/" + rt.file)
	if err != nil {
		panic(err)
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

func (s *site) paths() []string {
	var out []string
	for _, r := range s.seen() {
		out = append(out, r.URL.Path)
	}
	return out
}
