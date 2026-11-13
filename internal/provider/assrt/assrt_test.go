package assrt_test

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/rank"
)

func failure(t *testing.T, err error) *provider.Failure {
	t.Helper()
	f, ok := errors.AsType[*provider.Failure](err)
	if !ok {
		t.Fatalf("%v, want a *provider.Failure", err)
	}
	return f
}

// TestAssrtTokenNeverInURL: the token goes to api.assrt.net as a bearer
// header only (https://assrt.net/api/doc, "使用Token" method two), never in
// a URL, a log line or a Failure, and never to the file host.
func TestAssrtTokenNeverInURL(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	synctest.Test(t, func(t *testing.T) {
		s := recorded()
		ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
		a := build(t, entry())
		got, err := a.Search(ctx, inception("zh-Hans"))
		if err != nil || len(got) == 0 {
			t.Fatalf("search: %d candidates, %v", len(got), err)
		}
		if _, err := a.Fetch(ctx, got[0].Ref); err != nil {
			t.Fatalf("fetch: %v", err)
		}
		if err := a.(provider.Tester).Test(ctx); err != nil {
			t.Fatalf("test: %v", err)
		}
		failing := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(r.Header.Get("Authorization")))
		}), apiHost, fileHost)
		_, err = build(t, entry()).Search(failing, inception("zh-Hans"))
		if f := failure(t, err); strings.Contains(f.Error(), token) {
			t.Errorf("failure %q holds the token", f)
		}
		for _, r := range s.seen() {
			auth := r.Header.Get("Authorization")
			if strings.Contains(r.URL.String(), token) || r.URL.Query().Has("token") {
				t.Errorf("%s: the URL holds the token", r.URL)
			}
			switch r.Host {
			case apiHost:
				if auth != "Bearer "+token {
					t.Errorf("%s: Authorization %q, want the bearer token", r.URL.Path, auth)
				}
			case fileHost:
				if auth != "" {
					t.Errorf("%s: the download carries Authorization %q", r.URL.Path, auth)
				}
			}
		}
	})
	if strings.Contains(logs.String(), token) {
		t.Errorf("a log line holds the token: %s", logs.String())
	}
}

// TestAssrtStatusInBody: every answer's status decides it, whatever its
// HTTP status, as testdata/status.md records: a token error Auth, a quota
// status RateLimited for a window, an unknown id NotFound, a server status
// Transient, any other Permanent; never an answer.
func TestAssrtStatusInBody(t *testing.T) {
	for _, c := range []struct {
		name   string
		answer route
		body   string
		fetch  bool
		want   provider.FailureKind
		after  time.Duration
	}{
		{name: "recorded invalid token", answer: route{file: "status-20001.json", status: http.StatusBadRequest}, want: provider.Auth},
		{name: "invalid token in a 200", body: `{"errmsg":"invalid token","status":20001}`, want: provider.Auth},
		{name: "no such user", body: `{"errmsg":"no such user","status":1}`, want: provider.Auth},
		{name: "quota", body: `{"errmsg":"you are exceeding request limits","status":30900}`, want: provider.RateLimited, after: time.Minute},
		{name: "quota as a 509", body: `{"errmsg":"you are exceeding request limits","status":30900}`, answer: route{status: 509}, want: provider.RateLimited, after: time.Minute},
		{name: "recorded short keyword", answer: route{file: "status-101.json"}, want: provider.Permanent},
		{name: "recorded missing argument", answer: route{file: "status-20000.json", status: http.StatusBadRequest}, want: provider.Permanent},
		{name: "search engine down", body: `{"errmsg":"search engine is unavailable","status":30002}`, want: provider.Transient},
		{name: "unknown status", body: `{"errmsg":"what","status":7}`, want: provider.Permanent},
		{name: "recorded unknown subtitle", answer: route{file: "status-20900.json"}, fetch: true, want: provider.NotFound},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				answer := c.answer
				if c.body != "" {
					answer.body = c.body
				}
				s := recorded()
				s.routes["/v1/sub/search"], s.routes["/v1/sub/detail"] = answer, answer
				ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
				a := build(t, entry())
				var err error
				if c.fetch {
					_, err = a.Fetch(ctx, provider.NewRef("999999999/x.srt"))
				} else {
					_, err = a.Search(ctx, inception("zh-Hans"))
				}
				if f := failure(t, err); f.Kind != c.want || f.RetryAfter != c.after {
					t.Errorf("%v for %v, want %v for %v", f, f.RetryAfter, c.want, c.after)
				}
			})
		})
	}
}

