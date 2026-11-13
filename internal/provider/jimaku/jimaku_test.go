package jimaku_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/episodematch"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/archive"
	"github.com/cplieger/subflux/internal/provider/jimaku"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/rank"
)

// recorded is a testdata file list's rows.
func recorded(t *testing.T, name string) []struct{ URL, Name string } {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var fs []struct{ URL, Name string }
	if err := json.Unmarshal(b, &fs); err != nil {
		t.Fatal(err)
	}
	return fs
}

// searchFailure runs an episode search against answer and returns its
// Failure.
func searchFailure(t *testing.T, answer http.HandlerFunc) *provider.Failure {
	t.Helper()
	ctx := providertest.Serve(t, t.Context(), answer, host)
	_, err := adapter(t).Search(ctx, episode(6484))
	f, ok := errors.AsType[*provider.Failure](err)
	if !ok {
		t.Fatalf("search: %v, want a *provider.Failure", err)
	}
	return f
}

// TestJimaku429ResetAfter: a 429 without Retry-After pauses Jimaku for its
// x-ratelimit-reset-after, rounded up (https://jimaku.cc/api/docs, "Rate
// Limits"); with Retry-After, or with no usable reset, 5.1's 429 row reads
// it, a zero leaving the funnel's 10-minute default.
func TestJimaku429ResetAfter(t *testing.T) {
	for _, c := range []struct {
		name, reset, retryAfter string
		want                    time.Duration
	}{
		{"fraction rounded up", "12.5", "", 13 * time.Second},
		{"whole seconds", "7", "", 7 * time.Second},
		{"under a second", "0.98", "", time.Second},
		{"Retry-After wins", "12.5", "30", 30 * time.Second},
		{"no reset", "", "", 0},
		{"negative reset", "-3", "", 0},
		{"unreadable reset", "NaN", "", 0},
		{"huge reset bounded", "1e300", "", 24 * time.Hour},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := searchFailure(t, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("x-ratelimit-limit", "25")
					w.Header().Set("x-ratelimit-remaining", "0")
					if c.reset != "" {
						w.Header().Set("x-ratelimit-reset-after", c.reset)
					}
					if c.retryAfter != "" {
						w.Header().Set("Retry-After", c.retryAfter)
					}
					w.WriteHeader(http.StatusTooManyRequests)
				})
				if f.Kind != provider.RateLimited || f.RetryAfter != c.want {
					t.Errorf("429: %v for %v, want RateLimited for %v", f, f.RetryAfter, c.want)
				}
			})
		})
	}
}

// TestJimakuCode7IsAuth: a 200 whose body is Jimaku's ApiError is a
// refusal, code 7 (the code its 401 carries, testdata/search-401.json) Auth
// and any other Permanent, never an empty answer.
func TestJimakuCode7IsAuth(t *testing.T) {
	for _, c := range []struct {
		name, body string
		want       provider.FailureKind
	}{
		{"code 7", `{"error":"unauthorized","code":7}`, provider.Auth},
		{"code 7 spaced", " \n{\"code\": 7, \"error\": \"unauthorized\"}", provider.Auth},
		{"another code", `{"error":"something broke","code":3}`, provider.Permanent},
		{"malformed error", `{"code":"seven"}`, provider.Permanent},
		{"no list", `"busy"`, provider.Permanent},
	} {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := searchFailure(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(c.body)) })
				if f.Kind != c.want {
					t.Errorf("200 %s: %v, want %v", c.body, f, c.want)
				}
			})
		})
	}
	t.Run("recorded 401", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			b, err := os.ReadFile("testdata/search-401.json")
			if err != nil {
				t.Fatal(err)
			}
			f := searchFailure(t, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write(b)
			})
			if f.Kind != provider.Auth {
				t.Errorf("401: %v, want Auth", f)
			}
		})
	})
}

