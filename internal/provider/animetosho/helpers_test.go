package animetosho_test

import (
	"bytes"
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/ulikunitz/xz"
)

const (
	feedHost    = "feed.animetosho.net"
	storageHost = "storage.animetosho.net"
	ghHost      = "github.com"
	assetHost   = "release-assets.githubusercontent.com"
)

var hosts = []string{feedHost, storageHost, ghHost, "objects.githubusercontent.com", assetHost}

func tag(t testing.TB, s string) language.Tag {
	t.Helper()
	tg, ok := language.Parse(s)
	if !ok {
		t.Fatalf("tag %q", s)
	}
	return tg
}

// doc is an animap document holding anime, as a test writes it.
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

func encode(t testing.TB, d *mapread.Document) []byte {
	t.Helper()
	b, err := mapread.Encode(d)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func targets(ts ...mapread.Target) *[]mapread.Target {
	if ts == nil {
		ts = []mapread.Target{}
	}
	return &ts
}

func at(season, episode int) mapread.Target { return mapread.Target{Season: season, Episode: episode} }

// release is one release of the fake GitHub: its document's bytes and, when
// set, a sidecar replacing the true one.
type release struct {
	body    []byte
	sidecar *string
}

// github fakes animap's releases on github.com, an asset download
// redirecting to the asset host. latest answers releases/latest; fail, when
// set, answers a request first.
type github struct {
	fail     func(w http.ResponseWriter, r *http.Request) bool
	releases map[string]release
	latest   atomic.Value // string
	mu       sync.Mutex
	reqs     []string
}

func newGitHub(t testing.TB, latest string, d *mapread.Document) *github {
	g := &github{releases: map[string]release{}}
	g.publish(latest, release{body: encode(t, d)})
	return g
}

// publish adds a release and makes it the latest.
func (g *github) publish(tag string, r release) {
	g.mu.Lock()
	g.releases[tag] = r
	g.mu.Unlock()
	g.latest.Store(tag)
}

// setFail makes f answer each request first, nil for none.
func (g *github) setFail(f func(w http.ResponseWriter, r *http.Request) bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fail = f
}

func (g *github) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return slices.Clone(g.reqs)
}

// count is how many requests named a path ending in suffix, an asset
// counted at the asset host it redirects to.
func (g *github) count(suffix string) int {
	n := 0
	for _, r := range g.requests() {
		if strings.HasSuffix(r, suffix) && !strings.Contains(r, "/releases/download/") {
			n++
		}
	}
	return n
}

func (g *github) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	g.reqs = append(g.reqs, r.Host+r.URL.Path)
	fail := g.fail
	g.mu.Unlock()
	if fail != nil && fail(w, r) {
		return
	}
	const repo = "/cplieger/animap/releases/"
	p := strings.TrimPrefix(r.URL.Path, repo)
	switch {
	case r.Host == ghHost && p == "latest":
		w.Header().Set("Location", "https://github.com"+repo+"tag/"+g.latest.Load().(string))
		w.WriteHeader(http.StatusFound)
	case r.Host == ghHost && strings.HasPrefix(p, "download/"):
		w.Header().Set("Location", "https://"+assetHost+"/"+strings.TrimPrefix(p, "download/"))
		w.WriteHeader(http.StatusFound)
	case r.Host == assetHost:
		tg, file, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
		g.mu.Lock()
		rel, ok := g.releases[tg]
		g.mu.Unlock()
		switch {
		case !ok:
			http.NotFound(w, r)
		case file == "animap.json.sha256" && rel.sidecar != nil:
			_, _ = w.Write([]byte(*rel.sidecar))
		case file == "animap.json.sha256":
			sum := sha256.Sum256(rel.body)
			_, _ = fmt.Fprintf(w, "%s  animap.json\n", hex.EncodeToString(sum[:]))
		case file == "animap.json":
			_, _ = w.Write(rel.body)
		default:
			http.NotFound(w, r)
		}
	default:
		http.NotFound(w, r)
	}
}

// tosho fakes AnimeTosho's feed and storage: lists answers a search by its
// query ("eid=5", "aid=1", "q=Show S01"), details an entry by its id, and
// storage answers every attachment with an xz subtitle naming its path.
type tosho struct {
	lists   map[string]string
	details map[int]string
	mu      sync.Mutex
	reqs    []string
}

func newTosho() *tosho { return &tosho{lists: map[string]string{}, details: map[int]string{}} }

// recorded is a fake serving the recorded .net answers under testdata.
func recorded(t testing.TB) *tosho {
	t.Helper()
	f := newTosho()
	names, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		b, err := os.ReadFile("testdata/" + n.Name())
		if err != nil {
			t.Fatal(err)
		}
		switch base := strings.TrimSuffix(n.Name(), ".json"); {
		case strings.HasPrefix(base, "torrent-"):
			id, _ := strconv.Atoi(strings.TrimPrefix(base, "torrent-"))
			f.details[id] = string(b)
		case base == "feed-q-sousou-no-frieren":
			f.lists["q=Sousou no Frieren S01"] = string(b)
		case strings.HasPrefix(base, "feed-"):
			f.lists[strings.Replace(strings.TrimPrefix(base, "feed-"), "-", "=", 1)] = string(b)
		}
	}
	return f
}

// queries are the feed searches sent, in order.
func (f *tosho) queries() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, r := range f.reqs {
		if !strings.Contains(r, "show=torrent") && strings.HasPrefix(r, feedHost) {
			out = append(out, strings.TrimPrefix(r, feedHost+"/json?"))
		}
	}
	return out
}