// TestAssrtSearch pins the one request and its candidates over the recorded
// filelist answers.
func TestAssrtSearch(t *testing.T) {
	t.Run("video string", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded()
			ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
			got, err := build(t, entry()).Search(ctx, inception("zh-Hans", "en"))
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			if p := s.paths(); !slices.Equal(p, []string{apiHost + "/v1/user/quota", apiHost + "/v1/sub/search"}) {
				t.Fatalf("asked %q, want the quota then one search", p)
			}
			q := s.seen()[1].URL.Query()
			if q.Get("q") != "Inception.2010.1080p.BluRay.x264-SPARKS" || q.Get("is_file") != "1" || q.Get("filelist") != "1" || q.Get("cnt") != "15" || q.Has("pos") {
				t.Errorf("search query %v, want the video string as a file, filelist=1, cnt=15 and no pos", q)
			}
			want := []struct{ ref, lang string }{
				// named by their language segment, in the answer's order
				{"626317/" + chs, "zh-Hans"},
				{"442358/Inception.2010.DVDRip.XviD.AC3-ViSiON.eng.srt", "en"},
				{"433837/Inception.2010.BluRay.720p.DTS.2Audio.x264-CHD.chs.srt", "zh-Hans"},
				{"433839/Inception.2010.BluRay.720p.DTS.2Audio.x264-CHD.eng.srt", "en"},
				// the rest by their hit's langlist
				{"679247/Inception.2010.1080p.BluRay.x264-REFiNED.ass", "zh-Hans"},
				{"679247/Inception.2010.1080p.BluRay.x264-REFiNED.ass", "en"},
				{"612777/Inception.2010.BluRay.720p.x264.AC3.2Audios-CMCT.简体&英文.ass", "zh-Hans"},
				{"612777/Inception.2010.BluRay.720p.x264.AC3.2Audios-CMCT.简体&英文.ass", "en"},
				{"612777/Inception.2010.BluRay.720p.x264.AC3.2Audios-CMCT.简体.ass", "zh-Hans"},
				{"612777/Inception.2010.BluRay.720p.x264.AC3.2Audios-CMCT.简体.ass", "en"},
				{"612777/Inception.2010.BluRay.720p.x264.AC3.2Audios-CMCT.英文&简体.ass", "zh-Hans"},
				{"612777/Inception.2010.BluRay.720p.x264.AC3.2Audios-CMCT.英文&简体.ass", "en"},
				{"612777/Inception.2010.BluRay.720p.x264.AC3.2Audios-CMCT.英文.ass", "zh-Hans"},
				{"612777/Inception.2010.BluRay.720p.x264.AC3.2Audios-CMCT.英文.ass", "en"},
			}
			if len(got) != len(want) {
				var refs []string
				for i := range got {
					refs = append(refs, got[i].Ref.Token()+" "+got[i].Lang.String())
				}
				t.Fatalf("candidates %q, want %d", refs, len(want))
			}
			for i, w := range want {
				g := &got[i]
				_, name, _ := strings.Cut(w.ref, "/")
				if g.Ref.Token() != w.ref || g.Lang != tags(w.lang)[0] || g.Release != name || g.MatchedBy != rank.Title || g.Covers != nil || g.Year != 0 {
					t.Errorf("candidate %d: %s %v %q matched by %v, want %s %s matched by name", i, g.Ref.Token(), g.Lang, g.Release, g.MatchedBy, w.ref, w.lang)
				}
			}
		})
	})
	t.Run("episode in one language", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded()
			s.routes["/v1/sub/search"] = route{file: "search.json"}
			ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
			q := inception("zh-Hant")
			q.Video = "Breaking.Bad.S01E03.720p.BluRay.x264-DEMAND"
			got, err := build(t, entry()).Search(ctx, q)
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			var refs []string
			for i := range got {
				if got[i].Lang != tags("zh-Hant")[0] {
					t.Errorf("candidate %s in %v, want zh-Hant only", got[i].Ref.Token(), got[i].Lang)
				}
				refs = append(refs, got[i].Ref.Token())
			}
			for _, r := range []string{
				"710960/The.Bad.Guys.Breaking.In.S01E01.2025.1080p.NF.WEB-DL.DDP5.1.H264-HHWEB.cht.srt",
				"95007/Breaking.Bad.S01E03.720p.HDTV.X264-DIMENSION.big5.srt",
			} {
				if !slices.Contains(refs, r) {
					t.Errorf("no candidate %s", r)
				}
			}
			for _, r := range refs {
				if strings.Contains(r, ".gb.") || strings.Contains(r, ".chs.") || strings.Contains(r, ".eng.") || strings.HasPrefix(r, "620540/") {
					t.Errorf("candidate %s: its segment or langlist names no zh-Hant", r)
				}
			}
		})
	})
	t.Run("title when the video string is short", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded()
			ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
			q := inception("zh-Hans")
			q.Video = "ab"
			if _, err := build(t, entry()).Search(ctx, q); err != nil {
				t.Fatalf("search: %v", err)
			}
			sq := s.seen()[1].URL.Query()
			if sq.Get("q") != "Inception" || sq.Has("is_file") {
				t.Errorf("search query %v, want the first title, not as a file", sq)
			}
		})
	})
	t.Run("archives it cannot open", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded()
			s.routes["/v1/sub/search"] = route{body: `{"status":0,"sub":{"subs":[` +
				`{"id":1,"filename":"a.7z","lang":{"langlist":{"langchs":true}},"filelist":[{"f":"a.chs.srt"}]},` +
				`{"id":2,"filename":"b.rar","lang":{"langlist":{"langchs":true}},"filelist":[{"f":"b.sup"},{"f":"b.srt"}]},` +
				`{"id":3,"filename":"c.srt","lang":{"langlist":{"langchs":true}},"filelist":{}},` +
				`{"id":4,"lang":{"langlist":{"langchs":true}},"filelist":[{"f":"d.srt"}]}]}}`}
			ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
			got, err := build(t, entry()).Search(ctx, inception("zh-Hans"))
			if err != nil {
				t.Fatalf("search: %v", err)
			}
			var refs []string
			for i := range got {
				refs = append(refs, got[i].Ref.Token())
			}
			if want := []string{"2/b.srt", "3/c.srt"}; !slices.Equal(refs, want) {
				t.Errorf("candidates %q, want %q: no 7z, image subtitle or nameless hit", refs, want)
			}
		})
	})
	t.Run("skipped unsent", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded()
			ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
			short := inception("zh-Hans")
			short.Video, short.Titles = "", []string{"Up"}
			french := inception("fr")
			noKind := inception("zh-Hans")
			noKind.Kind = 0
			for _, q := range []provider.Query{short, french, noKind} {
				if _, err := build(t, entry()).Search(ctx, q); !isSkipped(err) {
					t.Errorf("query %q %q %v: %v, want a Skipped", q.Video, q.Titles, q.Langs, err)
				}
			}
			if n := len(s.seen()); n != 0 {
				t.Errorf("%d requests sent, want none", n)
			}
		})
	})
}

