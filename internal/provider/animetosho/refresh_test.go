package animetosho_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

// The documents a refresh test swaps between: under docA S01E01 of series 100
// has no landing, so a search goes by title; under docB it lands on AniDB
// episode 1101, searched by ?eid=.
var (
	docA = doc(listed(1, mapread.Last{Regular: 1}, one(mapread.Regular, 1, []int{1001}, at(1, 2))))
	docB = doc(listed(1, mapread.Last{Regular: 1}, one(mapread.Regular, 1, []int{1101}, at(1, 1))))
)

// usedB reports whether the searches r ran read docB since the fake's
// request count was mark.
func usedB(r *rig, mark int) bool {
	return slices.Contains(r.at.queries()[mark:], "eid=1101")
}

// fresh is a rig whose map holds docA's index, read at the bubble's start.
func fresh(t *testing.T) *rig {
	t.Helper()
	r := newRig(t, newGitHub(t, "v2026.11.01", docA), newTosho())
	r.search(t, episodeQuery(t, series, 1, 1))
	if n := r.gh.count("/animap.json"); n != 1 {
		t.Fatalf("the first search downloaded %d documents", n)
	}
	return r
}

// stale is fresh, 6 hours later, with tag v2026.11.02 publishing docB and
// fail, when set, answering GitHub's requests first.
func stale(t *testing.T, fail func(http.ResponseWriter, *http.Request) bool) *rig {
	t.Helper()
	r := fresh(t)
	time.Sleep(6 * time.Hour)
	r.gh.publish("v2026.11.02", release{body: encode(t, docB)})
	r.gh.setFail(fail)
	return r
}

// slowAsset delays the document's download by d.
func slowAsset(d time.Duration) func(http.ResponseWriter, *http.Request) bool {
	return func(_ http.ResponseWriter, r *http.Request) bool {
		if r.Host == assetHost && strings.HasSuffix(r.URL.Path, "/animap.json") {
			time.Sleep(d)
		}
		return false
	}
}

func TestAnimapLeaderCancelKeepsFetch(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := stale(t, slowAsset(3*time.Second))
		mark := len(r.at.queries())
		reports := &reportLog{}
		animetosho.NewEntry(r.m).Data(reports.add)
		leader, cancel := context.WithCancel(r.ctx)
		var wg sync.WaitGroup
		wg.Go(func() { _, _ = r.a.Search(leader, episodeQuery(t, series, 1, 1)) })
		var err error
		wg.Go(func() {
			time.Sleep(500 * time.Millisecond)
			_, err = r.a.Search(r.ctx, episodeQuery(t, series, 1, 1))
		})
		time.Sleep(time.Second)
		cancel()
		wg.Wait()
		if err != nil || !usedB(r, mark) {
			t.Errorf("the waiting search: %v, read docB %v; want the refreshed index", err, usedB(r, mark))
		}
		if last := reports.last(); last.active {
			t.Errorf("the map reports %+v after a good refresh", last)
		}
		// No hold-off: the next stale check at 6 hours asks again.
		time.Sleep(6 * time.Hour)
		r.search(t, episodeQuery(t, series, 1, 1))
		if n := r.gh.count("/latest"); n != 3 {
			t.Errorf("%d latest requests; want 3", n)
		}
	})
}

func TestAnimapCancelThenRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := stale(t, slowAsset(3*time.Second))
		mark := len(r.at.queries())
		first, cancel := context.WithCancel(r.ctx)
		go func() {
			time.Sleep(time.Second)
			cancel()
		}()
		_, _ = r.a.Search(first, episodeQuery(t, series, 1, 1))
		r.search(t, episodeQuery(t, series, 1, 1))
		if !usedB(r, mark) || r.gh.count("/latest") != 2 || r.gh.count("/animap.json") != 2 {
			t.Errorf("retry read docB %v after %d latest and %d document requests; want docB from the one refresh",
				usedB(r, mark), r.gh.count("/latest"), r.gh.count("/animap.json"))
		}
	})
}

