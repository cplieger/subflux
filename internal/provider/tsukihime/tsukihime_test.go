package tsukihime_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/animemap"
	"github.com/cplieger/subflux/internal/provider/animetosho"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/tsukihime"
	"github.com/cplieger/subflux/internal/rank"
)

func TestContract(t *testing.T) {
	gh := newGitHub(t, recordedDoc())
	providertest.Contract(t, providertest.Harness{
		Entry:    tsukihime.NewEntry(animemap.New()),
		Query:    episode(t, sasakiTVDB, 2, 5, "en"),
		Recorded: both(gh, recorded(t)),
	})
}

// listOf is a torrent list answer in the recorded rows' shape.
func listOf(rows ...map[string]any) map[string]any {
	return map[string]any{"total": len(rows), "start": 0, "limit": 50, "error": false, "results": rows}
}

func torrentRow(id int, sublangs ...string) map[string]any {
	r := map[string]any{"id": id, "state": "completed", "filecount": 1, "episode_no": 5}
	if sublangs != nil {
		r["sublangs"] = sublangs
	}
	return r
}

// torrentOf is a torrent answer whose files each carry one subtitle track,
// attachment id base+i, in lang, beside a font and a type 3 entry whose info
// also names lang, so only the type keeps them out.
func torrentOf(id, base int, lang string, names ...string) map[string]any {
	var files []map[string]any
	for i, n := range names {
		files = append(files, map[string]any{"id": i, "filename": n, "attachments": []map[string]any{
			{"id": 1, "type": 0, "info": map[string]any{"name": "font.ttf", "lang": lang}},
			{"id": 2, "type": 3, "info": map[string]any{"lang": lang}},
			{"id": base + i, "type": 1, "info": map[string]any{"codec": "ASS", "lang": lang, "name": "subs", "forced": 0, "cached": 1}},
		}})
	}
	return map[string]any{"id": id, "filecount": len(names), "files": files}
}