func isSkipped(err error) bool {
	_, ok := errors.AsType[*provider.Skipped](err)
	return ok
}

// TestAssrtFetch: a Fetch asks the detail for a fresh link, downloads the
// archive over https on file0.assrt.net and extracts the member the Ref
// names.
func TestAssrtFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		away := &site{}
		ctx := providertest.Serve(t, t.Context(), away, foreign)
		s := recorded()
		ctx = providertest.Serve(t, ctx, s, apiHost, fileHost)
		a := build(t, entry())
		members := map[string][]byte{}
		for _, name := range []string{chs, cht} {
			b, err := a.Fetch(ctx, provider.NewRef("626317/"+name))
			if err != nil {
				t.Fatalf("fetch %s: %v", name, err)
			}
			members[name] = b
		}
		want := map[string][]byte{}
		for _, name := range []string{chs, cht} {
			want[name] = extract(t, name)
		}
		for name, b := range members {
			if !bytes.Equal(b, want[name]) || len(b) == 0 {
				t.Errorf("fetch %s: %d bytes, want the member's %d", name, len(b), len(want[name]))
			}
		}
		reqs := s.seen()
		if p := s.paths(); !slices.Equal(p, []string{
			apiHost + "/v1/user/quota", apiHost + "/v1/sub/detail", fileHost + rarPath, apiHost + "/v1/sub/detail", fileHost + rarPath,
		}) {
			t.Fatalf("asked %q, want a detail then the archive per fetch", p)
		}
		if reqs[1].URL.Query().Get("id") != "626317" || reqs[2].URL.Scheme != "" && reqs[2].URL.Scheme != "https" || reqs[2].URL.Query().Get("api") != "1" {
			t.Errorf("detail %s, download %s: want id 626317 and the detail's link", reqs[1].URL, reqs[2].URL)
		}
		_, err := a.Fetch(ctx, provider.NewRef("626317/missing.srt"))
		if f := failure(t, err); f.Kind != provider.Permanent {
			t.Errorf("a name the archive lacks: %v, want Permanent", f)
		}
		for _, tok := range []string{"626317", "626317/", "0626317/x.srt", "x/y.srt", "-1/x.srt"} {
			if f := failure(t, func() error { _, err := a.Fetch(ctx, provider.NewRef(tok)); return err }()); f.Kind != provider.Permanent {
				t.Errorf("fetch %q: %v, want Permanent", tok, f)
			}
		}
		if n := len(s.seen()); n != 7 {
			t.Errorf("%d requests, want the missing member's detail and archive only after the two fetches", n)
		}
		if n := len(away.seen()); n != 0 {
			t.Errorf("the foreign host answered %d requests", n)
		}
	})
}

