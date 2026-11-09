package sonarr_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/arrapi/v3"
	arrsonarr "github.com/cplieger/arrapi/v3/sonarr"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
)

func itoa(n int) string { return strconv.Itoa(n) }

func atoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatalf("native id %q is not decimal", s)
	}
	return n
}

func ref(id int) mediasource.NativeRef {
	return mediasource.NativeRef{ID: mediasource.NativeID(itoa(id)), Kind: media.Episode}
}

func tag(t *testing.T, raw string) language.Tag {
	t.Helper()
	l, ok := language.Parse(raw)
	if !ok {
		t.Fatalf("language.Parse(%q) failed", raw)
	}
	return l
}

// episodesOf answers each series' episode list through h, the recorded
// answer for every other series.
func episodesOf(srv *arrtest.Server, path string, series int, h http.HandlerFunc) {
	srv.Handle(path, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("seriesId") == itoa(series) {
			h(w, r)
			return
		}
		srv.Answer(w, r)
	})
}

func TestItemsSeriesListFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := arrtest.Load(t, v5Dir)
		srv.Handle("/api/v5/series", func(w http.ResponseWriter, _ *http.Request) { srv.Write(w, http.StatusServiceUnavailable, nil) })
		a, _ := adapter(t, srv, "")
		w := walkItems(t, a)
		if causeOf(w.err) != mediasource.Unreachable || len(w.roots) != 0 || len(w.unread) != 0 {
			t.Errorf("Items over a failed series list = %v, yielded %v, unread %v; want unreachable and nothing", w.err, w.ids(), w.unread)
		}
	})
}

func TestSyncPassReadsThroughPin(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := arrtest.Load(t, v5Dir)
		episodesOf(srv, "/api/v5/episode", firefly, func(w http.ResponseWriter, r *http.Request) {
			srv.SetVersion("5.0.0.2999")
			srv.Answer(w, r)
		})
		a, _ := adapter(t, srv, "")
		w := walkItems(t, a)
		if causeOf(w.err) != mediasource.Unreachable || !errors.As(w.err, new(*arrapi.ServerChangedError)) {
			t.Errorf("Items across a server swap = %v, want unreachable through *arrapi.ServerChangedError", w.err)
		}
		if got := w.ids(); !slices.Equal(got, []string{itoa(shoah)}) || len(w.unread) != 0 {
			t.Errorf("Items yielded %v, unread %v; want only the series read before the swap", got, w.unread)
		}
	})
}

func TestEpisodeReadFailureReportsUnread(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := arrtest.Load(t, v5Dir)
		episodesOf(srv, "/api/v5/episode", firefly, func(w http.ResponseWriter, _ *http.Request) { srv.Write(w, http.StatusInternalServerError, nil) })
		a, _ := adapter(t, srv, "")
		w := walkItems(t, a)
		if w.err != nil {
			t.Fatalf("Items = %v, want the walk to finish", w.err)
		}
		if got := w.ids(); !slices.Equal(got, []string{itoa(shoah), itoa(monogatari)}) {
			t.Errorf("Items yielded %v, want every other series", got)
		}
		if len(w.unread) != 1 || w.unread[firefly] != mediasource.Unreachable {
			t.Errorf("unread = %v, want only Firefly, unreachable", w.unread)
		}
	})
}

func TestRootCarriesYear(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, c := range []struct {
			dir string
			id  int
		}{{v5Dir, firefly}, {v4Dir, fireflyV4}} {
			a, _ := adapter(t, arrtest.Load(t, c.dir), "")
			if got := walkItems(t, a).root(t, c.id).Year; got != 2002 {
				t.Errorf("%s: Firefly's Year = %d, want the recorded 2002", c.dir, got)
			}
		}
		for name, edit := range map[string]func(map[string]any){
			"year 0":      func(s map[string]any) { s["year"] = 0 },
			"year absent": func(s map[string]any) { delete(s, "year") },
		} {
			srv := arrtest.Load(t, v5Dir)
			rewrite(t, srv, "/api/v5/series/"+itoa(firefly), "series-"+itoa(firefly)+".json", edit)
			a, _ := adapter(t, srv, "")
			r, _, ok, err := a.Root(t.Context(), ref(firefly))
			if err != nil || !ok || r.Year != 0 {
				t.Errorf("%s: Root = Year %d, %v, %v; want 0", name, r.Year, ok, err)
			}
		}
	})
}