func TestTsukiHimeLookups(t *testing.T) {
	t.Run("a torrent with no file is not read", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			got := newRig(t, recordedDoc(), s).search(t, episode(t, series, 1, 1, "en"))
			if p := apiPaths(s.requests()); !slices.Equal(p, []string{"/animes/anidb/17617", "/animes/19/episodes/1"}) {
				t.Errorf("requests %q, want the anime and its episode only", p)
			}
			if len(got) != 0 {
				t.Errorf("candidates %v, want none", refs(got))
			}
		})
	})
	t.Run("one torrent's subtitle track", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			s.set("/v1/animes/9823/episodes/5", listOf(torrentRow(37747, "en")))
			got := newRig(t, recordedDoc(), s).search(t, episode(t, sasakiTVDB, 2, 5, "en"))
			if len(got) != 1 {
				t.Fatalf("candidates %v, want attachment 335942 alone", refs(got))
			}
			c := got[0]
			if c.Ref.Token() != "335942" || c.Lang != tag(t, "en") || c.Flags.Forced || c.MatchedBy != rank.ID ||
				!slices.Equal(c.Covers, []media.Numbering{{Scheme: media.TVDB, Season: 2, Episode: 5}}) ||
				c.Release != "[SubsPlease] Sasaki to Pii-chan S2 - 05 (720p) [7E340FBD].mkv" {
				t.Errorf("candidate %+v", c)
			}
		})
	})
	t.Run("an uncached track is still a candidate", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			s.set("/v1/animes/9823/episodes/5", listOf(torrentRow(37747, "en")))
			s.set("/v1/torrents/37747", strings.Replace(read(t, "one.json"), `"cached":1`, `"cached":0`, 1))
			if got := newRig(t, recordedDoc(), s).search(t, episode(t, sasakiTVDB, 2, 5, "en")); !slices.Equal(refs(got), []string{"335942"}) {
				t.Errorf("candidates %v, want 335942", refs(got))
			}
		})
	})
	t.Run("the recorded chain", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			got := newRig(t, recordedDoc(), s).search(t, episode(t, sasakiTVDB, 2, 5, "en"))
			want := []string{
				"/animes/anidb/18563", "/animes/9823/episodes/5",
				"/torrents/37779", "/torrents/37758", "/torrents/37753", "/torrents/37755", "/torrents/37745", "/torrents/37747",
			}
			if p := apiPaths(s.requests()); !slices.Equal(p, want) {
				t.Errorf("requests %q, want %q", p, want)
			}
			// 37779, 37755 and 37747 carry the one track 335942, offered once.
			if r := refs(got); !slices.Equal(r, []string{"335942", "335920", "335992", "335949"}) {
				t.Errorf("candidates %v, want each English track once", r)
			}
		})
	})
	t.Run("a film lists its anime's torrents", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			got := newRig(t, recordedDoc(), s).search(t, movie(t, filmTMDB, "en"))
			want := []string{
				"/animes/anidb/18890", "/torrents?anime_id=9627",
				"/torrents/12643", "/torrents/12625", "/torrents/12616", "/torrents/12607", "/torrents/12606", "/torrents/12605",
			}
			if p := apiPaths(s.requests()); !slices.Equal(p, want) {
				t.Errorf("requests %q, want %q (its episodes/1 answers total 0)", p, want)
			}
			if len(got) == 0 {
				t.Fatal("no candidate for the recorded film")
			}
			for i := range got {
				if got[i].MatchedBy != rank.ID || got[i].Covers != nil || got[i].Lang != tag(t, "en") {
					t.Errorf("film candidate %+v, want an English ID match covering nothing", got[i])
				}
			}
		})
	})
	t.Run("sublang is never sent", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			s.set("/v1/torrents?anime_id=9627", listOf(torrentRow(901, "en-US"), torrentRow(902, "en")))
			s.set("/v1/torrents/901", torrentOf(901, 9010, "en-US", "Film (1080p).mkv"))
			s.set("/v1/torrents/902", torrentOf(902, 9020, "en", "Film (720p).mkv"))
			got := newRig(t, recordedDoc(), s).search(t, movie(t, filmTMDB, "en", "en-US"))
			if r := refs(got); !slices.Equal(r, []string{"9010", "9020"}) {
				t.Errorf("candidates %v, want the en-US and the en torrent's tracks", r)
			}
			for _, r := range s.requests() {
				if strings.Contains(r, "sublang") {
					t.Errorf("request %s filters by sublang", r)
				}
			}
		})
	})
	t.Run("an anime TsukiHime does not list", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			r := newRig(t, doc(tv(99999999, 500, 1, 1, 1)), s)
			got, err := r.a.Search(r.ctx, episode(t, 500, 1, 1, "en"))
			if err != nil || len(got) != 0 {
				t.Errorf("search: %v, %v; want nothing and no error", refs(got), err)
			}
			if p := apiPaths(s.requests()); !slices.Equal(p, []string{"/animes/anidb/99999999"}) {
				t.Errorf("requests %q, want the anime lookup alone", p)
			}
		})
	})
	t.Run("the cap reads torrents labelling the target first", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			s.set("/v1/animes/9823/episodes/5", listOf(
				torrentRow(1, "fr"), torrentRow(2, "fr"), torrentRow(3), torrentRow(4, "de"), torrentRow(5, []string{}...), torrentRow(6, "fr"),
				torrentRow(7, "fr", "en"),
			))
			for id := 1; id <= 7; id++ {
				s.set("/v1/torrents/"+strconv.Itoa(id), torrentOf(id, id*10, "en", "Sasaki S2 - 05.mkv"))
			}
			newRig(t, recordedDoc(), s).search(t, episode(t, sasakiTVDB, 2, 5, "en"))
			want := []string{"/torrents/7", "/torrents/1", "/torrents/2", "/torrents/3", "/torrents/4", "/torrents/5"}
			if p := apiPaths(s.requests()); !slices.Equal(p[2:], want) {
				t.Errorf("torrents read %q, want %q", p[2:], want)
			}
		})
	})
	t.Run("a batch keeps the landing's files", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			s.set("/v1/animes/284/episodes/5", listOf(map[string]any{"id": 367, "filecount": 10, "sublangs": []string{"en"}}))
			got := newRig(t, recordedDoc(), s).search(t, episode(t, series, 2, 5, "en"))
			if r := refs(got); !slices.Equal(r, []string{"4580"}) {
				t.Errorf("candidates %v, want the S2 - 05v2 file's track 4580 alone", r)
			}
		})
	})
	t.Run("a film keeps the files claiming nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			s.set("/v1/torrents?anime_id=9627", listOf(torrentRow(900, "en")))
			s.set("/v1/torrents/900", torrentOf(900, 9000, "en", "Pack/Film (1080p).mkv", "Pack/Film 02 (1080p).mkv", "Pack/Film NCOP.mkv", "Pack/Film - S01E02.mkv"))
			got := newRig(t, recordedDoc(), s).search(t, movie(t, filmTMDB, "en"))
			if r := refs(got); !slices.Equal(r, []string{"9000", "9001"}) {
				t.Errorf("candidates %v, want the two files claiming no episode", r)
			}
		})
	})
	t.Run("a torrent keeping no file is dropped with one line", func(t *testing.T) {
		logs := capture.Default(t)
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			s.set("/v1/animes/284/episodes/5", listOf(torrentRow(903, "en")))
			s.set("/v1/torrents/903", torrentOf(903, 9030, "en", "Show S2 - 01.mkv", "Show S2 - 02.mkv"))
			if got := newRig(t, recordedDoc(), s).search(t, episode(t, series, 2, 5, "en")); len(got) != 0 {
				t.Errorf("candidates %v, want none", refs(got))
			}
		})
		msg := capture.Message("tsukihime: no file of the torrent is the landing's episode")
		if n := logs.CountLevel(slog.LevelDebug, string(msg)); n != 1 {
			t.Fatalf("%d DEBUG lines, want 1", n)
		}
		if ep, _ := logs.AttrValue(msg, "episode"); ep != "5" {
			t.Errorf("episode %q, want 5", ep)
		}
		if n, _ := logs.AttrValue(msg, "files"); n != "2" {
			t.Errorf("files %q, want 2", n)
		}
	})
	t.Run("no landing sends no request", func(t *testing.T) {
		for name, q := range map[string]provider.Query{
			"an episode no anime lands on": episode(t, 777, 1, 1, "en"),
			"a special landing":            episode(t, series, 0, 1, "en"),
			"a movie with no TMDB id":      {Kind: media.Movie, Langs: []language.Tag{tag(t, "en")}},
			"a TMDB-numbered episode":      {Kind: media.Episode, External: media.ExternalIDs{TMDB: 1}, Numbering: media.Numbering{Scheme: media.TMDB, Season: 1, Episode: 1}},
		} {
			t.Run(name, func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					s := recorded(t)
					special := []mapread.Target{{Season: 0, Episode: 1}}
					d := recordedDoc()
					d.Anime[0].Episodes = append(d.Anime[0].Episodes, mapread.Run{Kind: mapread.Special, Start: 1, End: 1, TVDB: &special})
					d.Anime[0].Last.Special = 1
					r := newRig(t, d, s)
					_, err := r.a.Search(r.ctx, q)
					if _, ok := errors.AsType[*provider.Skipped](err); !ok {
						t.Errorf("search: %v, want *provider.Skipped, even when its call refreshed the map", err)
					}
					if p := s.requests(); len(p) != 0 {
						t.Errorf("requests %q, want none", p)
					}
				})
			})
		}
	})
}