func (f *tosho) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	f.reqs = append(f.reqs, r.Host+r.URL.Path+"?"+r.URL.RawQuery)
	f.mu.Unlock()
	q := r.URL.Query()
	switch {
	case r.Host == storageHost:
		var buf bytes.Buffer
		zw, _ := xz.NewWriter(&buf)
		_, _ = fmt.Fprintf(zw, "1\n00:00:01,000 --> 00:00:02,000\nattachment %s\n", r.URL.Path)
		_ = zw.Close()
		_, _ = w.Write(buf.Bytes())
	case q.Get("show") == "torrent":
		id, _ := strconv.Atoi(q.Get("id"))
		d, ok := f.details[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(d))
	default:
		for _, k := range []string{"eid", "aid", "q"} {
			if v := q.Get(k); v != "" {
				_, _ = w.Write([]byte(cmp.Or(f.lists[k+"="+v], "[]")))
				return
			}
		}
		_, _ = w.Write([]byte("[]"))
	}
}

// entry adds a search answer of one complete entry whose files each carry
// one English subtitle, to the fake's list for query.
func (f *tosho) entry(query string, id int, title string, files ...string) {
	var list []map[string]any
	if prev, ok := f.lists[query]; ok {
		_ = json.Unmarshal([]byte(prev), &list)
	}
	list = append(list, map[string]any{"id": id, "title": title, "status": "complete"})
	b, _ := json.Marshal(list)
	f.lists[query] = string(b)
	type att struct {
		Type string            `json:"type"`
		URL  string            `json:"url"`
		Info map[string]string `json:"info"`
	}
	type fl struct {
		Filename    string `json:"filename"`
		Attachments []att  `json:"attachments"`
	}
	var fs []fl
	for i, n := range files {
		u := fmt.Sprintf("https://%s/attachments/%d/%d.xz?filename=%s", storageHost, id, i, url.QueryEscape(n))
		fs = append(fs, fl{Filename: n, Attachments: []att{{Type: "subtitle", URL: u, Info: map[string]string{"language_code": "eng", "name": "English"}}}})
	}
	d, _ := json.Marshal(map[string]any{"id": id, "title": title, "files": fs})
	f.details[id] = string(d)
}

// fileOf is the file name a candidate's attachment link names.
func fileOf(c *provider.Candidate) string {
	u, err := url.Parse(c.Ref.Token())
	if err != nil {
		return "?"
	}
	return u.Query().Get("filename")
}

// placed renders each candidate as its file, how it was matched and what it
// covers, sorted.
func placed(cs []provider.Candidate) []string {
	var out []string
	for i := range cs {
		c := &cs[i]
		s := fileOf(c)
		if c.Unconfirmed {
			s += " offered"
		}
		for _, n := range c.Covers {
			s += fmt.Sprintf(" S%02dE%02d", n.Season, n.Episode)
		}
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// rig is one adapter over its own Map, with GitHub and AnimeTosho served in
// memory.
type rig struct {
	ctx context.Context
	a   provider.Adapter
	m   *animemap.Map
	gh  *github
	at  *tosho
}

func newRig(t testing.TB, gh *github, at *tosho) *rig {
	t.Helper()
	m := animemap.New()
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == feedHost || r.Host == storageHost {
			at.ServeHTTP(w, r)
			return
		}
		gh.ServeHTTP(w, r)
	}), hosts...)
	return &rig{ctx: ctx, a: providertest.Build(t, animetosho.NewEntry(m), ""), m: m, gh: gh, at: at}
}

func episodeQuery(t testing.TB, series uint64, season, ep int) provider.Query {
	return provider.Query{
		Kind: media.Episode, Titles: []string{"Show"}, External: media.ExternalIDs{TVDB: series},
		Numbering: media.Numbering{Scheme: media.TVDB, Season: season, Episode: ep}, Langs: []language.Tag{tag(t, "en")},
	}
}

func (r *rig) search(t testing.TB, q provider.Query) []provider.Candidate {
	t.Helper()
	cs, err := r.a.Search(r.ctx, q)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	return cs
}

func allTags() []language.Tag {
	var out []language.Tag
	for _, n := range language.Names() {
		out = append(out, n.Tag)
	}
	return out
}

// numbered is format filled with each number from first to last.
func numbered(format string, first, last int) []string {
	var out []string
	for n := first; n <= last; n++ {
		out = append(out, fmt.Sprintf(format, n))
	}
	return out
}

func pad(n int) string { return fmt.Sprintf("%02d", n) }

func itoa(n int) string { return strconv.Itoa(n) }

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// fileNamesOf is every file name of a recorded detail answer.
func fileNamesOf(t testing.TB, detail string) []string {
	t.Helper()
	var d struct {
		Files []struct {
			Filename string `json:"filename"`
		} `json:"files"`
	}
	if err := json.Unmarshal([]byte(detail), &d); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range d.Files {
		out = append(out, f.Filename)
	}
	return out
}

// bracketedNumber is a bracket or parenthesis holding only an episode-shaped
// number ("[05]", "( 08 )", "[12v2]").
var bracketedNumber = regexp.MustCompile(`[\[(]\s*\d{1,3}(v\d+)?\s*[\])]`)

func bracketedOnly(name string) bool { return bracketedNumber.MatchString(name) }