func TestAdapterReportsSourceFacts(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		v5 := walkItems(t, first(adapter(t, arrtest.Load(t, v5Dir), "")))
		if v5.err != nil {
			t.Fatal(v5.err)
		}
		serenity := item(t, v5.items[firefly], 1, 1)
		want := mediasource.Item{Title: "Serenity", AirDate: "2002-12-20", SeriesNumbering: media.Numbering{Scheme: media.TVDB, Season: 1, Episode: 1}}
		if serenity.Title != want.Title || serenity.AirDate != want.AirDate || serenity.SeriesNumbering != want.SeriesNumbering {
			t.Errorf("DVD S01E01 = %q %q %+v, want Sonarr's %q %q %+v", serenity.Title, serenity.AirDate, serenity.SeriesNumbering, want.Title, want.AirDate, want.SeriesNumbering)
		}
		if serenity.Absolute != 0 || serenity.Numbering != (media.Numbering{}) {
			t.Errorf("DVD S01E01 Absolute %d, Numbering %+v; want Sonarr's 0 and no aired place", serenity.Absolute, serenity.Numbering)
		}
		if got := v5.root(t, firefly).Language; got != tag(t, "en") {
			t.Errorf("Sonarr v5 Firefly Language = %v, want its English metadata language", got)
		}

		v4 := walkItems(t, first(adapter(t, arrtest.Load(t, v4Dir), "")))
		if v4.err != nil {
			t.Fatal(v4.err)
		}
		if got := item(t, v4.items[monogatariV4], 1, 1); got.Absolute != 1 || got.Numbering != (media.Numbering{}) {
			t.Errorf("Sonarr v4 Monogatari S01E01 Absolute %d, Numbering %+v; want Sonarr's 1 and no aired place", got.Absolute, got.Numbering)
		}
		if got := v4.root(t, monogatariV4).Language; got != (language.Tag{}) {
			t.Errorf("Sonarr v4 (API v3) Language = %v, want zero: that API sends none", got)
		}

		srv := arrtest.Load(t, v5Dir)
		rewrite(t, srv, "/api/v5/series", "series.json", frenchMetadata)
		if got := walkItems(t, first(adapter(t, srv, ""))).root(t, monogatari).Language; got != tag(t, "fr") {
			t.Errorf("French metadata Language = %v, want French", got)
		}
	})
}

func first[A, B any](a A, _ B) A { return a }

func TestTitleSetFromTranslations(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		alternates := []string{"Bakemonogatari", "Nisemonogatari", "Monogatari Series Second Season", "Owarimonogatari", "Owarimonogatari S2", "Owarimonogatari Second Season", "Monogatari Series - Off and Monster Season", "Monogatari Series Off & Monster Season"}

		srv := arrtest.Load(t, v5Dir)
		rewrite(t, srv, "/api/v5/series", "series.json", frenchMetadata)
		fr := walkItems(t, first(adapter(t, srv, ""))).root(t, monogatari)
		if want := append([]string{"Monogatari", "物語シリーズ"}, alternates...); fr.Title != "Les Monogatari" || !slices.Equal(fr.AltTitles, want) {
			t.Errorf("French metadata titles = %q %q, want %q %q", fr.Title, fr.AltTitles, "Les Monogatari", want)
		}

		v4 := walkItems(t, first(adapter(t, arrtest.Load(t, v4Dir), ""))).root(t, monogatariV4)
		if v4.Title != "Monogatari" || !slices.Equal(v4.AltTitles, alternates) {
			t.Errorf("Sonarr v4 (API v3) titles = %q %q, want the title and alternates", v4.Title, v4.AltTitles)
		}
	})
}

