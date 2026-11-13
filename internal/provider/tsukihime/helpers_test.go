package tsukihime_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/tsukihime"
)

const (
	apiHost     = "api.tsukihime.org"
	storageHost = "storage.tsukihime.org"
	ghHost      = "github.com"
	assetHost   = "release-assets.githubusercontent.com"

	// The recorded animes: AniDB id and its TVDB series as a test maps it.
	frieren     = 17617 // TsukiHime anime 19
	sasaki2     = 18563 // TsukiHime anime 9823
	frierenS2   = 18886 // TsukiHime anime 284
	dangersFilm = 18890 // TsukiHime anime 9627, a film
	series      = 424536
	sasakiTVDB  = 400100
	filmTMDB    = 1300100
)

var hosts = []string{apiHost, storageHost, ghHost, "objects.githubusercontent.com", assetHost}

func tag(t testing.TB, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

// doc is an animap document holding anime.
func doc(anime ...mapread.Anime) *mapread.Document {
	return &mapread.Document{
		Version:     2,
		GeneratedAt: "2026-11-01T00:00:00Z",
		Sources:     mapread.Sources{Mapping: mapread.MappingSource{Revision: "r"}, AniDB: mapread.AniDBSource{TitlesDump: "2026-10-31"}},
		Attribution: mapread.Attribution{License: "ODbL-1.0", LicenseURL: "u", ContentsLicense: "DbCL-1.0", ContentsLicenseURL: "u", Notice: "n"},
		Anime:       anime,
		Entries:     []mapread.Entry{},
	}
}

// tv is a TV anime on series whose regular episodes first to last land on
// season's episodes of the same numbers.
func tv(aid, tvdb, season, first, last int) mapread.Anime {
	ts := []mapread.Target{{Season: season, Episode: first}}
	runs := []mapread.Run{{Kind: mapread.Regular, Start: first, End: last, TVDB: &ts}}
	return mapread.Anime{AniDBID: aid, Type: "TV", TVDBID: tvdb, Last: mapread.Last{Regular: last}, Episodes: runs}
}

// film is a one-episode anime stated as TMDB movie tmdb.
func film(aid, tmdb int) mapread.Anime {
	return mapread.Anime{
		AniDBID: aid, Type: "MOVIE", Last: mapread.Last{Regular: 1},
		Episodes: []mapread.Run{{Kind: mapread.Regular, Start: 1, End: 1, TMDBMovies: []int{tmdb}}},
	}
}

// the document every test reads unless it says otherwise.
func recordedDoc() *mapread.Document {
	return doc(tv(frieren, series, 1, 1, 28), tv(sasaki2, sasakiTVDB, 2, 1, 12), tv(frierenS2, series, 2, 1, 10), film(dangersFilm, filmTMDB))
}

// github serves animap's release d on github.com, the asset redirecting to
// the asset host, and counts releases/latest requests.
type github struct {
	fail   atomic.Bool
	body   []byte
	latest atomic.Int64
}

func newGitHub(t testing.TB, d *mapread.Document) *github {
	t.Helper()
	b, err := mapread.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	return &github{body: b}
}

func (g *github) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if g.fail.Load() {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	const repo = "/cplieger/animap/releases/"
	p := strings.TrimPrefix(r.URL.Path, repo)
	switch {
	case r.Host == ghHost && p == "latest":
		g.latest.Add(1)
		w.Header().Set("Location", "https://github.com"+repo+"tag/v2026.11.01")
		w.WriteHeader(http.StatusFound)
	case r.Host == ghHost && strings.HasPrefix(p, "download/"):
		w.Header().Set("Location", "https://"+assetHost+"/"+strings.TrimPrefix(p, "download/"))
		w.WriteHeader(http.StatusFound)
	case r.Host == assetHost && strings.HasSuffix(r.URL.Path, "/animap.json.sha256"):
		sum := sha256.Sum256(g.body)
		_, _ = fmt.Fprintf(w, "%s  animap.json\n", hex.EncodeToString(sum[:]))
	case r.Host == assetHost && strings.HasSuffix(r.URL.Path, "/animap.json"):
		_, _ = w.Write(g.body)
	default:
		http.NotFound(w, r)
	}
}

// site fakes TsukiHime's API and storage from testdata, by request path and
// query; answers set by a test win. Every request is recorded. A torrent
// list filters by sublang as the live API does: one label, matched exactly.
type site struct {
	answers map[string]string
	status  map[string]int
	mu      sync.Mutex
	reqs    []string
}

// recordedRoutes maps the recorded files that are not torrent-<id>.json.
var recordedRoutes = map[string]string{
	"/v1/animes/anidb/17617":     "a.json",
	"/v1/animes/19/episodes/1":   "e.json",
	"/v1/torrents/37747":         "one.json",
	"/v1/animes/anidb/18563":     "a5.json",
	"/v1/animes/9823/episodes/5": "e5.json",
	"/v1/animes/anidb/18886":     "a-batch.json",
	"/v1/animes/anidb/18890":     "a-film.json",
	"/v1/animes/9627/episodes/1": "e-film.json",
	"/v1/torrents?anime_id=9627": "torrents-film.json",
	"/attach/00052046/335942.xz": "335942.xz",
	"/v1/animes/anidb/99999999":  "a-unknown.json",
}

func recorded(t testing.TB) *site {
	t.Helper()
	s := &site{answers: map[string]string{}, status: map[string]int{}}
	for route, file := range recordedRoutes {
		s.answers[route] = read(t, file)
	}
	s.status["/v1/animes/anidb/99999999"] = http.StatusNotFound
	names, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if id, ok := strings.CutPrefix(strings.TrimSuffix(n.Name(), ".json"), "torrent-"); ok {
			s.answers["/v1/torrents/"+id] = read(t, n.Name())
		}
	}
	return s
}

