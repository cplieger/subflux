package mediaserver_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediasource"
)

var tvMaps = []mediasource.PathMapping{{From: "/srv", To: "/m"}, {From: "/srv/tv", To: "/m/tv"}}

func TestRefreshMapsToServerView(t *testing.T) {
	st := newStore(t)
	f := &fake{}
	s := newSet(t, st, map[string]*fake{"plex": f}, cfg("plex", "http://a", tvMaps...))

	t.Run("mapped", func(t *testing.T) {
		got := s.Refresh(t.Context(), "/m/tv/show/s01.mkv", []string{"plex"})
		if len(got) != 1 || got[0].Server != "plex" || got[0].Err != nil {
			t.Fatalf("Refresh = %+v, want plex's success", got)
		}
		if refreshed := f.calls(); !slices.Equal(refreshed, []string{"/srv/tv/show/s01.mkv"}) {
			t.Errorf("refreshed %v, want the server path asked once", refreshed)
		}
	})
	t.Run("labels ask the server path", func(t *testing.T) {
		f.byPath = map[string][]mediaserver.Label{"/srv/tv/show/s01.mkv": {{LanguageTag: "en", Display: "English"}}}
		got, err := s.Labels(t.Context(), "/m/tv/show/s01.mkv")
		if err != nil || len(got) != 1 || got[0].Err != "" || !slices.Equal(got[0].Labels, f.byPath["/srv/tv/show/s01.mkv"]) {
			t.Errorf("Labels = %+v %v, want the server path's labels", got, err)
		}
	})
	t.Run("unmatched passes through", func(t *testing.T) {
		if got := s.Refresh(t.Context(), "/other/a.mkv", []string{"plex"}); got[0].Err != nil {
			t.Errorf("Refresh = %+v, want a success", got)
		}
		if refreshed := f.calls(); refreshed[len(refreshed)-1] != "/other/a.mkv" {
			t.Errorf("refreshed %v, want /other/a.mkv as given", refreshed)
		}
	})
	t.Run("aliased refused", func(t *testing.T) {
		got := s.Refresh(t.Context(), "/srv/tv/a.mkv", []string{"plex"})
		if !errors.As(got[0].Err, new(*mediasource.AliasedPathError)) || got[0].Transient {
			t.Errorf("Refresh = %+v, want a permanent AliasedPathError", got)
		}
		if _, raised := alert(st, "plex"); !raised {
			t.Error("no media_server:plex alert after a failed Refresh")
		}
	})
	t.Run("success clears the alert", func(t *testing.T) {
		s.Refresh(t.Context(), "/m/tv/show/s01.mkv", []string{"plex"})
		if text, raised := alert(st, "plex"); raised {
			t.Errorf("alert %q still raised after a success", text)
		}
	})
}

func TestRefreshResultsInServersOrder(t *testing.T) {
	st := newStore(t)
	failing := &fake{refresh: func(context.Context, string) error { return errors.New("boom") }}
	s := newSet(t, st, map[string]*fake{"a": {}, "b": failing}, cfg("a", "http://a"), cfg("b", "http://b"))
	got := s.Refresh(t.Context(), "/m/a.mkv", []string{"b", "nope", "a"})
	if len(got) != 3 || got[0].Server != "b" || got[1].Server != "nope" || got[2].Server != "a" {
		t.Fatalf("Refresh = %+v, want b, nope, a", got)
	}
	if got[0].Err == nil || got[1].Err == nil || got[2].Err != nil {
		t.Errorf("errors %v, %v, %v; want b and nope failing, a succeeding", got[0].Err, got[1].Err, got[2].Err)
	}
	if _, raised := alert(st, "nope"); raised {
		t.Error("an unknown server raised an alert")
	}
}