// TestJimakuByTMDB pins the lookup over the recorded answers: a series' or
// a film's TMDB id among Jimaku's other entries, then its anime, then the
// files of each entry found.
func TestJimakuByTMDB(t *testing.T) {
	cases := []struct {
		routes map[string]string
		name   string
		files  string
		want   []string
		q      provider.Query
	}{
		{
			name: "series", q: episode(6484), files: "files-4464.json",
			routes: map[string]string{search("tv:6484", "false"): "search-tv-6484.json", "/api/entries/4464/files": "files-4464.json"},
			want:   []string{search("tv:6484", "false"), "/api/entries/4464/files"},
		},
		{
			name: "film", q: movie(403579), files: "files-7742.json",
			routes: map[string]string{search("movie:403579", "false"): "search-movie-403579.json", "/api/entries/7742/files": "files-7742.json"},
			want:   []string{search("movie:403579", "false"), "/api/entries/7742/files"},
		},
		{
			name: "anime", q: episode(61222), files: "files-5992.json",
			routes: map[string]string{
				search("tv:61222", "false"): "search-tv-61222-nonanime.json",
				search("tv:61222", "true"):  "search-tv-61222-anime.json",
				"/api/entries/5992/files":   "files-5992.json",
			},
			want: []string{search("tv:61222", "false"), search("tv:61222", "true"), "/api/entries/5992/files"},
		},
		{
			name: "listed nowhere", q: episode(209867),
			routes: map[string]string{
				search("tv:209867", "false"): "search-tv-61222-nonanime.json",
				search("tv:209867", "true"):  "search-tv-61222-nonanime.json",
			},
			want: []string{search("tv:209867", "false"), search("tv:209867", "true")},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s := &site{routes: c.routes}
				ctx := providertest.Serve(t, t.Context(), s, host)
				got, err := adapter(t).Search(ctx, c.q)
				if err != nil {
					t.Fatalf("search: %v", err)
				}
				if uris := s.uris(); !slices.Equal(uris, c.want) {
					t.Errorf("asked %q, want %q", uris, c.want)
				}
				for _, r := range s.seen() {
					if r.Header.Get("Authorization") != key || !strings.HasPrefix(r.UserAgent(), "subflux/") {
						t.Errorf("%s: Authorization %q, User-Agent %q, want the key and subflux's agent", r.URL, r.Header.Get("Authorization"), r.UserAgent())
					}
				}
				var want []struct{ URL, Name string }
				if c.files != "" {
					want = recorded(t, c.files)
				}
				if len(got) != len(want) {
					t.Fatalf("%d candidates, want the %d recorded files", len(got), len(want))
				}
				for i, f := range want {
					g := &got[i]
					if g.Ref.Token() != f.URL || g.Release != f.Name || g.Lang != ja || g.MatchedBy != rank.ID || g.Covers != nil || g.Title != "" {
						t.Errorf("candidate %d: %+v, want %s (%q) in ja, matched by id with no Covers", i, g, f.URL, f.Name)
					}
				}
			})
		})
	}
}

// TestJimakuNoTMDBNoRequest: a query with no TMDB id, or of no episode or
// film, is skipped unsent.
func TestJimakuNoTMDBNoRequest(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &site{}
		ctx := providertest.Serve(t, t.Context(), s, host)
		imdbOnly := episode(0)
		imdbOnly.External.IMDb = 903747
		noKind := movie(6484)
		noKind.Kind = 0
		for _, q := range []provider.Query{imdbOnly, noKind} {
			if _, err := adapter(t).Search(ctx, q); !isSkipped(err) {
				t.Errorf("query %+v: %v, want a Skipped", q.External, err)
			}
		}
		if n := len(s.seen()); n != 0 {
			t.Errorf("%d requests sent, want none", n)
		}
	})
}

func isSkipped(err error) bool {
	_, ok := errors.AsType[*provider.Skipped](err)
	return ok
}

// packs is a search finding One Piece's entry, whose recorded files hold
// zip, rar and 7z packs and image subtitles beside subtitle files.
var packs = map[string]string{
	search("tv:37854", "false"): "search-tv-61222-nonanime.json",
	search("tv:37854", "true"):  "search-1563.json",
	"/api/entries/1563/files":   "files-1563-subset.json",
	"/entry/1563/download/[HorribleSubs] One Piece 442-443 Retimed subs.zip": "pack-442-443.zip",
	"/entry/1563/download/[Netflix] One Piece 207-219.rar":                   "pack-207-219.rar",
}