func TestAnimapRefreshByTag(t *testing.T) {
	t.Run("the held tag renews the index", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := fresh(t)
			time.Sleep(6 * time.Hour)
			r.search(t, episodeQuery(t, series, 1, 1))
			time.Sleep(5 * time.Hour)
			r.search(t, episodeQuery(t, series, 1, 1))
			if r.gh.count("/latest") != 2 || r.gh.count(".sha256") != 1 || r.gh.count("/animap.json") != 1 {
				t.Errorf("requests %q; want one more latest and no download", r.gh.requests())
			}
		})
	})
	t.Run("a new tag swaps the index", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := stale(t, slowAsset(12*time.Second))
			mark := len(r.at.queries())
			r.search(t, episodeQuery(t, series, 1, 1))
			if usedB(r, mark) {
				t.Error("a search during the download read the new index")
			}
			time.Sleep(5 * time.Second)
			mark = len(r.at.queries())
			r.search(t, episodeQuery(t, series, 1, 1))
			if !usedB(r, mark) || r.gh.count(".sha256") != 2 || r.gh.count("/animap.json") != 2 {
				t.Errorf("read docB %v after requests %q; want one sidecar and one document more", usedB(r, mark), r.gh.requests())
			}
		})
	})
	t.Run("every adapter of the entry reads one map", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := fresh(t)
			before := len(r.gh.requests())
			second := providertest.Build(t, animetosho.NewEntry(r.m), "")
			if _, err := second.Search(r.ctx, episodeQuery(t, series, 1, 1)); err != nil {
				t.Fatal(err)
			}
			if n := len(r.gh.requests()); n != before {
				t.Errorf("a second adapter sent %d GitHub requests", n-before)
			}
		})
	})
}

// refusals are releases mapread refuses for their content.
func refusals(t *testing.T) map[string]release {
	v3 := *docB
	v3.Version = 3
	bad := "0000000000000000000000000000000000000000000000000000000000000000  animap.json\n"
	invalid := *docB
	invalid.Anime = []mapread.Anime{{AniDBID: 0, Type: "TV", Last: mapread.Last{Regular: 1}}}
	return map[string]release{
		"version 3":        {body: encode(t, &v3)},
		"sidecar mismatch": {body: encode(t, docB), sidecar: &bad},
		"invalid row":      {body: encode(t, &invalid)},
		"not animap's":     {body: []byte(`{"hello":"world"}`)},
		"empty sidecar":    {body: encode(t, docB), sidecar: new(string)},
		"over 32 MiB":      {body: bytes.Repeat([]byte(" "), 32<<20+1)},
		"not JSON":         {body: []byte("<html>")},
	}
}

func TestAnimapRefusedTagNotRefetched(t *testing.T) {
	for name, rel := range refusals(t) {
		if name == "over 32 MiB" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := capture.Default(t)
				r := fresh(t)
				time.Sleep(6 * time.Hour)
				r.gh.publish("v2026.11.02", rel)
				mark := len(r.at.queries())
				r.search(t, episodeQuery(t, series, 1, 1))
				if usedB(r, mark) {
					t.Fatal("a refused document was read")
				}
				docs := r.gh.count("/animap.json")
				// Searches every 5 minutes for 2 hours ask latest once per 30
				// minutes and never download the refused tag again.
				for range 24 {
					time.Sleep(5 * time.Minute)
					r.search(t, episodeQuery(t, series, 1, 1))
				}
				if n := r.gh.count("/latest"); n != 2+4 {
					t.Errorf("%d latest requests over 2 hours; want one per 30 minutes", n-2)
				}
				if n := r.gh.count("/animap.json"); n != docs {
					t.Errorf("the refused tag downloaded %d more times", n-docs)
				}
				if n := rec.CountLevel(slog.LevelWarn, "anime map refresh failed"); n != 1 {
					t.Errorf("%d WARN lines for one refused tag; want 1", n)
				}
				r.gh.publish("v2026.11.03", release{body: encode(t, docB)})
				time.Sleep(30 * time.Minute)
				mark = len(r.at.queries())
				r.search(t, episodeQuery(t, series, 1, 1))
				if !usedB(r, mark) {
					t.Error("a newer tag was not fetched")
				}
				// A new Map, as after a restart, tries the refused tag once more.
				r.gh.publish("v2026.11.02", rel)
				restarted := newRig(t, r.gh, newTosho())
				before := r.gh.count("/animap.json.sha256")
				restarted.search(t, episodeQuery(t, series, 1, 1))
				if r.gh.count("/animap.json.sha256") != before+1 {
					t.Error("a restarted map did not try the refused tag")
				}
			})
		})
	}
}

