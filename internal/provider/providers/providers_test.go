package providers_test

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/buildinfo"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providers"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

// foreign is a host no entry lists.
const foreign = "foreign.example.test"

// wantHosts is design 5.4's table with the download-link hosts the
// 2026-11-06 capture recorded.
var wantHosts = map[provider.ProviderID][]string{
	"animetosho":    {"feed.animetosho.net", "storage.animetosho.net", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"},
	"assrt":         {"api.assrt.net", "file0.assrt.net"},
	"betaseries":    {"api.betaseries.com", "www.betaseries.com", "subtitles.betaseries.com"},
	"gestdown":      {"api.gestdown.info"},
	"hdbits":        {"hdbits.org"},
	"jimaku":        {"jimaku.cc"},
	"opensubtitles": {"api.opensubtitles.com", "vip-api.opensubtitles.com", "www.opensubtitles.com"},
	"subdl":         {"api.subdl.com", "dl.subdl.com"},
	"subsource":     {"api.subsource.net"},
	"subsro":        {"api.subs.ro"},
	"subx":          {"subx-api.duckdns.org"},
	"tsukihime":     {"api.tsukihime.org", "storage.tsukihime.org", "github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"},
	"yifysubtitles": {"yifysubtitles.ch"},
}

func TestEntries(t *testing.T) {
	es := providers.New().Entries
	ids := make([]provider.ProviderID, 0, len(es))
	for _, e := range es {
		ids = append(ids, e.ID)
		if n := len(e.Summary); n < 1 || n > 120 || strings.ContainsAny(e.Summary, "\r\n") {
			t.Errorf("%s: Summary %q is not one line of 1 to 120 bytes", e.ID, e.Summary)
		}
		want, ok := wantHosts[e.ID]
		if !ok {
			t.Errorf("%s: no expected Hosts", e.ID)
		}
		if !slices.Equal(e.Hosts, want) {
			t.Errorf("%s: Hosts %v, want %v", e.ID, e.Hosts, want)
		}
		if e.New == nil || len(e.Languages.Tags()) == 0 {
			t.Errorf("%s: no constructor or no languages", e.ID)
		}
	}
	if !slices.IsSorted(ids) || len(slices.Compact(slices.Clone(ids))) != len(ids) {
		t.Errorf("ids %v are not unique and in order", ids)
	}
	if len(ids) != len(wantHosts) {
		t.Errorf("%d entries, want %d", len(ids), len(wantHosts))
	}
	again := providers.New().Entries
	again[0].Hosts[0] = "changed.example.test"
	if providers.New().Entries[0].Hosts[0] == "changed.example.test" {
		t.Error("Entries shares its rows between calls")
	}
}

// TestQueryUnion asks each production map through an automatic query, as
// the engine does: a target reaches every label the provider files its tag
// under, a bare language every listed variety of it, and nb a map holding
// only no.
func TestQueryUnion(t *testing.T) {
	wires := func(id provider.ProviderID, needing ...string) []string {
		m := entry(t, id).Languages
		var need []language.Tag
		for _, n := range needing {
			need = append(need, mustTag(n))
		}
		var out []string
		for _, tg := range (language.Policy{}).AutoQuery(need, m.Tags()) {
			w := m.Wires(tg)
			if len(w) == 0 {
				t.Fatalf("%s: AutoQuery returned %v, which the map does not list", id, tg)
			}
			out = append(out, w...)
		}
		slices.Sort(out)
		return out
	}
	for _, r := range []struct {
		id      provider.ProviderID
		needing []string
		want    []string
	}{
		{"opensubtitles", []string{"zh-Hant"}, []string{"zh-tw"}},
		{"opensubtitles", []string{"pt"}, []string{"pt-br", "pt-pt"}},
		{"subsource", []string{"zh-Hant"}, []string{"big_5_code", "chinese_traditional"}},
		{"subsource", []string{"pt-BR"}, []string{"brazilian_portuguese"}},
		{"subdl", []string{"nb"}, []string{"NO"}},
		{"gestdown", []string{"pt-BR"}, []string{"pt-BR"}},
		{"subx", []string{"es"}, []string{"es-419", "es-ES"}},
	} {
		if got := wires(r.id, r.needing...); !slices.Equal(got, r.want) {
			t.Errorf("%s asked for %v sends %q, want %q", r.id, r.needing, got, r.want)
		}
	}
}

// values is a YAML settings mapping filling e's String and Secret fields;
// every other field keeps its default.
func values(t *testing.T, e provider.Entry) string {
	t.Helper()
	var b strings.Builder
	for _, f := range e.Schema.Fields {
		switch f.Kind {
		case formschema.String:
			fmt.Fprintf(&b, "%s: user-%s\n", f.Key, f.Key)
		case formschema.Secret:
			fmt.Fprintf(&b, "%s: %s\n", f.Key, secret(f.Key))
		case formschema.Bool:
		default:
			t.Fatalf("%s: field %s has a kind this test does not fill", e.ID, f.Key)
		}
	}
	return b.String()
}

func secret(key string) string { return "secret-" + key + "-0123456789" }

// counter records every request it answers.
type counter struct {
	answer http.Handler
	reqs   []*http.Request
	mu     sync.Mutex
}

func (c *counter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	c.reqs = append(c.reqs, r)
	c.mu.Unlock()
	if c.answer != nil {
		c.answer.ServeHTTP(w, r)
	}
}

func (c *counter) seen() []*http.Request {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.reqs)
}