func read(t testing.TB, name string) string {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// set makes route answer body.
func (s *site) set(route string, body any) {
	b, ok := body.(string)
	if !ok {
		j, _ := json.Marshal(body)
		b = string(j)
	}
	s.mu.Lock()
	s.answers[route] = b
	s.mu.Unlock()
}

// requests are the API and storage requests sent, as path?query.
func (s *site) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

func (s *site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	route := r.URL.Path
	q := r.URL.Query()
	sublang := q.Get("sublang")
	q.Del("sublang")
	if len(q) > 0 {
		route += "?" + q.Encode()
	}
	s.mu.Lock()
	s.reqs = append(s.reqs, r.URL.RequestURI())
	body, ok := s.answers[route]
	status := s.status[route]
	s.mu.Unlock()
	switch {
	case !ok:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"detail":"Tsukihime API Error: URL does not exist."}`))
	case status != 0:
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	case sublang != "":
		_, _ = w.Write(filterSublang(body, sublang))
	default:
		_, _ = w.Write([]byte(body))
	}
}

func filterSublang(body, label string) []byte {
	var l map[string]any
	_ = json.Unmarshal([]byte(body), &l)
	rs, _ := l["results"].([]any)
	var kept []any
	for _, r := range rs {
		labels, _ := r.(map[string]any)["sublangs"].([]any)
		if slices.Contains(labels, any(label)) {
			kept = append(kept, r)
		}
	}
	l["results"] = kept
	b, _ := json.Marshal(l)
	return b
}

// rig is one TsukiHime adapter over its own Map, GitHub serving d and api
// answering TsukiHime's hosts, in memory.
type rig struct {
	ctx context.Context
	a   provider.Adapter
	gh  *github
}

func newRig(t testing.TB, d *mapread.Document, api http.Handler) *rig {
	t.Helper()
	gh := newGitHub(t, d)
	ctx := providertest.Serve(t, t.Context(), both(gh, api), hosts...)
	return &rig{ctx: ctx, a: providertest.Build(t, tsukihime.NewEntry(animemap.New()), ""), gh: gh}
}

// both serves TsukiHime's hosts through api and every other through gh.
func both(gh, api http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == apiHost || r.Host == storageHost {
			api.ServeHTTP(w, r)
			return
		}
		gh.ServeHTTP(w, r)
	})
}

func (r *rig) search(t testing.TB, q provider.Query) []provider.Candidate {
	t.Helper()
	cs, err := r.a.Search(r.ctx, q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	return cs
}

func episode(t testing.TB, tvdb uint64, season, ep int, langs ...string) provider.Query {
	q := provider.Query{
		Kind: media.Episode, External: media.ExternalIDs{TVDB: tvdb},
		Numbering: media.Numbering{Scheme: media.TVDB, Season: season, Episode: ep},
	}
	for _, l := range langs {
		q.Langs = append(q.Langs, tag(t, l))
	}
	return q
}

func movie(t testing.TB, tmdb uint64, langs ...string) provider.Query {
	q := provider.Query{Kind: media.Movie, External: media.ExternalIDs{TMDB: tmdb}}
	for _, l := range langs {
		q.Langs = append(q.Langs, tag(t, l))
	}
	return q
}

// refs are the candidates' Ref tokens, in order.
func refs(cs []provider.Candidate) []string {
	out := make([]string, len(cs))
	for i := range cs {
		out[i] = cs[i].Ref.Token()
	}
	return out
}

// apiPaths are the requests to TsukiHime's API, less its /v1 prefix.
func apiPaths(reqs []string) []string {
	var out []string
	for _, r := range reqs {
		if p, ok := strings.CutPrefix(r, "/v1"); ok {
			out = append(out, p)
		}
	}
	return out
}
