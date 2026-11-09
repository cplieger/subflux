package radarr_test

import (
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr/arrtest"
)

func tag(t *testing.T, raw string) language.Tag {
	t.Helper()
	l, ok := language.Parse(raw)
	if !ok {
		t.Fatalf("language.Parse(%q) failed", raw)
	}
	return l
}

func TestItemsMovieListFailure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := arrtest.Load(t, dir)
		srv.Handle("/api/v3/movie", func(w http.ResponseWriter, _ *http.Request) { srv.Write(w, http.StatusServiceUnavailable, nil) })
		got, _, err := walkItems(t, first(adapter(t, srv, "")))
		if causeOf(err) != mediasource.Unreachable || len(got) != 0 {
			t.Errorf("Items over a failed movie list = %v, yielded %d; want unreachable and nothing", err, len(got))
		}
	})
}

func TestItemsOneRootPerMovie(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got, order, err := walkItems(t, first(adapter(t, arrtest.Load(t, dir), "")))
		if err != nil || !slices.Equal(order, []int{totoro, fastForever, scrapper}) {
			t.Fatalf("Items = %v %v, want the three recorded movies in list order", order, err)
		}
		m := got[scrapper]
		want := media.ExternalIDs{TMDB: 935906, IMDb: 8649252}
		if m.root.Native != ref(scrapper) || m.root.Title != "Scrapper" || m.root.External != want {
			t.Errorf("Scrapper root = %+v, want movie %d titled Scrapper with %+v", m.root, scrapper, want)
		}
		if len(m.items) != 1 {
			t.Fatalf("Scrapper items = %d, want one", len(m.items))
		}
		it := m.items[0]
		if it.Native != m.root.Native.ID || it.External != want || !slices.Equal(it.Tags, []mediasource.TagRef{"5"}) {
			t.Errorf("Scrapper item = %+v, want the movie id, its ids and tag 5", it)
		}
		if it.VideoPath != "/media/movies/Scrapper (2023) {imdb-tt8649252}/Scrapper (2023) {imdb-tt8649252} - [Bluray-1080p][EAC3 5.1][x264]-SbR.mkv" || it.SceneName != "Scrapper.2023.1080p.BluRay.DD+5.1.x264-SbR" {
			t.Errorf("Scrapper file = %q %q, want the recorded path and scene name", it.VideoPath, it.SceneName)
		}
		if ff := got[fastForever].items[0]; ff.VideoPath != "" || ff.AudioLangs != nil {
			t.Errorf("fileless Fast Forever item = %+v, want no video", ff)
		}
		if l := got[totoro].items[0].OriginalLang; l != tag(t, "ja") {
			t.Errorf("Totoro OriginalLang = %v, want Japanese", l)
		}
	})
}

func TestOrderZeroForMovies(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got, _, err := walkItems(t, first(adapter(t, arrtest.Load(t, dir), "")))
		if err != nil {
			t.Fatal(err)
		}
		for id, m := range got {
			it := m.items[0]
			if m.root.Order != (mediasource.Order{}) || it.SeriesNumbering != (media.Numbering{}) || it.Numbering != (media.Numbering{}) || it.Absolute != 0 {
				t.Errorf("movie %d: Order %+v, numberings %+v %+v, absolute %d; want all zero", id, m.root.Order, it.SeriesNumbering, it.Numbering, it.Absolute)
			}
		}
	})
}

func TestRootCarriesYear(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got, _, err := walkItems(t, first(adapter(t, arrtest.Load(t, dir), "")))
		if err != nil || got[totoro].root.Year != 1988 {
			t.Errorf("Totoro Year = %d %v, want the recorded 1988", got[totoro].root.Year, err)
		}
		for name, edit := range map[string]func(map[string]any){
			"year 0":      func(m map[string]any) { m["year"] = 0 },
			"year absent": func(m map[string]any) { delete(m, "year") },
		} {
			srv := arrtest.Load(t, dir)
			rewrite(t, srv, "/api/v3/movie/"+itoa(totoro), "movie-"+itoa(totoro)+".json", edit)
			r, _, ok, err := first(adapter(t, srv, "")).Root(t.Context(), ref(totoro))
			if err != nil || !ok || r.Year != 0 {
				t.Errorf("%s: Root = Year %d, %v, %v; want 0", name, r.Year, ok, err)
			}
		}
	})
}

func TestRootAgreesWithItems(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		a, _ := adapter(t, arrtest.Load(t, dir), "")
		got, _, err := walkItems(t, a)
		if err != nil {
			t.Fatal(err)
		}
		for _, id := range []int{totoro, fastForever, scrapper} {
			r, items, ok, err := a.Root(t.Context(), ref(id))
			if err != nil || !ok || r.Title != got[id].root.Title || r.Year != got[id].root.Year || len(items) != 1 || items[0].VideoPath != got[id].items[0].VideoPath {
				t.Errorf("Root(%d) = %+v %+v %v %v, want what Items yielded", id, r, items, ok, err)
			}
		}
	})
}

func TestPingWrongApplication(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		err := first(adapter(t, arrtest.Load(t, "../sonarr/testdata/v5-api-v5"), "")).Ping(t.Context())
		if causeOf(err) != mediasource.Protocol || !strings.Contains(err.Error(), "Radarr") || !strings.Contains(err.Error(), "Sonarr") {
			t.Errorf("Ping of a Sonarr = %v, want protocol naming Sonarr and Radarr", err)
		}
	})
}