// TestJimakuPacks: subtitle files and zip and rar packs are offered, 7z
// packs and image subtitles are not, and a pack is fetched as Jimaku serves
// it, so Extract hands its members to 8.11's choice, which takes the
// episode's.
func TestJimakuPacks(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), &site{routes: packs}, host)
		a := adapter(t)
		got, err := a.Search(ctx, episode(37854))
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		var names []string
		for i := range got {
			names = append(names, got[i].Release)
		}
		want := []string{
			"Dream 9 Toriko (Episode 099) And One Piece (Episode 590) And Dragon Ball Z Special Collaboration!! (2013.04.07).srt",
			"One Piece - Dr. チョッパーの冒険カルテ ～親友達の人生航路～ [2025-06-22][Recap][TV].srt",
			"[Anime Land] One Piece Special Episode 4 _Legendary record! Red-haired Shanks!_ (WEBRip 1080p Hi10P AAC) RAW [96472A7F].ass",
			"[HorribleSubs] One Piece 442-443 Retimed subs.zip",
			"[Netflix] One Piece 207-219.rar",
		}
		if !slices.Equal(names, want) {
			t.Fatalf("offered %q, want %q", names, want)
		}
		zip := fetch(t, ctx, a, got, "pack-442-443.zip")
		var listed []string
		if _, err := archive.Extract(zip, func(names []string) (int, error) {
			listed = names
			return 0, nil
		}); err != nil {
			t.Fatalf("extract the zip: %v", err)
		}
		if want := []string{"[HorribleSubs] One Piece - 443 [720p].ass", "[HorribleSubs] One Piece - 442 [720p].ass"}; !slices.Equal(listed, want) {
			t.Errorf("the zip offers %q to choose, want %q", listed, want)
		}
		rar := fetch(t, ctx, a, got, "pack-207-219.rar")
		var chosen string
		_, err = archive.Extract(rar, func(names []string) (int, error) {
			ch := episodematch.Select(&episodematch.Input{Series: season(9, 207, 13), Members: members(names), Item: 210 - 207, Variant: language.Standard})
			if ch.Member < 0 {
				return 0, fmt.Errorf("no member: refusal %d", ch.Refusal)
			}
			chosen = names[ch.Member]
			return ch.Member, nil
		})
		if want := "ONE PIECE-S9_E210-Episode 210_ja.srt"; err != nil || chosen != want {
			t.Errorf("the rar for S09E210: took %q (%v), want %q", chosen, err, want)
		}
	})
}

// fetch fetches the candidate of got whose Ref names pack's recorded file
// and checks Jimaku's bytes come back as served.
func fetch(t *testing.T, ctx context.Context, a provider.Adapter, got []provider.Candidate, pack string) []byte {
	t.Helper()
	ext := path.Ext(pack)
	i := slices.IndexFunc(got, func(c provider.Candidate) bool { return path.Ext(c.Ref.Token()) == ext })
	if i < 0 {
		t.Fatalf("no %s candidate", ext)
	}
	b, err := a.Fetch(ctx, got[i].Ref)
	if err != nil {
		t.Fatalf("fetch %s: %v", pack, err)
	}
	raw, err := os.ReadFile("testdata/" + pack)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, raw) {
		t.Fatalf("fetch %s: %d bytes, want the %d recorded", pack, len(b), len(raw))
	}
	return b
}

// season is a series whose one season, numbered season, holds the n
// episodes numbered from first, each its own absolute number.
func season(season, first, n int) episodematch.Series {
	items := make([]mediasource.Item, n)
	for i := range items {
		items[i].Numbering = media.Numbering{Scheme: media.TVDB, Season: season, Episode: first + i}
		items[i].SeriesNumbering = items[i].Numbering
		items[i].Absolute = first + i
	}
	return episodematch.Series{Items: items}
}

func members(names []string) []episodematch.Member {
	out := make([]episodematch.Member, len(names))
	for i, n := range names {
		out[i] = episodematch.Member{Name: n}
	}
	return out
}

// TestJimakuHosts: a file whose URL is not https on jimaku.cc under /entry/
// is not offered, a Ref naming another host is refused before dialing, and
// a download carries no key.
func TestJimakuHosts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		away := &site{}
		ctx := providertest.Serve(t, t.Context(), away, foreign)
		home := &site{routes: map[string]string{"/entry/4464/download/ok.srt": "dl-4464-ep01.srt"}}
		ctx = providertest.Serve(t, ctx, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/entries/search":
				_, _ = w.Write([]byte(`[{"id":4464}]`))
			case "/api/entries/4464/files":
				_, _ = w.Write([]byte(`[
					{"url":"https://` + foreign + `/entry/4464/download/away.srt","name":"away.srt"},
					{"url":"http://jimaku.cc/entry/4464/download/plain.srt","name":"plain.srt"},
					{"url":"https://user@jimaku.cc/entry/4464/download/user.srt","name":"user.srt"},
					{"url":"https://jimaku.cc:8443/entry/4464/download/port.srt","name":"port.srt"},
					{"url":"https://jimaku.cc/api/entries/4464/files","name":"api.srt"},
					{"url":"https://jimaku.cc/entry/4464/download/ok.srt","name":"ok.srt"}]`))
			default:
				home.ServeHTTP(w, r)
			}
		}), host)
		a := adapter(t)
		got, err := a.Search(ctx, episode(6484))
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if len(got) != 1 || got[0].Ref.Token() != "https://jimaku.cc/entry/4464/download/ok.srt" {
			t.Fatalf("candidates %+v, want only ok.srt", got)
		}
		for _, tok := range []string{"https://" + foreign + "/entry/1/download/x.srt", "//" + foreign + "/entry/1/x", "https://jimaku.cc.foreign.example.test/entry/1/x", "/entry/1/download/x.srt"} {
			_, err := a.Fetch(ctx, provider.NewRef(tok))
			if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent {
				t.Errorf("fetch %q: %v, want Permanent", tok, err)
			}
		}
		if n := len(away.seen()); n != 0 {
			t.Errorf("the foreign host answered %d requests", n)
		}
		if _, err := a.Fetch(ctx, got[0].Ref); err != nil {
			t.Fatalf("fetch: %v", err)
		}
		reqs := home.seen()
		if len(reqs) != 1 || reqs[0].Header.Get("Authorization") != "" {
			t.Errorf("download requests %d, the first with Authorization %q, want one without the key", len(reqs), reqs[0].Header.Get("Authorization"))
		}
	})
}