// TestTsukiHimeSharesAnimeMap: AnimeTosho and TsukiHime read one map, so one
// refresh serves both, and its condition reaches each entry's report.
func TestTsukiHimeSharesAnimeMap(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := animemap.New()
		gh := newGitHub(t, recordedDoc())
		at, th := animetosho.NewEntry(m), tsukihime.NewEntry(m)
		ctx := providertest.Serve(t, t.Context(), both(gh, recorded(t)), slices.Concat(at.Hosts, th.Hosts[:2])...)
		var mu sync.Mutex
		reports := map[string]string{}
		for _, e := range []provider.Entry{at, th} {
			e.Data(func(text string, active bool) {
				mu.Lock()
				defer mu.Unlock()
				if active {
					reports[string(e.ID)] = text
				} else {
					delete(reports, string(e.ID))
				}
			})
		}
		atA, thA := providertest.Build(t, at, ""), providertest.Build(t, th, "")
		q := episode(t, sasakiTVDB, 2, 5, "en")
		search := func() {
			_, _ = atA.Search(ctx, q)
			_, _ = thA.Search(ctx, q)
		}
		search()
		time.Sleep(7 * time.Hour)
		before := gh.latest.Load()
		search()
		if n := gh.latest.Load() - before; n != 1 {
			t.Errorf("a stale map searched by both asked releases/latest %d times, want 1", n)
		}
		gh.fail.Store(true)
		time.Sleep(25 * time.Hour)
		_, _ = thA.Search(ctx, q)
		mu.Lock()
		defer mu.Unlock()
		for id, name := range map[string]string{"animetosho": "AnimeTosho", "tsukihime": "TsukiHime"} {
			if !strings.HasPrefix(reports[id], name+"'s anime map has not refreshed since ") {
				t.Errorf("%s's report %q, want its own stale-map alert", id, reports[id])
			}
		}
	})
}

