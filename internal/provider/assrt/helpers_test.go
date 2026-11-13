package assrt_test

import (
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/assrt"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

const (
	apiHost  = "api.assrt.net"
	fileHost = "file0.assrt.net"
	foreign  = "foreign.example.test"
	token    = "Ab3dEf6hIj9kLm2nOp5qRs8tUv1wXy4z" // gitleaks:allow
	values   = "token: " + token
	rarPath  = "/download/626317/Inception.2010.BluRay.rar"
	chs      = "Inception.2010.UHD.BluRay.1080p.DTS.HDR.x265-TnP.chs.srt"
	cht      = "Inception.2010.UHD.BluRay.1080p.DTS.HDR.x265-TnP.cht.srt"
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

func build(t *testing.T, e provider.Entry) provider.Adapter {
	return providertest.Build(t, e, values)
}

// inception is a query for the recorded film by its video string.
func inception(langs ...string) provider.Query {
	return provider.Query{
		Kind: media.Movie, Video: "Inception.2010.1080p.BluRay.x264-SPARKS", Titles: []string{"Inception"},
		Year: 2010, Langs: tags(langs...),
	}
}

// route is one answer: a testdata file, else body, and its status, 0 for
// 200.
type route struct {
	file, body string
	status     int
}

// site answers each path it lists with its route, any other with 404, and
// records every request with the time it arrived.
type site struct {
	routes map[string]route
	reqs   []*http.Request
	at     []time.Time
	mu     sync.Mutex
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.reqs, s.at = append(s.reqs, r), append(s.at, time.Now())
	rt, ok := s.routes[r.URL.Path]
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	b := []byte(rt.body)
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

func (s *site) paths() []string {
	var out []string
	for _, r := range s.seen() {
		out = append(out, r.Host+r.URL.Path)
	}
	return out
}

// spending are the arrival times of the requests that spend the quota:
// every one but user/quota (testdata/README.md).
func (s *site) spending() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []time.Time
	for i, r := range s.reqs {
		if !strings.HasSuffix(r.URL.Path, "/user/quota") {
			out = append(out, s.at[i])
		}
	}
	return out
}

// quotaReads is how many user/quota requests s answered.
func (s *site) quotaReads() int {
	n := 0
	for _, p := range s.paths() {
		if strings.HasSuffix(p, "/user/quota") {
			n++
		}
	}
	return n
}

// recorded serves the recorded Inception search, its detail and archive,
// and a quota of 5.
func recorded() *site {
	return &site{routes: map[string]route{
		"/v1/user/quota": {file: "quota.json"},
		"/v1/sub/search": {file: "search-inception.json"},
		"/v1/sub/detail": {file: "detail-626317.json"},
		rarPath:          {file: "dl-626317.rar"},
	}}
}

func entry() provider.Entry { return assrt.NewEntry() }