// extract is the recorded archive's member name, read as the engine would.
func extract(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/dl-626317.rar")
	if err != nil {
		t.Fatal(err)
	}
	m, err := archive.Extract(b, func(names []string) (int, error) { return slices.Index(names, name), nil })
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestAssrtFetchLinks: a detail link off file0.assrt.net, or not to an
// archive, is refused before dialing; a single file the detail names is
// returned as served.
func TestAssrtFetchLinks(t *testing.T) {
	for _, c := range []struct {
		name, link, filename string
		ok                   bool
	}{
		{"http made https", "http://file0.assrt.net/download/1/a.srt?api=1", "a.srt", true},
		{"https", "https://file0.assrt.net/download/1/a.srt", "a.srt", true},
		{"foreign host", "http://" + foreign + "/download/1/a.srt", "a.srt", false},
		{"the site", "https://assrt.net/download/1/a.srt", "a.srt", false},
		{"the API host", "https://api.assrt.net/download/1/a.srt", "a.srt", false},
		{"onthefly", "http://file0.assrt.net/onthefly/1/-/1/a.srt", "a.srt", false},
		{"dot segments", "http://file0.assrt.net/download/../v1/user/quota", "a.srt", false},
		{"userinfo", "http://u@file0.assrt.net/download/1/a.srt", "a.srt", false},
		{"no archive, another name", "https://file0.assrt.net/download/1/b.srt", "b.srt", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				away := &site{}
				ctx := providertest.Serve(t, t.Context(), away, foreign, "assrt.net")
				var mu sync.Mutex
				var got []string
				ctx = providertest.Serve(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					mu.Lock()
					got = append(got, r.Host+r.URL.Path)
					mu.Unlock()
					switch r.URL.Path {
					case "/v1/user/quota":
						_, _ = w.Write([]byte(`{"status":0,"user":{"quota":5}}`))
					case "/v1/sub/detail":
						_, _ = w.Write([]byte(`{"status":0,"sub":{"subs":[{"url":"` + c.link + `","filename":"` + c.filename + `"}]}}`))
					default:
						_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nhi\n"))
					}
				}), apiHost, fileHost)
				b, err := build(t, entry()).Fetch(ctx, provider.NewRef("1/a.srt"))
				if c.ok {
					if err != nil || !bytes.HasPrefix(b, []byte("1\n")) {
						t.Fatalf("fetch: %q, %v", b, err)
					}
					return
				}
				if f := failure(t, err); f.Kind != provider.Permanent {
					t.Errorf("%v, want Permanent", f)
				}
				if n := len(away.seen()); n != 0 {
					t.Errorf("%d requests left the file host", n)
				}
			})
		})
	}
}