// answering serves every TsukiHime request with status and header, and
// counts them.
type answering struct {
	header http.Header
	mu     sync.Mutex
	paths  []string
	status int
}

func (a *answering) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.paths = append(a.paths, r.URL.RequestURI())
	a.mu.Unlock()
	maps.Copy(w.Header(), a.header)
	w.WriteHeader(a.status)
}

func (a *answering) sent() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.paths)
}

func TestTsukiHimeLimits(t *testing.T) {
	t.Run("a Search sends at most 8 requests", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			var rows []map[string]any
			for id := 1; id <= 10; id++ {
				rows = append(rows, torrentRow(id, "en"))
				s.set("/v1/torrents/"+strconv.Itoa(id), torrentOf(id, id*10, "en", "Sasaki S2 - 05.mkv"))
			}
			s.set("/v1/animes/9823/episodes/5", listOf(rows...))
			d := recordedDoc()
			d.Anime = append(d.Anime, tv(20000, sasakiTVDB, 2, 5, 5))
			got := newRig(t, d, s).search(t, episode(t, sasakiTVDB, 2, 5, "en"))
			p := apiPaths(s.requests())
			if len(p) != 8 || slices.Contains(p, "/torrents/7") || slices.Contains(p, "/animes/anidb/20000") {
				t.Errorf("requests %q, want 8: the first landing and 6 torrents, never a 7th or the second landing", p)
			}
			if len(got) != 6 {
				t.Errorf("%d candidates, want the 6 torrents' tracks", len(got))
			}
		})
	})
	for _, c := range []struct {
		header http.Header
		name   string
		status int
		kind   provider.FailureKind
		after  time.Duration
	}{
		{name: "429", status: http.StatusTooManyRequests, header: http.Header{"Retry-After": {"7"}}, kind: provider.RateLimited, after: 7 * time.Second},
		{name: "401", status: http.StatusUnauthorized, kind: provider.Permanent},
		{name: "403", status: http.StatusForbidden, kind: provider.Permanent},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				api := &answering{status: c.status, header: c.header}
				r := newRig(t, recordedDoc(), api)
				_, err := r.a.Search(r.ctx, episode(t, sasakiTVDB, 2, 5, "en"))
				f, ok := errors.AsType[*provider.Failure](err)
				if !ok || f.Kind != c.kind || f.RetryAfter != c.after {
					t.Errorf("search: %v, want %v for %v", err, c.kind, c.after)
				}
			})
		})
	}
	t.Run("a Ref that is no attachment id is refused unsent", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			api := &answering{status: http.StatusOK}
			r := newRig(t, recordedDoc(), api)
			for _, tok := range []string{"https://foreign.example.test/x", "335942.ass", "0335942", "0", "4294967296", "-1", "+5", ""} {
				_, err := r.a.Fetch(r.ctx, provider.NewRef(tok))
				if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent {
					t.Errorf("fetch %q: %v, want Permanent", tok, err)
				}
			}
			if p := api.sent(); len(p) != 0 {
				t.Errorf("requests %q, want none", p)
			}
		})
	})
}