func TestPingIsInfo(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		srv := arrtest.Load(t, dir)
		a, l := adapter(t, srv, "")
		for range 2 {
			if err := a.Ping(t.Context()); err != nil {
				t.Fatalf("Ping = %v", err)
			}
		}
		if n := strings.Count(l.String(), "source server version"); n != 1 || !strings.Contains(l.String(), srv.Version()) {
			t.Errorf("two Pings logged %d version lines in %q, want one naming %s", n, l.String(), srv.Version())
		}
	})
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
	const command = "/api/v3/command"
	t.Run("accepted", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := arrtest.Load(t, dir)
			srv.Handle(command, func(w http.ResponseWriter, _ *http.Request) { srv.Write(w, http.StatusCreated, []byte(`{"id":7}`)) })
			if err := first(adapter(t, srv, "")).Rescan(t.Context(), ref(totoro)); err != nil || posts(srv) != 1 {
				t.Errorf("Rescan = %v after %d POSTs, want nil after one", err, posts(srv))
			}
		})
	})
	t.Run("an unattributable 2xx is unreachable", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := arrtest.Load(t, dir)
			srv.Handle(command, func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusCreated)
				_, _ = io.WriteString(w, `{"id":7}`)
			})
			err := first(adapter(t, srv, "")).Rescan(t.Context(), ref(totoro))
			if causeOf(err) != mediasource.Unreachable || !errors.Is(err, mediasource.ErrRescanOutcomeUnknown) || posts(srv) != 1 {
				t.Errorf("Rescan = %v after %d POSTs, want unreachable, outcome unknown, after one", err, posts(srv))
			}
		})
	})
	t.Run("a redirect is protocol", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			srv := arrtest.Load(t, dir)
			srv.Handle(command, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("X-Application-Version", srv.Version())
				w.Header().Set("Location", "http://elsewhere.test/api/v3/command")
				w.WriteHeader(http.StatusFound)
			})
			if err := first(adapter(t, srv, "")).Rescan(t.Context(), ref(totoro)); causeOf(err) != mediasource.Protocol || posts(srv) != 1 {
				t.Errorf("Rescan = %v after %d POSTs, want protocol after one", err, posts(srv))
			}
		})
	})
}

func TestPosterFetchesMediaCoverURL(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		jpeg := []byte("\xff\xd8\xff\xe0 poster")
		srv := arrtest.Load(t, dir)
		cover := "/api/v3/mediacover/" + itoa(totoro) + "/poster.jpg"
		srv.Handle(cover, func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("X-Application-Version", srv.Version())
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(jpeg)
		})
		body, ct, found, err := first(adapter(t, srv, "")).Poster(t.Context(), ref(totoro))
		if err != nil || !found {
			t.Fatalf("Poster = %v %v", found, err)
		}
		got, _ := io.ReadAll(body)
		_ = body.Close()
		if string(got) != string(jpeg) || ct != "image/jpeg" {
			t.Errorf("Poster = %q %q, want the cover's bytes as image/jpeg", got, ct)
		}
		reqs := srv.Requests()
		if last := reqs[len(reqs)-1]; last.Target != cover || last.Key != arrtest.Key {
			t.Errorf("poster request %+v, want %s with the key", last, cover)
		}
	})
}

func TestAudioTracksFromList(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		got, _, err := walkItems(t, first(adapter(t, arrtest.Load(t, dir), "")))
		if err != nil {
			t.Fatal(err)
		}
		want := []language.Tag{tag(t, "ja"), tag(t, "en"), tag(t, "fr"), tag(t, "de"), tag(t, "it"), tag(t, "ko"), tag(t, "zh")}
		if l := got[totoro].items[0].AudioLangs; !slices.Equal(l, want) {
			t.Errorf("Totoro jpn/eng/eng/fre/ger/ita/kor/chi/chi = %v, want %v", l, want)
		}
		for name, list := range map[string]string{"codes": "eng/jpn/qqq", "names": "english/japanese/klingonish"} {
			srv := arrtest.Load(t, dir)
			rewrite(t, srv, "/api/v3/movie", "movie.json", func(m map[string]any) {
				if f, ok := m["movieFile"].(map[string]any); ok {
					f["mediaInfo"].(map[string]any)["audioLanguages"] = list
				}
			})
			got, _, err := walkItems(t, first(adapter(t, srv, "")))
			if l := got[scrapper].items[0].AudioLangs; err != nil || !slices.Equal(l, []language.Tag{tag(t, "en"), tag(t, "ja")}) {
				t.Errorf("%s %q: AudioLangs = %v %v, want English then Japanese", name, list, l, err)
			}
		}
	})
}

func TestRootLink(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		for publicURL, want := range map[string]string{
			"":                       base + "/movie/8392",
			"https://movies.example": "https://movies.example/movie/8392",
		} {
			r, _, ok, err := first(adapter(t, arrtest.Load(t, dir), publicURL)).Root(t.Context(), ref(totoro))
			if err != nil || !ok || r.Link != want {
				t.Errorf("public_url %q: Root Link = %q %v %v, want %q", publicURL, r.Link, ok, err, want)
			}
		}
	})
}

func TestRoot404False(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		if _, _, ok, err := first(adapter(t, arrtest.Load(t, dir), "")).Root(t.Context(), ref(999999)); ok || err != nil {
			t.Errorf("Root of a movie the server answers 404 = %v %v, want false and no error", ok, err)
		}
	})
}