// TestStalledBodyIsTransient: httpclient counts a passed caller deadline as
// final, but the Set's caller passes its per-call bound, so a server that
// sends headers and stalls must be retried.
func TestStalledBodyIsTransient(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })
	f := &fake{refresh: func(ctx context.Context, _ string) error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, http.NoBody)
		if err != nil {
			return err
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, err = io.ReadAll(resp.Body)
		return err
	}}
	st := newStore(t)
	s := newSet(t, st, map[string]*fake{"plex": f}, cfg("plex", "http://a"))
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	got := s.Refresh(ctx, "/m/a.mkv", []string{"plex"})
	if got[0].Err == nil || !got[0].Transient {
		t.Errorf("Refresh = %+v, want a transient failure", got[0])
	}
	if _, raised := alert(st, "plex"); !raised {
		t.Error("no alert after the stalled call")
	}
}

func TestCancelledRefreshRaisesNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	f := &fake{refresh: func(ctx context.Context, _ string) error {
		cancel()
		return ctx.Err()
	}}
	st := newStore(t)
	s := newSet(t, st, map[string]*fake{"plex": f}, cfg("plex", "http://a"))
	got := s.Refresh(ctx, "/m/a.mkv", []string{"plex"})
	if !errors.Is(got[0].Err, context.Canceled) || got[0].Transient {
		t.Errorf("Refresh = %+v, want the cancellation, not transient", got[0])
	}
	if _, raised := alert(st, "plex"); raised {
		t.Error("the caller's cancellation raised the server's alert")
	}
}

func TestTransient503Permanent401(t *testing.T) {
	for code, want := range map[int]bool{http.StatusServiceUnavailable: true, http.StatusUnauthorized: false} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			f := &fake{refresh: func(context.Context, string) error { return &httpclient.StatusError{Code: code} }}
			s := newSet(t, newStore(t), map[string]*fake{"plex": f}, cfg("plex", "http://a"))
			got := s.Refresh(t.Context(), "/m/a.mkv", []string{"plex"})
			if got[0].Err == nil || got[0].Transient != want {
				t.Errorf("Refresh = %+v, want Transient %v", got[0], want)
			}
		})
	}
}

func TestFailedConstructorRecovers(t *testing.T) {
	st := newStore(t)
	good := &fake{}
	s := newSet(t, st, map[string]*fake{"good": good}, cfg("good", "http://a"), cfg("broken", "bad"))
	if text, raised := alert(st, "broken"); !raised || !strings.Contains(text, errBadURL.Error()) {
		t.Errorf("alert %q %v, want media_server:broken raised naming New's error", text, raised)
	}
	if got := s.Servers(); !slices.Equal(got, []string{"good"}) {
		t.Errorf("Servers = %v, want only good", got)
	}
	labels, err := s.Labels(t.Context(), "/m/a.mkv")
	if err != nil || len(labels) != 2 || labels[1].Server != "broken" || !strings.Contains(labels[1].Err, errBadURL.Error()) {
		t.Errorf("Labels = %+v %v, want broken's entry carrying New's error", labels, err)
	}
	if err := s.Ping(t.Context(), "broken"); !errors.Is(err, errBadURL) {
		t.Errorf("Ping(broken) = %v, want New's error", err)
	}
	if got := s.Refresh(t.Context(), "/m/a.mkv", []string{"good"}); got[0].Err != nil {
		t.Errorf("Refresh(good) = %+v, want a success beside the broken entry", got[0])
	}

	newSet(t, st, nil, cfg("good", "http://a"), cfg("broken", "http://fixed"))
	if text, raised := alert(st, "broken"); raised {
		t.Errorf("alert %q still raised after a rebuild whose New succeeds", text)
	}
}

func TestNewSetProgrammingFaults(t *testing.T) {
	k := []mediaserver.AdapterKind{fakeKind(nil)}
	other := cfg("x", "http://a")
	other.Type = "jellyfin"
	cases := map[string][]mediaserver.Config{
		"unknown type": {other},
		"duplicate id": {cfg("a", "http://a"), cfg("a", "http://b")},
	}
	for name, cfgs := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := mediaserver.NewSet(k, cfgs, mediasource.Deps{}, newStore(t)); err == nil {
				t.Error("NewSet succeeded")
			}
		})
	}
	t.Run("no store", func(t *testing.T) {
		if _, err := mediaserver.NewSet(k, nil, mediasource.Deps{}, nil); err == nil {
			t.Error("NewSet succeeded without a status store")
		}
	})
}