func TestTsukiHimeFetchPath(t *testing.T) {
	t.Run("the recorded attachment", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			r := newRig(t, recordedDoc(), s)
			b, err := r.a.Fetch(r.ctx, provider.NewRef("335942"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(bytes.TrimPrefix(b, []byte("\ufeff")), []byte("[Script Info]")) {
				t.Errorf("payload starts %q, want the decompressed ASS", b[:min(len(b), 20)])
			}
			if p := s.requests(); !slices.Equal(p, []string{"/attach/00052046/335942.xz"}) {
				t.Errorf("requests %q, want the storage path alone", p)
			}
		})
	})
	t.Run("the directory is the id in 8 upper-case hex digits", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			s := recorded(t)
			xz := read(t, "335942.xz")
			for _, id := range []string{"1", "171", "4294967295"} {
				n, _ := strconv.ParseUint(id, 10, 32)
				s.set(fmt.Sprintf("/attach/%08X/%s.xz", n, id), xz)
			}
			r := newRig(t, recordedDoc(), s)
			for _, id := range []string{"1", "171", "4294967295"} {
				if _, err := r.a.Fetch(r.ctx, provider.NewRef(id)); err != nil {
					t.Errorf("fetch %s: %v", id, err)
				}
			}
			want := []string{"/attach/00000001/1.xz", "/attach/000000AB/171.xz", "/attach/FFFFFFFF/4294967295.xz"}
			if p := s.requests(); !slices.Equal(p, want) {
				t.Errorf("requests %q, want %q", p, want)
			}
		})
	})
	t.Run("a redirect is refused, not followed", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			api := &answering{status: http.StatusFound, header: http.Header{"Location": {"https://storage.tsukihime.org/attach/00052046/335942.xz"}}}
			r := newRig(t, recordedDoc(), api)
			_, err := r.a.Fetch(r.ctx, provider.NewRef("335941"))
			if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent {
				t.Errorf("fetch: %v, want Permanent", err)
			}
			if p := api.sent(); !slices.Equal(p, []string{"/attach/00052045/335941.xz"}) {
				t.Errorf("requests %q, want the one storage request", p)
			}
		})
	})
}

// TestProviderMaps: every label and track language the recorded answers
// hold reads through language.Parse, and every track's tag is one the map
// lists, so a target can ask it.
func TestProviderMaps(t *testing.T) {
	offered := tsukihime.NewEntry(animemap.New()).Languages.Tags()
	names, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		if !strings.HasPrefix(n.Name(), "torrent") && !strings.HasPrefix(n.Name(), "e") && n.Name() != "one.json" {
			continue
		}
		var a struct {
			Results  []struct{ Sublangs []string } `json:"results"`
			Sublangs []string                      `json:"sublangs"`
			Files    []struct {
				Attachments []struct {
					Info *struct{ Lang string } `json:"info"`
					Type int                    `json:"type"`
				} `json:"attachments"`
			} `json:"files"`
		}
		if err := json.Unmarshal([]byte(read(t, n.Name())), &a); err != nil {
			t.Fatalf("%s: %v", n.Name(), err)
		}
		labels := slices.Clone(a.Sublangs)
		for _, r := range a.Results {
			labels = append(labels, r.Sublangs...)
		}
		for _, l := range labels {
			if _, ok := language.Parse(l); !ok {
				t.Errorf("%s: sublangs label %q does not parse", n.Name(), l)
			}
		}
		for _, f := range a.Files {
			for _, at := range f.Attachments {
				if at.Type != 1 {
					continue
				}
				if tg, ok := language.Parse(at.Info.Lang); !ok || !slices.Contains(offered, tg) {
					t.Errorf("%s: track language %q is not a listed tag", n.Name(), at.Info.Lang)
				}
			}
		}
	}
}