func TestPingWrongApplication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := adapter(t, arrtest.Load(t, "../radarr/testdata/api-v3"), "")
		err := a.Ping(t.Context())
		if causeOf(err) != mediasource.Protocol || !strings.Contains(err.Error(), "Radarr") || !strings.Contains(err.Error(), "Sonarr") {
			t.Errorf("Ping of a Radarr = %v, want protocol naming Radarr and Sonarr", err)
		}
	})
}

func TestPingIsInfo(t *testing.T) {
	for _, dir := range []string{v4Dir, v5Dir} {
		t.Run(dir, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := arrtest.Load(t, dir)
				a, l := adapter(t, srv, "")
				for range 2 {
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					err := a.Ping(ctx)
					cancel()
					if err != nil {
						t.Fatalf("Ping = %v", err)
					}
				}
				if n := strings.Count(l.String(), "source server version"); n != 1 || !strings.Contains(l.String(), srv.Version()) {
					t.Errorf("two Pings logged %d version lines in %q, want one naming %s", n, l.String(), srv.Version())
				}
			})
		})
	}
}

func posts(srv *arrtest.Server) int {
	n := 0
	for _, r := range srv.Requests() {
		if r.Method == http.MethodPost {
			n++
		}
	}
	return n
}

func TestRescanSentAtMostOnce(t *testing.T) {
	const command = "/api/v5/command"
	t.Run("accepted", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := arrtest.Load(t, v5Dir)
			srv.Handle(command, func(w http.ResponseWriter, _ *http.Request) { srv.Write(w, http.StatusCreated, []byte(`{"id":7}`)) })
			a, _ := adapter(t, srv, "")
			if err := a.Rescan(t.Context(), ref(firefly)); err != nil || posts(srv) != 1 {
				t.Errorf("Rescan = %v after %d POSTs, want nil after one", err, posts(srv))
			}
		})
	})
	t.Run("a detection made stale sends nothing", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := arrtest.Load(t, v5Dir)
			a, _ := adapter(t, srv, "")
			if err := a.Ping(t.Context()); err != nil {
				t.Fatal(err)
			}
			atStatus, release := make(chan struct{}), make(chan struct{})
			var held atomic.Bool
			srv.Handle("/api/v5/system/status", func(w http.ResponseWriter, r *http.Request) {
				if held.CompareAndSwap(false, true) {
					close(atStatus)
					<-release
				}
				srv.Answer(w, r)
			})
			srv.Handle("/api/v5/tag", func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Application-Version", "5.0.0.2999")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(srv.Body("tag.json"))
			})
			done := make(chan error, 1)
			go func() { done <- a.Rescan(t.Context(), ref(firefly)) }()
			<-atStatus
			if _, err := a.Tags(t.Context()); err == nil {
				t.Error("Tags answered by another version succeeded")
			}
			close(release)
			if err := <-done; causeOf(err) != mediasource.Unreachable || posts(srv) != 0 {
				t.Errorf("Rescan = %v after %d POSTs, want unreachable and none sent", err, posts(srv))
			}
		})
	})
	t.Run("a redirect is protocol", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := arrtest.Load(t, v5Dir)
			srv.Handle(command, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Application-Version", srv.Version())
				w.Header().Set("Location", "http://elsewhere.test/api/v5/command")
				w.WriteHeader(http.StatusFound)
			})
			a, _ := adapter(t, srv, "")
			if err := a.Rescan(t.Context(), ref(firefly)); causeOf(err) != mediasource.Protocol || posts(srv) != 1 {
				t.Errorf("Rescan = %v after %d POSTs, want protocol after one", err, posts(srv))
			}
		})
	})
	t.Run("an unattributable 2xx is unreachable", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := arrtest.Load(t, v5Dir)
			srv.Handle(command, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"id":7}`)
			})
			a, _ := adapter(t, srv, "")
			err := a.Rescan(t.Context(), ref(firefly))
			if causeOf(err) != mediasource.Unreachable || !errors.Is(err, mediasource.ErrRescanOutcomeUnknown) || posts(srv) != 1 {
				t.Errorf("Rescan = %v after %d POSTs, want unreachable, outcome unknown, after one", err, posts(srv))
			}
		})
	})
}

func TestPosterFetchesMediaCoverURL(t *testing.T) {
	jpeg := []byte("\xff\xd8\xff\xe0 poster")
	for _, c := range []struct {
		dir, prefix string
		id          int
	}{{v4Dir, "/api/v3", fireflyV4}, {v5Dir, "/api/v5", firefly}} {
		t.Run(c.dir, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				srv := arrtest.Load(t, c.dir)
				cover := c.prefix + "/mediacover/" + itoa(c.id) + "/poster.jpg"
				srv.Handle(cover, func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("X-Application-Version", srv.Version())
					w.Header().Set("Content-Type", "image/jpeg")
					_, _ = w.Write(jpeg)
				})
				client, err := arrsonarr.New(base, arrtest.Key, arrsonarr.WithHTTPClient(srv.Client()))
				if err != nil {
					t.Fatal(err)
				}
				want, err := client.MediaCoverURL(t.Context(), c.id, "poster.jpg")
				if err != nil || want != base+cover {
					t.Fatalf("MediaCoverURL = %q %v, want %q", want, err, base+cover)
				}
				a, _ := adapter(t, srv, "")
				body, ct, found, err := a.Poster(t.Context(), ref(c.id))
				if err != nil || !found {
					t.Fatalf("Poster = %v %v", found, err)
				}
				got, _ := io.ReadAll(body)
				_ = body.Close()
				if string(got) != string(jpeg) || ct != "image/jpeg" {
					t.Errorf("Poster = %q %q, want the cover's bytes as image/jpeg", got, ct)
				}
				reqs := srv.Requests()
				last := reqs[len(reqs)-1]
				if last.Target != strings.TrimPrefix(want, base) || last.Key != arrtest.Key {
					t.Errorf("poster request %+v, want %s with the key", last, want)
				}
			})
		})
	}
}

func TestHeaderlessAnswerCostsOneDetection(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := arrtest.Load(t, v5Dir)
		var answered atomic.Bool
		episodesOf(srv, "/api/v5/episode", firefly, func(w http.ResponseWriter, r *http.Request) {
			if answered.CompareAndSwap(false, true) {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			srv.Answer(w, r)
		})
		a, _ := adapter(t, srv, "")
		w := walkItems(t, a)
		if w.err != nil || len(w.roots) != 3 || len(w.unread) != 0 {
			t.Fatalf("Items = %v, yielded %v, unread %v; want all three series", w.err, w.ids(), w.unread)
		}
		episodes := func(id int) string {
			return "/api/v5/episode?seriesId=" + itoa(id) + "&includeSubresources=episodeFile"
		}
		want := []string{
			"/api", "/api/v5/system/status", "/api/v5/series",
			episodes(shoah),
			episodes(firefly), "/api", "/api/v5/system/status", episodes(firefly),
			episodes(monogatari),
		}
		var got []string
		for _, r := range srv.Requests() {
			got = append(got, r.Target)
		}
		if !slices.Equal(got, want) {
			t.Errorf("walk requests = %q, want %q", got, want)
		}
	})
}

func TestAudioTracksFromList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		en, ja := tag(t, "en"), tag(t, "ja")
		for _, c := range []struct {
			name, dir, path string
			edit            func(mediaInfo map[string]any)
		}{
			{"Sonarr v4 (API v3) codes", v4Dir, "/api/v3/episode", func(mi map[string]any) { mi["audioLanguages"] = "eng/jpn" }},
			{"Sonarr v4 (API v3) names", v4Dir, "/api/v3/episode", func(mi map[string]any) { mi["audioLanguages"] = "english/japanese/klingonish" }},
			{"Sonarr v5 (API v5) codes", v5Dir, "/api/v5/episode", func(mi map[string]any) {
				mi["audioStreams"] = []any{map[string]any{"language": "eng"}, map[string]any{"language": "jpn"}, map[string]any{"language": "qqq"}}
			}},
		} {
			srv := arrtest.Load(t, c.dir)
			rewrite(t, srv, c.path, "episode-"+itoa(shoah)+".json", func(e map[string]any) {
				if f, ok := e["episodeFile"].(map[string]any); ok {
					c.edit(f["mediaInfo"].(map[string]any))
				}
			})
			w := walkItems(t, first(adapter(t, srv, "")))
			if got := item(t, w.items[shoah], 1, 1).AudioLangs; !slices.Equal(got, []language.Tag{en, ja}) {
				t.Errorf("%s: AudioLangs = %v, want English then Japanese", c.name, got)
			}
		}
	})
}

func TestSeriesTagsOnEveryItem(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := arrtest.Load(t, v5Dir)
		rewrite(t, srv, "/api/v5/series", "series.json", func(s map[string]any) {
			if s["id"] == float64(firefly) {
				s["tags"] = []any{8, 10}
			}
		})
		w := walkItems(t, first(adapter(t, srv, "")))
		want := []mediasource.TagRef{"8", "10"}
		for _, it := range w.items[firefly] {
			if !slices.Equal(it.Tags, want) {
				t.Fatalf("Firefly item %s Tags = %v, want the series' %v", it.Native, it.Tags, want)
			}
		}
		if got := item(t, w.items[shoah], 1, 1).Tags; len(got) != 0 {
			t.Errorf("untagged Shoah item Tags = %v, want none", got)
		}
	})
}

func TestDoubleEpisodeFile(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := walkItems(t, first(adapter(t, arrtest.Load(t, v5Dir), "")))
		e1, e2 := item(t, w.items[shoah], 1, 1), item(t, w.items[shoah], 1, 2)
		if e1.Native == e2.Native || e1.VideoPath == "" || e1.VideoPath != e2.VideoPath {
			t.Errorf("S01E01-E02 = %q %q and %q %q, want two items over one video", e1.Native, e1.VideoPath, e2.Native, e2.VideoPath)
		}
	})
}

func TestFilelessEpisodeIsWanted(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		w := walkItems(t, first(adapter(t, arrtest.Load(t, v5Dir), "")))
		if got := item(t, w.items[shoah], 0, 1); got.VideoPath != "" || got.Title == "" {
			t.Errorf("fileless S00E01 = %+v, want an item with no video", got)
		}
	})
}

func TestRootLink(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for publicURL, want := range map[string]string{
			"":                   base + "/series/firefly",
			"https://tv.example": "https://tv.example/series/firefly",
		} {
			a, _ := adapter(t, arrtest.Load(t, v5Dir), publicURL)
			r, _, ok, err := a.Root(t.Context(), ref(firefly))
			if err != nil || !ok || r.Link != want {
				t.Errorf("public_url %q: Root Link = %q %v %v, want %q", publicURL, r.Link, ok, err, want)
			}
		}
	})
}

func TestRoot404False(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for _, dir := range []string{v4Dir, v5Dir} {
			a, _ := adapter(t, arrtest.Load(t, dir), "")
			if _, _, ok, err := a.Root(t.Context(), ref(999999)); ok || err != nil {
				t.Errorf("%s: Root of a series the server answers 404 = %v %v, want false and no error", dir, ok, err)
			}
		}
	})
}
