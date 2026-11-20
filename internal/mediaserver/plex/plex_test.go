package plex

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/plexapi/v3"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediasource"
)

func TestNewRefusesBadURL(t *testing.T) {
	for name, url := range map[string]string{"scheme": "ftp://plex:32400", "host": "http://"} {
		t.Run(name, func(t *testing.T) {
			if _, err := newPlex(plexConfig("plex", url, testToken), mediasource.Deps{}); err == nil {
				t.Errorf("newPlex(%q) succeeded", url)
			}
		})
	}
}

// TestPingServerThroughKind: the connection test reaches Ping through the
// registry's New, so a refused token answers before any Save.
func TestPingServerThroughKind(t *testing.T) {
	f := library(t)
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	kinds := []mediaserver.AdapterKind{Kind}
	if err := mediaserver.PingServer(t.Context(), kinds, mediasource.Deps{}, plexConfig("plex", srv.URL, testToken)); err != nil {
		t.Errorf("PingServer = %v", err)
	}
	if !slices.Contains(f.log(), "GET /") {
		t.Errorf("requests %v, want one Identity read", f.log())
	}
	err := mediaserver.PingServer(t.Context(), kinds, mediasource.Deps{}, plexConfig("plex", srv.URL, "wrong"))
	if se, ok := errors.AsType[*plexapi.StatusError](err); !ok || se.Code != http.StatusUnauthorized {
		t.Errorf("PingServer with a refused token = %v, want its 401", err)
	}
}

// TestRedirectNeverFollowed: plexapi's own client refuses every redirect,
// so the token never reaches another host.
func TestRedirectNeverFollowed(t *testing.T) {
	var reached atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached.Add(1)
	}))
	t.Cleanup(target.Close)
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(origin.Close)
	a, err := newPlex(plexConfig("plex", origin.URL, testToken), mediasource.Deps{})
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Ping(t.Context()); err == nil {
		t.Error("Ping through a redirect succeeded")
	}
	if err := a.Refresh(t.Context(), showPath); err == nil {
		t.Error("Refresh through a redirect succeeded")
	}
	if n := reached.Load(); n != 0 {
		t.Errorf("the redirect target saw %d requests, want none", n)
	}
}

func TestRefreshSendsRefreshMetadata(t *testing.T) {
	f := library(t)
	const badPath = "/media/movies/Bad.mkv"
	f.put("1", row("../x", "movie", 1, 1, badPath))
	a := inProcess(t, f)
	if err := a.Refresh(t.Context(), showPath); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.log(), "PUT /library/metadata/101/refresh") {
		t.Errorf("requests %v, want the item's refresh", f.log())
	}
	sent := len(f.log())
	if err := a.Refresh(t.Context(), badPath); err == nil {
		t.Error("Refresh over an invalid rating key succeeded")
	}
	if after := f.log()[sent:]; len(after) != 0 {
		t.Errorf("requests %v for an invalid rating key, want none", after)
	}
}

// TestPlexStatusTransientRule: a Plex answer is classified by
// StatusError.IsTransient, which follows httpclient.IsRetryableStatus, so a
// 501 repeats and is permanent while a 503 is retried.
func TestPlexStatusTransientRule(t *testing.T) {
	for code, want := range map[int]bool{
		http.StatusServiceUnavailable: true, http.StatusTooManyRequests: true,
		http.StatusNotImplemented: false, http.StatusUnauthorized: false,
	} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			f := library(t)
			f.hook = func(r *http.Request) int {
				if r.Method == http.MethodPut {
					return code
				}
				return 0
			}
			got := setOver(t, inProcess(t, f)).Refresh(t.Context(), showPath, []string{"plex"})
			if got[0].Err == nil || got[0].Transient != want {
				t.Errorf("Refresh answered %d = %+v, want Transient %v", code, got[0], want)
			}
		})
	}
}

func TestLabelsSidecarsOnly(t *testing.T) {
	f := newFakePlex()
	f.addSection("1", "movie")
	movie := recorded(t, "movie-two-sidecars")
	f.put("1", movie)
	a := inProcess(t, f)
	got, err := a.Labels(t.Context(), files(movie)[0])
	want := []mediaserver.Label{{LanguageTag: "en", Display: "English"}, {LanguageTag: "fr", Display: "Français"}}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("Labels = %+v %v, want the two sidecar streams %+v", got, err, want)
	}
}

func TestLabelsFlagsAndCap(t *testing.T) {
	streams := make([]any, 0, 70)
	for i := range 70 {
		streams = append(streams, map[string]any{
			"id": i, "streamType": 3, "key": "/library/streams/" + string(rune('a'+i%26)),
			"languageTag": "en", "displayTitle": "English", "hearingImpaired": i == 0, "forced": i == 1,
		})
	}
	streams = append(streams, map[string]any{"id": 99, "streamType": 2, "key": "/library/streams/audio", "languageTag": "en"})
	item := row("301", "movie", 1, 1, "/media/movies/Many.mkv")
	item["Media"].([]any)[0].(map[string]any)["Part"].([]any)[0].(map[string]any)["Stream"] = streams
	f := newFakePlex()
	f.addSection("1", "movie")
	f.put("1", item)
	a := inProcess(t, f)
	got, err := a.Labels(t.Context(), "/media/movies/Many.mkv")
	if err != nil || len(got) != maxLabels {
		t.Fatalf("Labels = %d labels %v, want the first %d", len(got), err, maxLabels)
	}
	if got[0].Flags != (language.Flags{HI: true}) || got[1].Flags != (language.Flags{Forced: true}) || got[2].Flags != (language.Flags{}) {
		t.Errorf("flags %+v %+v %+v, want HI, Forced, none", got[0].Flags, got[1].Flags, got[2].Flags)
	}
}

func TestCallsAfterShutdownTransient(t *testing.T) {
	a := inProcess(t, library(t))
	if err := a.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, laerr := a.Labels(t.Context(), showPath)
	for name, err := range map[string]error{
		"Labels": laerr, "Refresh": a.Refresh(t.Context(), showPath), "Ping": a.Ping(t.Context()),
	} {
		if !errors.Is(err, errShutDown) || !httpclient.IsTransient(err) {
			t.Errorf("%s after Shutdown = %v, want the transient shut-down error", name, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Errorf("a second Shutdown = %v", err)
	}
}