// TestAssrtPacing: back-to-back calls of every kind, from two adapters of
// one entry as an engine swap leaves them, never put more than the quota
// user/quota reports, at most 20, in any 60 s window; the quota is read
// before the first request and again a window after a quota status, and a
// quota of 0 waits a window.
func TestAssrtPacing(t *testing.T) {
	for _, quota := range []string{"5", "20", "40"} {
		t.Run("quota "+quota, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := recorded()
				s.routes["/v1/user/quota"] = route{body: `{"status":0,"user":{"quota":` + quota + `}}`}
				ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
				e := entry()
				a, b := build(t, e), build(t, e)
				var wg sync.WaitGroup
				for i := range 24 {
					ad := a
					if i%2 == 1 {
						ad = b
					}
					wg.Go(func() {
						if i%3 == 0 {
							_, _ = ad.Fetch(ctx, provider.NewRef("626317/"+chs))
							return
						}
						_, _ = ad.Search(ctx, inception("zh-Hans"))
					})
				}
				wg.Wait()
				n, _ := strconv.Atoi(quota)
				limit := min(n, 20)
				at := s.spending()
				if len(at) != 32 {
					t.Fatalf("%d quota-spending requests, want 16 searches and 8 fetches of 2", len(at))
				}
				for i := range at {
					n := 0
					for _, u := range at[i:] {
						if u.Sub(at[i]) < time.Minute {
							n++
						}
					}
					if n > limit {
						t.Fatalf("%d requests in the 60 s from %v, want at most %d", n, at[i].Sub(at[0]), limit)
					}
				}
				if q := s.quotaReads(); q != 1 {
					t.Errorf("user/quota read %d times, want once for both adapters", q)
				}
			})
		})
	}
	t.Run("quota status", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded()
			var mu sync.Mutex
			refused, reads := false, []time.Time{}
			var status time.Time
			ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch {
				case r.URL.Path == "/v1/user/quota":
					reads = append(reads, time.Now())
					_, _ = w.Write([]byte(`{"status":0,"user":{"quota":5}}`))
				case !refused:
					refused, status = true, time.Now()
					_, _ = w.Write([]byte(`{"errmsg":"you are exceeding request limits","status":30900}`))
				default:
					s.ServeHTTP(w, r)
				}
			}), apiHost, fileHost)
			a := build(t, entry())
			_, err := a.Search(ctx, inception("zh-Hans"))
			if f := failure(t, err); f.Kind != provider.RateLimited {
				t.Fatalf("quota status: %v, want RateLimited", f)
			}
			if _, err := a.Search(ctx, inception("zh-Hans")); err != nil {
				t.Fatalf("search after the quota status: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(reads) != 2 || reads[1].Sub(status) < time.Minute {
				t.Errorf("quota read at %v after the status at %v, want once more a window after it", reads, status)
			}
		})
	})
	t.Run("quota 0 then 5", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var mu sync.Mutex
			var reads []time.Time
			s := recorded()
			ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/v1/user/quota" {
					mu.Lock()
					reads = append(reads, time.Now())
					n := len(reads)
					mu.Unlock()
					q := "5"
					if n == 1 {
						q = "0"
					}
					_, _ = w.Write([]byte(`{"status":0,"user":{"quota":` + q + `}}`))
					return
				}
				s.ServeHTTP(w, r)
			}), apiHost, fileHost)
			start := time.Now()
			if _, err := build(t, entry()).Search(ctx, inception("zh-Hans")); err != nil {
				t.Fatalf("search: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(reads) != 2 || reads[1].Sub(start) < time.Minute {
				t.Errorf("quota reads %v from %v, want a second a window after the 0", reads, start)
			}
		})
	})
	t.Run("cancelled while waiting", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(`{"status":0,"user":{"quota":0}}`))
			}), apiHost, fileHost)
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if _, err := build(t, entry()).Search(cctx, inception("zh-Hans")); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("search: %v, want the context's deadline", err)
			}
		})
	})
}

// TestAssrtTester: Test reads the quota, and the recorded invalid token is
// Auth.
func TestAssrtTester(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := recorded()
		ctx := providertest.Serve(t, t.Context(), s, apiHost, fileHost)
		if err := build(t, entry()).(provider.Tester).Test(ctx); err != nil {
			t.Fatalf("test: %v", err)
		}
		if p := s.paths(); !slices.Equal(p, []string{apiHost + "/v1/user/quota"}) {
			t.Errorf("asked %q, want user/quota", p)
		}
		refused := providertest.Serve(t, t.Context(), &site{routes: map[string]route{"/v1/user/quota": {file: "status-20001.json", status: http.StatusBadRequest}}}, apiHost)
		if f := failure(t, build(t, entry()).(provider.Tester).Test(refused)); f.Kind != provider.Auth {
			t.Errorf("invalid token: %v, want Auth", f)
		}
	})
}

// TestProviderMaps: the three langlist keys of one language map, langdou
// does not.
func TestProviderMaps(t *testing.T) {
	m := entry().Languages
	for label, want := range map[string]string{"langchs": "zh-Hans", "langcht": "zh-Hant", "langeng": "en"} {
		if tg, ok := m.Tag(label); !ok || tg != tags(want)[0] {
			t.Errorf("%s maps to %v, %t, want %s", label, tg, ok, want)
		}
	}
	if _, ok := m.Tag("langdou"); ok {
		t.Error("langdou maps to a tag")
	}
}