func mustTag(s string) language.Tag {
	tg, ok := language.Parse(s)
	if !ok {
		panic("tag " + s)
	}
	return tg
}

// movie is the HDBits torrent search the POST table reaches.
var movie = provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666}, Langs: []language.Tag{mustTag("en")}}

// queries reach every adapter's search path: a movie by id and title, and an
// episode by its series' ids.
func queries() []provider.Query {
	en := []language.Tag{mustTag("en")}
	return []provider.Query{
		{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666, TMDB: 27205}, Titles: []string{"Inception"}, Year: 2010, Langs: en},
		{
			Kind: media.Episode, External: media.ExternalIDs{IMDb: 903747, TVDB: 81189, TMDB: 1396}, Titles: []string{"Breaking Bad"},
			Numbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}, Langs: en,
		},
	}
}

// tokens are a well-formed token for each adapter and tokens that name the
// foreign host in each form a URL builder could read as its host.
var tokens = []string{
	"551609", "551609.ass", "/subtitle/1-2.zip", "/subtitles/inception-english-yify-1",
	"https://" + foreign + "/x", "//" + foreign + "/x", "@" + foreign + "/x", "." + foreign + "/x",
}

func TestClientRefusesUnlistedHost(t *testing.T) {
	for _, e := range providers.New().Entries {
		t.Run(string(e.ID), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				away := &counter{}
				ctx := providertest.Serve(t, t.Context(), away, foreign)
				home := &counter{answer: http.RedirectHandler("https://"+foreign+"/stolen", http.StatusFound)}
				ctx = providertest.Serve(t, ctx, home, e.Hosts...)
				a := providertest.Build(t, e, values(t, e))
				for _, q := range queries() {
					_, err := a.Search(ctx, q)
					refused(t, err)
				}
				for _, tok := range tokens {
					_, err := a.Fetch(ctx, provider.NewRef(tok))
					refused(t, err)
				}
				if n := len(away.seen()); n != 0 {
					t.Fatalf("the foreign host answered %d requests", n)
				}
				if len(home.seen()) == 0 {
					t.Fatal("no request reached the listed hosts")
				}
			})
		})
	}
}

// refused fails t when err is neither nil nor a Failure or Skipped; a
// redirect the Client refuses is Permanent.
func refused(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		return
	}
	switch e := err.(type) {
	case *provider.Failure, *provider.Skipped:
	default:
		t.Fatalf("error %T %v", e, err)
	}
}