// TestAnimapRefreshClassifies: every failure keeps the held index and is
// no failure of the search; a permanent one refuses its tag.
func TestAnimapRefreshClassifies(t *testing.T) {
	type failure struct {
		answer    func(w http.ResponseWriter, r *http.Request) bool
		rel       *release
		permanent bool
		status    int
	}
	onAsset := func(f func(w http.ResponseWriter)) func(http.ResponseWriter, *http.Request) bool {
		return func(w http.ResponseWriter, r *http.Request) bool {
			if r.Host == assetHost && strings.HasSuffix(r.URL.Path, "/animap.json") {
				f(w)
				return true
			}
			return false
		}
	}
	rels := refusals(t)
	pick := func(n string) *release { r := rels[n]; return &r }
	tests := map[string]failure{
		"503":              {answer: onAsset(func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) }), status: 503},
		"429":              {answer: onAsset(func(w http.ResponseWriter) { w.WriteHeader(http.StatusTooManyRequests) }), status: 429},
		"404 on the asset": {answer: onAsset(func(w http.ResponseWriter) { w.WriteHeader(http.StatusNotFound) }), permanent: true, status: 404},
		"redirect to elsewhere": {answer: func(w http.ResponseWriter, r *http.Request) bool {
			if r.Host == ghHost && strings.Contains(r.URL.Path, "/download/") {
				w.Header().Set("Location", "https://evil.example/animap.json")
				w.WriteHeader(http.StatusFound)
				return true
			}
			return false
		}, permanent: true, status: 302},
		"body short of its length": {answer: onAsset(func(w http.ResponseWriter) {
			w.Header().Set("Content-Length", "100000")
			_, _ = w.Write([]byte(`{"version":2`))
		})},
		"the 2-minute timeout": {answer: onAsset(func(w http.ResponseWriter) { stall(w) })},
		"empty sidecar":        {rel: pick("empty sidecar"), permanent: true},
		"sidecar mismatch":     {rel: pick("sidecar mismatch"), permanent: true},
		"over 32 MiB":          {rel: pick("over 32 MiB"), permanent: true},
		"version 3":            {rel: pick("version 3"), permanent: true},
		"not animap's":         {rel: pick("not animap's"), permanent: true},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				rec := capture.Default(t)
				r := fresh(t)
				time.Sleep(6 * time.Hour)
				if tt.rel != nil {
					r.gh.publish("v2026.11.02", *tt.rel)
				} else {
					r.gh.publish("v2026.11.02", release{body: encode(t, docB)})
					r.gh.setFail(tt.answer)
				}
				mark := len(r.at.queries())
				if _, err := r.a.Search(r.ctx, episodeQuery(t, series, 1, 1)); err != nil {
					t.Errorf("search: %v; a refresh failure is no search failure", err)
				}
				time.Sleep(3 * time.Minute)
				if usedB(r, mark) {
					t.Error("the held index was not kept")
				}
				const msg = "anime map refresh failed"
				class := map[bool]string{true: "permanent", false: "transient"}[tt.permanent]
				if !rec.HasAttr(msg, "class", class) || !rec.HasAttr(msg, "tag", "v2026.11.02") || rec.CountLevel(slog.LevelWarn, msg) != 1 {
					t.Errorf("log %q; want one WARN of class %s naming the tag", rec.Messages(), class)
				}
				if tt.status != 0 && !rec.HasAttr(msg, "status", strconv.Itoa(tt.status)) {
					t.Errorf("log names no status %d", tt.status)
				}
				if name == "version 3" && !rec.HasAttr(msg, "version", "3") {
					t.Error("log names no version")
				}
				// After the hold-off a permanent refusal is not downloaded
				// again; a transient one is.
				r.gh.setFail(nil)
				docs := r.gh.count(".sha256")
				time.Sleep(30 * time.Minute)
				r.search(t, episodeQuery(t, series, 1, 1))
				if again := r.gh.count(".sha256") > docs; again == tt.permanent {
					t.Errorf("downloaded again %v; want %v", again, !tt.permanent)
				}
			})
		})
	}
}