// TestJimakuTester: Test is one keyed search for a fixed TMDB id, and a
// refused key is Auth.
func TestJimakuTester(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &site{routes: map[string]string{search("tv:6484", "false"): "search-tv-6484.json"}}
		ctx := providertest.Serve(t, t.Context(), s, host)
		tester, ok := adapter(t).(provider.Tester)
		if !ok {
			t.Fatal("no Tester")
		}
		if err := tester.Test(ctx); err != nil {
			t.Fatalf("test: %v", err)
		}
		if uris := s.uris(); !slices.Equal(uris, []string{search("tv:6484", "false")}) || s.seen()[0].Header.Get("Authorization") != key {
			t.Errorf("asked %q, want one keyed search for tv:6484", uris)
		}
		refused := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized","code":7}`))
		}), host)
		err := adapter(t).(provider.Tester).Test(refused)
		if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Auth {
			t.Errorf("refused key: %v, want Auth", err)
		}
	})
}

// TestProviderMaps: Jimaku lists Japanese only, so its map holds ja alone.
func TestProviderMaps(t *testing.T) {
	m := jimaku.NewEntry().Languages
	if tags := m.Tags(); !slices.Equal(tags, []language.Tag{ja}) {
		t.Errorf("tags %v, want [ja]", tags)
	}
}

// TestJimakuEntryBound: an answer listing more entries than a TMDB id names
// reads the files of the first two only.
func TestJimakuEntryBound(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &site{routes: map[string]string{"/api/entries/4464/files": "files-7742.json", "/api/entries/4465/files": "files-7742.json"}}
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/entries/search" {
				_, _ = w.Write([]byte(`[{"id":4464},{"id":4465},{"id":4466}]`))
				return
			}
			s.ServeHTTP(w, r)
		}), host)
		got, err := adapter(t).Search(ctx, episode(6484))
		if err != nil {
			t.Fatalf("search: %v", err)
		}
		if uris := s.uris(); len(got) != 2 || !slices.Equal(uris, []string{"/api/entries/4464/files", "/api/entries/4465/files"}) {
			t.Errorf("read %q for %d candidates, want the first two entries' files", uris, len(got))
		}
	})
}

// TestJimakuLaterListFails: a file list failing after another was read
// returns the candidates read with its failure (design 5.1).
func TestJimakuLaterListFails(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &site{routes: map[string]string{"/api/entries/4464/files": "files-7742.json"}}
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/api/entries/search":
				_, _ = w.Write([]byte(`[{"id":4464},{"id":4465}]`))
			case "/api/entries/4465/files":
				w.WriteHeader(http.StatusBadRequest)
			default:
				s.ServeHTTP(w, r)
			}
		}), host)
		got, err := adapter(t).Search(ctx, episode(6484))
		if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent || len(got) != 1 {
			t.Errorf("%d candidates and %v, want the first list's one with the second's Permanent failure", len(got), err)
		}
	})
}

// TestJimakuEmptyFile: a download answering no bytes is Permanent.
func TestJimakuEmptyFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), host)
		_, err := adapter(t).Fetch(ctx, provider.NewRef("https://jimaku.cc/entry/4464/download/x.srt"))
		if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Permanent {
			t.Errorf("empty download: %v, want Permanent", err)
		}
	})
}