func TestSubSourceKeyStaysHome(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := entry(t, "subsource")
		away := &counter{}
		ctx := providertest.Serve(t, t.Context(), away, foreign)
		home := &counter{answer: http.RedirectHandler("https://"+foreign+"/api/v1/movies/search", http.StatusFound)}
		ctx = providertest.Serve(t, ctx, home, e.Hosts...)
		a := providertest.Build(t, e, values(t, e))
		_, err := a.Search(ctx, queries()[0])
		if f, ok := err.(*provider.Failure); !ok || f.Kind != provider.Permanent || f.Host != foreign {
			t.Fatalf("error %v, want the redirect refused as Permanent on %s", err, foreign)
		}
		reqs := home.seen()
		if len(reqs) == 0 || !carries(reqs[0], secret("api_key")) {
			t.Fatalf("the listed host saw %d requests, the first without the key", len(reqs))
		}
		if n := len(away.seen()); n != 0 {
			t.Fatalf("the key's redirect reached the foreign host %d times", n)
		}
	})
}

// carries reports whether r holds v in its query or a header; SubSource's
// key rides in the query.
func carries(r *http.Request, v string) bool {
	if slices.Contains(r.URL.Query()["api_key"], v) {
		return true
	}
	for _, vs := range r.Header {
		if slices.Contains(vs, v) {
			return true
		}
	}
	return false
}

func TestStoredLinkOnUnlistedHost(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		e := entry(t, "opensubtitles")
		away := &counter{}
		ctx := providertest.Serve(t, t.Context(), away, foreign)
		ctx = providertest.Serve(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/v1/login":
				_, _ = w.Write([]byte(`{"token":"t-1","base_url":"api.opensubtitles.com","user":{"vip":false}}`))
			case "/api/v1/download":
				_, _ = w.Write([]byte(`{"link":"https://` + foreign + `/download/x.srt","requests":1,"remaining":9}`))
			default:
				http.NotFound(w, r)
			}
		}), e.Hosts...)
		a := providertest.Build(t, e, values(t, e))
		_, err := a.Fetch(ctx, provider.NewRef("1"))
		if f, ok := err.(*provider.Failure); !ok || f.Kind != provider.Permanent || f.Host != foreign {
			t.Fatalf("error %v, want the link refused as Permanent on %s", err, foreign)
		}
		if n := len(away.seen()); n != 0 {
			t.Fatalf("the stored link reached the foreign host %d times", n)
		}
	})
}

func entry(t *testing.T, id provider.ProviderID) provider.Entry {
	t.Helper()
	for _, e := range providers.New().Entries {
		if e.ID == id {
			return e
		}
	}
	t.Fatalf("no entry %s", id)
	return provider.Entry{}
}

// fault answers one way a POST can fail: a status, or 0 for a connection
// reset mid-body.
func fault(w http.ResponseWriter, status int) {
	if status != 0 {
		w.WriteHeader(status)
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		panic(err)
	}
	writeCut(rw)
	_ = conn.Close()
}

func writeCut(rw *bufio.ReadWriter) {
	_, _ = rw.WriteString("HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Length: 4096\r\n\r\n{\"link\":")
	_ = rw.Flush()
}

// postCase is one adapter's POST the Client sends once: the path whose fault
// the test plants, the answers the calls before it get, and the call that
// reaches it.
type postCase struct {
	id      provider.ProviderID
	path    string
	answers map[string]string
	call    func(context.Context, provider.Adapter) error
}

func TestFetchPOSTNotRetried(t *testing.T) {
	cases := []postCase{
		{
			id: "opensubtitles", path: "/api/v1/download",
			answers: map[string]string{"/api/v1/login": `{"token":"t-1","base_url":"api.opensubtitles.com","user":{"vip":false}}`},
			call: func(ctx context.Context, a provider.Adapter) error {
				_, err := a.Fetch(ctx, provider.NewRef("1"))
				return err
			},
		},
		{
			id: "hdbits", path: "/api/subtitles",
			answers: map[string]string{"/api/torrents": `{"status":0,"data":[{"id":562587}]}`},
			call: func(ctx context.Context, a provider.Adapter) error {
				_, err := a.Search(ctx, movie)
				return err
			},
		},
	}
	for _, pc := range cases {
		for _, status := range []int{500, 503, 507, 599, 0} {
			t.Run(fmt.Sprintf("%s/%d", pc.id, status), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					e := entry(t, pc.id)
					var mu sync.Mutex
					n := 0
					ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == pc.path {
							if r.Method != http.MethodPost {
								t.Errorf("%s %s, want POST", r.Method, r.URL.Path)
							}
							mu.Lock()
							n++
							mu.Unlock()
							fault(w, status)
							return
						}
						if a, ok := pc.answers[r.URL.Path]; ok {
							_, _ = w.Write([]byte(a))
							return
						}
						http.NotFound(w, r)
					}), e.Hosts...)
					a := providertest.Build(t, e, values(t, e))
					err := pc.call(ctx, a)
					if f, ok := err.(*provider.Failure); !ok || f.Kind != provider.Transient {
						t.Errorf("error %v, want Transient", err)
					}
					mu.Lock()
					defer mu.Unlock()
					if n != 1 {
						t.Errorf("the POST was sent %d times, want once", n)
					}
				})
			})
		}
	}
}