func TestAnimapRefreshOnDemand(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := newRig(t, newGitHub(t, "v2026.11.01", docA), newTosho())
			time.Sleep(48 * time.Hour)
			if n := len(r.gh.requests()); n != 0 {
				t.Errorf("an idle map sent %d requests", n)
			}
		})
	})
	t.Run("stale", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := fresh(t)
			for range 3 {
				time.Sleep(6 * time.Hour)
				r.search(t, episodeQuery(t, series, 1, 1))
			}
			if r.gh.count("/latest") != 4 || r.gh.count("/animap.json") != 1 {
				t.Errorf("requests %q; want one download and a latest per stale check", r.gh.requests())
			}
		})
	})
	t.Run("hanging GitHub", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			g := newGitHub(t, "v2026.11.01", docA)
			g.setFail(func(_ http.ResponseWriter, r *http.Request) bool {
				<-r.Context().Done()
				return true
			})
			r := newRig(t, g, newTosho())
			for range 6 {
				if _, err := r.a.Search(r.ctx, episodeQuery(t, series, 1, 1)); err != nil {
					t.Errorf("search: %v", err)
				}
				time.Sleep(100 * time.Second)
			}
			if n := g.count("/latest"); n != 1 {
				t.Errorf("%d attempts in 10 minutes; want one", n)
			}
		})
	})
}

// stall sends a 200's headers and then nothing for 3 minutes, so the
// refresh's own 2-minute timeout ends the read.
func stall(w http.ResponseWriter) {
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	time.Sleep(3 * time.Minute)
}

type report struct {
	at     time.Time
	text   string
	active bool
}

// reportLog records each report the map makes.
type reportLog struct {
	mu   sync.Mutex
	seen []report
}

func (l *reportLog) add(text string, active bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seen = append(l.seen, report{at: time.Now(), text: text, active: active})
}

func (l *reportLog) all() []report {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.seen)
}

func (l *reportLog) last() report {
	all := l.all()
	if len(all) == 0 {
		return report{}
	}
	return all[len(all)-1]
}