func TestSetPing(t *testing.T) {
	st := newStore(t)
	down := errors.New("connection refused")
	f := &fake{}
	s := newSet(t, st, map[string]*fake{"plex": f}, cfg("plex", "http://a"), cfg("broken", "bad"))
	t.Run("live", func(t *testing.T) {
		if err := s.Ping(t.Context(), "plex"); err != nil {
			t.Errorf("Ping = %v", err)
		}
		f.ping = func(context.Context) error { return down }
		if err := s.Ping(t.Context(), "plex"); !errors.Is(err, down) {
			t.Errorf("Ping = %v, want the adapter's error", err)
		}
		if _, raised := alert(st, "plex"); !raised {
			t.Error("a failed Ping raised no alert")
		}
	})
	t.Run("unknown", func(t *testing.T) {
		if err := s.Ping(t.Context(), "nope"); err == nil {
			t.Error("Ping of an unknown server succeeded")
		}
	})
	t.Run("failed constructor", func(t *testing.T) {
		if err := s.Ping(t.Context(), "broken"); !errors.Is(err, errBadURL) {
			t.Errorf("Ping = %v, want New's error", err)
		}
	})
}

func TestSetShutdownWaitsForAdapters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		var aDone, bDone bool
		a := &fake{shutdown: func(context.Context) error { <-release; aDone = true; return nil }}
		b := &fake{shutdown: func(context.Context) error { bDone = true; return errors.New("b stuck") }}
		s := newSet(t, newStore(t), map[string]*fake{"a": a, "b": b}, cfg("a", "http://a"), cfg("b", "http://b"))

		returned := make(chan error, 1)
		go func() { returned <- s.Shutdown(t.Context()) }()
		synctest.Wait()
		select {
		case err := <-returned:
			t.Fatalf("Shutdown returned %v before a's Shutdown did", err)
		default:
		}
		got := s.Refresh(t.Context(), "/m/a.mkv", []string{"a"})
		if got[0].Err == nil || !got[0].Transient || !strings.Contains(got[0].Err.Error(), "server set closed") {
			t.Errorf("Refresh during Shutdown = %+v, want a transient closed error", got[0])
		}
		close(release)
		if err := <-returned; err == nil || !strings.Contains(err.Error(), "b stuck") {
			t.Errorf("Shutdown = %v, want b's error", err)
		}
		if !aDone || !bDone {
			t.Errorf("adapters shut down: a %v, b %v", aDone, bDone)
		}
		if err := s.Ping(t.Context(), "a"); err == nil || !strings.Contains(err.Error(), "server set closed") {
			t.Errorf("Ping after Shutdown = %v, want closed", err)
		}
		if err := s.Shutdown(t.Context()); err == nil {
			t.Error("a second Shutdown lost the first one's error")
		}
	})
}

func TestSetShutdownHonoursContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		release := make(chan struct{})
		a := &fake{shutdown: func(context.Context) error { <-release; return nil }}
		s := newSet(t, newStore(t), map[string]*fake{"a": a}, cfg("a", "http://a"))
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Shutdown = %v, want its context's deadline", err)
		}
		close(release)
		if err := s.Shutdown(t.Context()); err != nil {
			t.Errorf("a later Shutdown = %v, want the adapters' nil", err)
		}
	})
}