func TestDownloadGETRetried(t *testing.T) {
	cases := []struct {
		id      provider.ProviderID
		path    string
		answers map[string]string
		ref     string
		// post is the quota-spending request the GET follows, sent once
		// however often the GET is retried; "" for none.
		post string
	}{
		{"opensubtitles", "/download/x.srt", map[string]string{
			"/api/v1/login":    `{"token":"t-1","base_url":"api.opensubtitles.com","user":{"vip":false}}`,
			"/api/v1/download": `{"link":"https://www.opensubtitles.com/download/x.srt","requests":1,"remaining":9}`,
		}, "1", "/api/v1/download"},
		{"hdbits", "/getdox.php", nil, "551609", ""},
	}
	for _, c := range cases {
		t.Run(string(c.id), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e := entry(t, c.id)
				var mu sync.Mutex
				n, posts := 0, 0
				ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == c.path {
						mu.Lock()
						n++
						mu.Unlock()
						w.WriteHeader(http.StatusServiceUnavailable)
						return
					}
					if r.URL.Path == c.post {
						mu.Lock()
						if r.Method == http.MethodPost {
							posts++
						}
						mu.Unlock()
					}
					if a, ok := c.answers[r.URL.Path]; ok {
						_, _ = w.Write([]byte(a))
						return
					}
					http.NotFound(w, r)
				}), e.Hosts...)
				a := providertest.Build(t, e, values(t, e))
				start := time.Now()
				_, err := a.Fetch(ctx, provider.NewRef(c.ref))
				if f, ok := err.(*provider.Failure); !ok || f.Kind != provider.Transient || f.Status != http.StatusServiceUnavailable {
					t.Errorf("error %v, want Transient 503", err)
				}
				mu.Lock()
				defer mu.Unlock()
				if n != 3 {
					t.Errorf("the GET was sent %d times, want 3", n)
				}
				if c.post != "" && posts != 1 {
					t.Errorf("POST %s was sent %d times, want once while the GET is retried", c.post, posts)
				}
				// httpclient.Do waits about 2 s, then about 4 s, each with equal jitter.
				if d := time.Since(start); d < 3*time.Second {
					t.Errorf("3 attempts took %v of virtual time, want the 2 s backoff", d)
				}
			})
		})
	}
}

// TestExceptionEntries: subx and yifysubtitles are the named exceptions (5.8).
func TestExceptionEntries(t *testing.T) {
	var got []provider.ProviderID
	for _, e := range providers.New().Entries {
		if e.Exception {
			got = append(got, e.ID)
		}
	}
	if want := []provider.ProviderID{"subx", "yifysubtitles"}; !slices.Equal(got, want) {
		t.Errorf("Exception entries %v, want %v", got, want)
	}
}

// TestUserAgentPolicy: every entry's search, fetch and robots.txt requests
// carry subflux's agent, but yifysubtitles', which carry its browser agent.
func TestUserAgentPolicy(t *testing.T) {
	subflux := "subflux/" + buildinfo.Version() + " (+https://github.com/cplieger/subflux)"
	for _, e := range providers.New().Entries {
		t.Run(string(e.ID), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				want := subflux
				if e.ID == "yifysubtitles" {
					want = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36"
				}
				home := &counter{answer: http.NotFoundHandler()}
				ctx := providertest.Serve(t, t.Context(), home, e.Hosts...)
				a := providertest.Build(t, e, values(t, e))
				for _, q := range queries() {
					_, _ = a.Search(ctx, q)
				}
				for _, tok := range tokens[:4] {
					_, _ = a.Fetch(ctx, provider.NewRef(tok))
				}
				reqs := home.seen()
				if len(reqs) == 0 {
					t.Fatal("no request sent")
				}
				robots := false
				for _, r := range reqs {
					robots = robots || r.URL.Path == "/robots.txt"
					if ua := r.UserAgent(); ua != want {
						t.Errorf("%s %s: User-Agent %q, want %q", r.Method, r.URL.Path, ua, want)
					}
				}
				if robots != e.Exception {
					t.Errorf("robots.txt asked %v, want %v", robots, e.Exception)
				}
			})
		})
	}
}