func TestAnimapStaleAlert(t *testing.T) {
	failLatest := func(g *github) {
		g.setFail(func(w http.ResponseWriter, r *http.Request) bool {
			if strings.HasSuffix(r.URL.Path, "/latest") {
				w.WriteHeader(http.StatusBadGateway)
				return true
			}
			return false
		})
	}
	t.Run("registration reports at once", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := fresh(t)
			l := &reportLog{}
			animetosho.NewEntry(r.m).Data(l.add)
			if got := l.all(); len(got) != 1 || got[0].active {
				t.Errorf("reports %+v; want one inactive", got)
			}
		})
	})
	t.Run("failing refreshes raise it at 24 hours", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			rec := capture.Default(t)
			start := time.Now()
			r := fresh(t)
			l := &reportLog{}
			animetosho.NewEntry(r.m).Data(l.add)
			failLatest(r.gh)
			for time.Since(start) < 25*time.Hour {
				time.Sleep(30 * time.Minute)
				r.search(t, episodeQuery(t, series, 1, 1))
			}
			for _, rp := range l.all()[1:] {
				if want := rp.at.Sub(start) >= 24*time.Hour; rp.active != want {
					t.Errorf("at %v active %v; want %v", rp.at.Sub(start), rp.active, want)
				}
			}
			want := "AnimeTosho's anime map has not refreshed since 2000-01-01T00:00:00Z: https://github.com/cplieger/animap/releases/latest answered HTTP 502"
			if last := l.last(); last.text != want {
				t.Errorf("text %q; want %q", last.text, want)
			}
			if n := rec.CountLevel(slog.LevelWarn, "anime map refresh failed"); n != 1 {
				t.Errorf("%d WARN lines for one streak; want 1", n)
			}
		})
	})
	t.Run("an attempt outliving its caller reports when it ends", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			r := fresh(t)
			l := &reportLog{}
			animetosho.NewEntry(r.m).Data(l.add)
			time.Sleep(24 * time.Hour)
			r.gh.setFail(func(_ http.ResponseWriter, req *http.Request) bool {
				<-req.Context().Done()
				return true
			})
			r.search(t, episodeQuery(t, series, 1, 1))
			time.Sleep(3 * time.Minute)
			if last := l.last(); !last.active || !strings.Contains(last.text, "has not refreshed since") {
				t.Errorf("last report %+v; want active once the timed-out attempt ended", last)
			}
			r.gh.publish("v2026.11.02", release{body: encode(t, docB)})
			r.gh.setFail(slowAsset(12 * time.Second))
			time.Sleep(30 * time.Minute)
			r.search(t, episodeQuery(t, series, 1, 1))
			if !l.last().active {
				t.Fatal("the good refresh ended before its caller returned")
			}
			time.Sleep(5 * time.Second)
			if last := l.last(); last.active {
				t.Errorf("last report %+v; want inactive once the slow good refresh ended", last)
			}
		})
	})
	t.Run("a refused version stays the cause", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			start := time.Now()
			r := fresh(t)
			l := &reportLog{}
			animetosho.NewEntry(r.m).Data(l.add)
			time.Sleep(6 * time.Hour)
			v3 := *docB
			v3.Version = 3
			r.gh.publish("v2026.11.02", release{body: encode(t, &v3)})
			r.search(t, episodeQuery(t, series, 1, 1))
			if l.last().active {
				t.Error("active before 24 hours")
			}
			time.Sleep(24*time.Hour - time.Since(start))
			r.search(t, episodeQuery(t, series, 1, 1))
			if last := l.last(); !last.active || !strings.Contains(last.text, "version 3") {
				t.Errorf("last report %+v; want active naming version 3", last)
			}
		})
	})
	t.Run("a map with no index", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			g := newGitHub(t, "v2026.11.01", docA)
			failLatest(g)
			r := newRig(t, g, newTosho())
			l := &reportLog{}
			animetosho.NewEntry(r.m).Data(l.add)
			r.search(t, episodeQuery(t, series, 1, 1))
			want := "AnimeTosho's anime map could not be downloaded: https://github.com/cplieger/animap/releases/latest answered HTTP 502"
			if last := l.last(); !last.active || last.text != want {
				t.Errorf("last report %+v; want active %q", last, want)
			}
		})
	})
	t.Run("an idle map", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			g := newGitHub(t, "v2026.11.01", docA)
			failLatest(g)
			r := newRig(t, g, newTosho())
			l := &reportLog{}
			animetosho.NewEntry(r.m).Data(l.add)
			time.Sleep(48 * time.Hour)
			if got := l.all(); len(got) != 1 || got[0].active {
				t.Errorf("reports %+v; want only the registration's", got)
			}
		})
	})
	t.Run("a second registration replaces the first", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			g := newGitHub(t, "v2026.11.01", docA)
			failLatest(g)
			r := newRig(t, g, newTosho())
			first, second := &reportLog{}, &reportLog{}
			e := animetosho.NewEntry(r.m)
			e.Data(first.add)
			e.Data(second.add)
			r.search(t, episodeQuery(t, series, 1, 1))
			if len(first.all()) != 1 || len(second.all()) != 2 || !second.last().active {
				t.Errorf("first %+v, second %+v; want the second alone called", first.all(), second.all())
			}
		})
	})
}