func TestLabelsFailOnlyOnCancel(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		boom := errors.New("boom")
		failing := &fake{labels: func(context.Context, string) ([]mediaserver.Label, error) { return nil, boom }}
		hung := &fake{labels: func(ctx context.Context, _ string) ([]mediaserver.Label, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		answering := &fake{byPath: map[string][]mediaserver.Label{"/m/a.mkv": {
			{LanguageTag: "en", Display: "English", Flags: language.Flags{HI: true}},
			{LanguageTag: "fr", Display: "Français", Flags: language.Flags{Forced: true}},
		}}}
		s := newSet(t, newStore(t), map[string]*fake{"f": failing, "h": hung, "ok": answering},
			cfg("f", "http://a"), cfg("h", "http://b"), cfg("ok", "http://c"))

		start := time.Now()
		got, err := s.Labels(t.Context(), "/m/a.mkv")
		if err != nil {
			t.Fatalf("Labels = %v, want per-server failures only", err)
		}
		if waited := time.Since(start); waited != 5*time.Second {
			t.Errorf("Labels took %v, want the 5 s per-server bound", waited)
		}
		if len(got) != 3 || got[0].Server != "f" || got[1].Server != "h" || got[2].Server != "ok" {
			t.Fatalf("Labels = %+v, want f, h, ok in entry order", got)
		}
		if !strings.Contains(got[0].Err, "boom") || got[1].Err == "" || got[2].Err != "" {
			t.Errorf("errors %q, %q, %q; want f's and h's only", got[0].Err, got[1].Err, got[2].Err)
		}
		if want := answering.byPath["/m/a.mkv"]; !slices.Equal(got[2].Labels, want) {
			t.Errorf("ok's labels %+v, want %+v", got[2].Labels, want)
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		if got, err := s.Labels(ctx, "/m/a.mkv"); !errors.Is(err, context.Canceled) || got != nil {
			t.Errorf("Labels on a cancelled context = %+v %v, want the cancellation", got, err)
		}
	})
}

// dirty holds every rune class a server's text must not carry into a label.
const dirty = "en\nUS\u202eX\xff"

func TestLabelsBoundedStrings(t *testing.T) {
	clean := mediaserver.Label{File: "/m/a.en.srt", LanguageTag: "en", Display: "English (SDH)"}
	f := &fake{byPath: map[string][]mediaserver.Label{"/m/a.mkv": {
		{File: dirty, LanguageTag: dirty, Display: strings.Repeat("é", 1<<19)},
		clean,
	}}}
	s := newSet(t, newStore(t), map[string]*fake{"plex": f}, cfg("plex", "http://a"))
	got, err := s.Labels(t.Context(), "/m/a.mkv")
	if err != nil || len(got) != 1 || len(got[0].Labels) != 2 {
		t.Fatalf("Labels = %+v %v", got, err)
	}
	l := got[0].Labels[0]
	for name, v := range map[string]string{"File": l.File, "LanguageTag": l.LanguageTag, "Display": l.Display} {
		assertBounded(t, name, v)
	}
	if !strings.HasSuffix(l.Display, "…") {
		t.Errorf("Display %q, want the cut marked", l.Display[len(l.Display)-8:])
	}
	if got[0].Labels[1] != clean {
		t.Errorf("clean label %+v, want it unchanged", got[0].Labels[1])
	}
}

func TestLabelsErrorBounded(t *testing.T) {
	long := strings.Repeat("line one\nline two "+dirty, 8<<10/24)
	f := &fake{labels: func(context.Context, string) ([]mediaserver.Label, error) { return nil, errors.New(long) }}
	s := newSet(t, newStore(t), map[string]*fake{"plex": f}, cfg("plex", "http://a"))
	got, err := s.Labels(t.Context(), "/m/a.mkv")
	if err != nil || len(got) != 1 {
		t.Fatalf("Labels = %+v %v", got, err)
	}
	assertBounded(t, "Err", got[0].Err)
	if !strings.HasPrefix(got[0].Err, "reading the labels: line one line two") {
		t.Errorf("Err %q, want the error's text on one line", got[0].Err)
	}
}

func assertBounded(t *testing.T, name, v string) {
	t.Helper()
	if !utf8.ValidString(v) || strings.ContainsAny(v, "\n\r\u202e") || len(v) > 512 {
		t.Errorf("%s %.60q (%d bytes), want valid one-line UTF-8 of at most 512 bytes", name, v, len(v))
	}
}