// TestClosedCachesAskNothing: an adapter keeping a lookup cache closes it
// at its Close, so a search that asks when open asks nothing once closed;
// every other adapter asks as before.
func TestClosedCachesAskNothing(t *testing.T) {
	asks := func(e provider.Entry, closed bool) (n int) {
		synctest.Test(t, func(t *testing.T) {
			home := &counter{answer: http.NotFoundHandler()}
			ctx := providertest.Serve(t, t.Context(), home, e.Hosts...)
			a := providertest.Build(t, e, values(t, e))
			if closed {
				if err := a.(provider.Closer).Close(t.Context()); err != nil {
					t.Errorf("%s: Close: %v", e.ID, err)
				}
			}
			for _, q := range queries() {
				_, _ = a.Search(ctx, q)
			}
			synctest.Wait()
			n = len(home.seen())
		})
		return n
	}
	var silenced []provider.ProviderID
	for _, e := range providers.New().Entries {
		if asks(e, false) > 0 && asks(e, true) == 0 {
			silenced = append(silenced, e.ID)
		}
	}
	if want := []provider.ProviderID{"gestdown", "hdbits", "subsource"}; !slices.Equal(silenced, want) {
		t.Errorf("adapters a Close silenced %v, want the cache keepers %v", silenced, want)
	}
}

// TestTableCloseEndsRefresh: the anime map's refresh outlives the search
// that started it; the table's Close ends its request and returns once it
// has, and a later search starts none.
func TestTableCloseEndsRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		table := providers.New()
		e := entry(t, "animetosho")
		var mu sync.Mutex
		open, asked := 0, 0
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host != "github.com" {
				http.NotFound(w, r)
				return
			}
			mu.Lock()
			open++
			asked++
			mu.Unlock()
			<-r.Context().Done()
			mu.Lock()
			open--
			mu.Unlock()
		}), e.Hosts...)
		e = table.Entries[slices.IndexFunc(table.Entries, func(x provider.Entry) bool { return x.ID == "animetosho" })]
		a := providertest.Build(t, e, values(t, e))
		_, _ = a.Search(ctx, queries()[1])
		synctest.Wait()
		mu.Lock()
		before := open
		mu.Unlock()
		if before != 1 {
			t.Fatalf("%d refresh requests open after the search returned, want 1", before)
		}
		if err := table.Close(t.Context()); err != nil {
			t.Fatalf("Close: %v", err)
		}
		mu.Lock()
		after := open
		mu.Unlock()
		if after != 0 {
			t.Errorf("%d refresh requests open when Close returned, want none", after)
		}
		_, _ = a.Search(ctx, queries()[1])
		synctest.Wait()
		mu.Lock()
		defer mu.Unlock()
		if asked != 1 {
			t.Errorf("%d refresh requests asked, want no new one after Close", asked)
		}
	})
}

// TestCredentialedEntriesTest: every entry with a Secret or a required
// String builds a Tester, so a connection test can check its credentials.
func TestCredentialedEntriesTest(t *testing.T) {
	credentialed := 0
	for _, e := range providers.New().Entries {
		if !slices.ContainsFunc(e.Schema.Fields, func(f formschema.Field) bool {
			return f.Kind == formschema.Secret || f.Kind == formschema.String && f.Required
		}) {
			continue
		}
		credentialed++
		if _, ok := providertest.Build(t, e, values(t, e)).(provider.Tester); !ok {
			t.Errorf("%s takes credentials and builds no Tester", e.ID)
		}
	}
	if credentialed != 9 {
		t.Errorf("%d credentialed entries, want 9 (assrt, betaseries, hdbits, jimaku, opensubtitles, subdl, subsource, subsro, subx)", credentialed)
	}
}
