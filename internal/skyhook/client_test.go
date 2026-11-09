package skyhook

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/ssrf"
)

// fakeSkyHook answers in turn from answers, the last repeating, and records
// each request's URL.
type fakeSkyHook struct {
	answers []answer
	seen    []string
	mu      sync.Mutex
}

type answer struct {
	header http.Header
	body   []byte
	code   int
}

func ok(body []byte) answer { return answer{code: http.StatusOK, body: body} }

func (f *fakeSkyHook) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	a := f.answers[min(len(f.seen), len(f.answers)-1)]
	f.seen = append(f.seen, r.URL.String())
	h := a.header
	if h == nil {
		h = http.Header{}
	}
	return &http.Response{StatusCode: a.code, Header: h, Body: io.NopCloser(bytes.NewReader(a.body)), ContentLength: -1, Request: r}, nil
}

func (f *fakeSkyHook) requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.seen)
}

// fakeClient is New's client with f in place of the network.
func fakeClient(f *fakeSkyHook) *Client {
	c := New(nil, slog.New(slog.DiscardHandler))
	c.http.Transport = f
	return c
}

func recorded(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

const fireflyURL = origin + "/v2/tvdb/shows/eng/78874/official"

func TestShowRedirectToOtherHostRefused(t *testing.T) {
	for name, target := range map[string]string{
		"another host": "https://skyhook.example.org/v2/tvdb/shows/eng/78874/official",
		"another port": "https://skyhook.sonarr.tv:8443/v2/tvdb/shows/eng/78874/official",
		"http":         "http://skyhook.sonarr.tv/v2/tvdb/shows/eng/78874/official",
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeSkyHook{answers: []answer{{code: http.StatusFound, header: http.Header{"Location": {target}}}}}
			_, _, err := fakeClient(f).Show(t.Context(), 78874, "eng", "official")
			if _, refused := errors.AsType[*httpclient.RedirectRefusedError](err); !refused {
				t.Errorf("Show error = %v, want a refused redirect", err)
			}
			if got := f.requests(); !slices.Equal(got, []string{fireflyURL}) {
				t.Errorf("requests = %q, want only %s", got, fireflyURL)
			}
		})
	}
	t.Run("same origin is followed", func(t *testing.T) {
		moved := origin + "/v2/tvdb/shows/eng/78874/official?moved=1"
		f := &fakeSkyHook{answers: []answer{
			{code: http.StatusFound, header: http.Header{"Location": {moved}}},
			ok(recorded(t, "78874-eng-official.json")),
		}}
		if _, found, err := fakeClient(f).Show(t.Context(), 78874, "eng", "official"); !found || err != nil {
			t.Errorf("Show = %v, %v; want the moved answer", found, err)
		}
		if got := f.requests(); !slices.Equal(got, []string{fireflyURL, moved}) {
			t.Errorf("requests = %q", got)
		}
	})
}

type staticResolver []netip.Addr

func (r staticResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	return r, nil
}

func TestShowPrivateAddressRefused(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "10.1.2.3", "192.168.1.10", "169.254.169.254", "::1", "fd00::1"} {
		t.Run(addr, func(t *testing.T) {
			saved := transportOptions
			t.Cleanup(func() { transportOptions = saved })
			transportOptions = []ssrf.TransportOption{ssrf.WithResolver(staticResolver{netip.MustParseAddr(addr)})}
			f := &fakeSkyHook{answers: []answer{ok(recorded(t, "78874-eng-official.json"))}}
			c := New(&http.Client{Transport: f}, slog.New(slog.DiscardHandler))
			_, _, err := c.Show(t.Context(), 78874, "eng", "official")
			if serr, refused := errors.AsType[*ssrf.Error](err); !refused || serr.Kind != ssrf.KindNonPublicIP {
				t.Errorf("Show error = %v, want ssrf's non-public refusal", err)
			}
			if got := f.requests(); len(got) != 0 {
				t.Errorf("base's transport carried %q", got)
			}
		})
	}
}

func TestShowSegmentsValidated(t *testing.T) {
	refused := []struct {
		name, lang, order string
		id                int
	}{
		{"series id 0", "eng", "official", 0},
		{"series id 2^31", "eng", "official", 1 << 31},
		{"series id -1", "eng", "official", -1},
		{"language en", "en", "official", 78874},
		{"language ENG", "ENG", "official", 78874},
		{"language e-slash-g", "e/g", "official", 78874},
		{"language engl", "engl", "official", 78874},
		{"order with a dot-dot path", "eng", "dvd/../x", 78874},
		{"order of 33 bytes", "eng", strings.Repeat("a", 33), 78874},
		{"empty order", "eng", "", 78874},
		{"order DVD", "eng", "DVD", 78874},
		{"order with a query", "eng", "dvd?x=1", 78874},
	}
	for _, tc := range refused {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeSkyHook{answers: []answer{ok(recorded(t, "78874-eng-official.json"))}}
			if _, _, err := fakeClient(f).Show(t.Context(), tc.id, tc.lang, tc.order); err == nil {
				t.Error("Show error = nil, want a refused segment")
			}
			if got := f.requests(); len(got) != 0 {
				t.Errorf("requests = %q, want none", got)
			}
		})
	}
	t.Run("the bounds", func(t *testing.T) {
		f := &fakeSkyHook{answers: []answer{{code: http.StatusNotFound}}}
		c := fakeClient(f)
		long := strings.Repeat("a", 31) + "9"
		for _, id := range []int{1, 1<<31 - 1} {
			if _, _, err := c.Show(t.Context(), id, "zho", long); err != nil {
				t.Errorf("Show(%d) error = %v", id, err)
			}
		}
		want := []string{
			origin + "/v2/tvdb/shows/zho/1/" + long,
			origin + "/v2/tvdb/shows/zho/2147483647/" + long,
		}
		if got := f.requests(); !slices.Equal(got, want) {
			t.Errorf("requests = %q, want %q", got, want)
		}
	})
}

func TestShow404Absent(t *testing.T) {
	f := &fakeSkyHook{answers: []answer{{code: http.StatusNotFound, body: recorded(t, "2147483646-eng-official.404.json")}}}
	s, found, err := fakeClient(f).Show(t.Context(), 2147483646, "eng", "official")
	if found || err != nil || s.Episodes != nil || s.Orders != nil {
		t.Errorf("Show = %+v, %v, %v; want absent", s, found, err)
	}
	if n := len(f.requests()); n != 1 {
		t.Errorf("requests = %d, want 1", n)
	}
}

func TestShowTransientRetried(t *testing.T) {
	body := recorded(t, "78874-eng-official.json")
	t.Run("a 503 then a 200", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := &fakeSkyHook{answers: []answer{{code: http.StatusServiceUnavailable}, ok(body)}}
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
			defer cancel()
			if _, found, err := fakeClient(f).Show(ctx, 78874, "eng", "official"); !found || err != nil {
				t.Errorf("Show = %v, %v; want the second answer", found, err)
			}
			if n := len(f.requests()); n != 2 {
				t.Errorf("requests = %d, want 2", n)
			}
		})
	})
	t.Run("a 429's Retry-After", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := &fakeSkyHook{answers: []answer{{code: http.StatusTooManyRequests, header: http.Header{"Retry-After": {"7"}}}, ok(body)}}
			start := time.Now()
			if _, found, err := fakeClient(f).Show(t.Context(), 78874, "eng", "official"); !found || err != nil {
				t.Errorf("Show = %v, %v; want the second answer", found, err)
			}
			if waited := time.Since(start); waited != 7*time.Second {
				t.Errorf("waited %v before the retry, want the 7 s Retry-After", waited)
			}
		})
	})
	for _, code := range []int{http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusBadGateway, 599} {
		t.Run("three "+strconv.Itoa(code)+"s", func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &fakeSkyHook{answers: []answer{{code: code}}}
				_, _, err := fakeClient(f).Show(t.Context(), 78874, "eng", "official")
				if !httpclient.IsTransient(err) {
					t.Errorf("Show error = %v, want transient", err)
				}
				if n := len(f.requests()); n != maxAttempts {
					t.Errorf("requests = %d, want %d", n, maxAttempts)
				}
			})
		})
	}
	t.Run("the caller's deadline ends the wait", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := &fakeSkyHook{answers: []answer{{code: http.StatusServiceUnavailable, header: http.Header{"Retry-After": {"20"}}}}}
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			start := time.Now()
			_, _, err := fakeClient(f).Show(ctx, 78874, "eng", "official")
			if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) != 5*time.Second {
				t.Errorf("Show error = %v after %v, want the deadline at 5 s", err, time.Since(start))
			}
			if n := len(f.requests()); n != 1 {
				t.Errorf("requests = %d, want 1", n)
			}
		})
	})
}

func TestShowExhaustedLogsNoWarn(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var logs bytes.Buffer
		c := New(nil, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})))
		f := &fakeSkyHook{answers: []answer{{code: http.StatusServiceUnavailable}}}
		c.http.Transport = f
		if _, _, err := c.Show(t.Context(), 78874, "eng", "official"); !httpclient.IsTransient(err) {
			t.Fatalf("Show error = %v, want transient", err)
		}
		if n := len(f.requests()); n != maxAttempts {
			t.Errorf("requests = %d, want %d", n, maxAttempts)
		}
		if logs.Len() != 0 {
			t.Errorf("an exhausted Show logged at WARN or above: %s", logs.String())
		}
	})
}

func TestShowOtherStatusNotRetried(t *testing.T) {
	for _, code := range []int{http.StatusNoContent, http.StatusPartialContent, http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusGone, 600} {
		t.Run(strconv.Itoa(code), func(t *testing.T) {
			f := &fakeSkyHook{answers: []answer{{code: code, body: recorded(t, "78874-eng-official.json")}}}
			_, found, err := fakeClient(f).Show(t.Context(), 78874, "eng", "official")
			if found || err == nil || httpclient.IsTransient(err) {
				t.Errorf("Show = %v, %v; want a permanent error", found, err)
			}
			if err != nil && !strings.HasSuffix(err.Error(), "HTTP "+strconv.Itoa(code)) {
				t.Errorf("error text %q does not name the status", err)
			}
			if n := len(f.requests()); n != 1 {
				t.Errorf("requests = %d, want 1", n)
			}
		})
	}
}

// showOfSize is a valid answer of exactly n bytes: one episode, padded.
func showOfSize(n int) []byte {
	b := []byte(`{"seasonTypes":[{"type":"official","name":"Aired Order"}],"episodes":[{"tvdbId":1,"seasonNumber":1,"episodeNumber":1,"title":"Pilot"}]}`)
	return append(b, bytes.Repeat([]byte(" "), n-len(b))...)
}

func TestShowOverCapRefused(t *testing.T) {
	t.Run("17 MiB", func(t *testing.T) {
		f := &fakeSkyHook{answers: []answer{ok(showOfSize(17 << 20))}}
		_, found, err := fakeClient(f).Show(t.Context(), 78874, "eng", "official")
		if _, over := errors.AsType[*httpclient.ResponseTooLargeError](err); !over || found || httpclient.IsTransient(err) {
			t.Errorf("Show = %v, %v; want a permanent over-cap error", found, err)
		}
		if n := len(f.requests()); n != 1 {
			t.Errorf("requests = %d, want 1", n)
		}
	})
	t.Run("exactly the cap", func(t *testing.T) {
		f := &fakeSkyHook{answers: []answer{ok(showOfSize(maxBody))}}
		if s, found, err := fakeClient(f).Show(t.Context(), 78874, "eng", "official"); !found || err != nil || len(s.Episodes) != 1 {
			t.Errorf("Show = %v, %v, %d episodes; want the whole answer", found, err, len(s.Episodes))
		}
	})
}

// withEpisode is an answer listing a valid episode, then ep.
func withEpisode(ep string) string {
	return `{"episodes":[{"tvdbId":3,"seasonNumber":1,"episodeNumber":1},` + ep + `]}`
}

func TestShowOutOfRangeRefused(t *testing.T) {
	for name, body := range map[string]string{
		"tvdbId 0":                      withEpisode(`{"tvdbId":0,"seasonNumber":1,"episodeNumber":1}`),
		"no tvdbId":                     withEpisode(`{"seasonNumber":1,"episodeNumber":1}`),
		"tvdbId 2^53":                   withEpisode(`{"tvdbId":9007199254740992,"seasonNumber":1,"episodeNumber":1}`),
		"negative tvdbId":               withEpisode(`{"tvdbId":-4,"seasonNumber":1,"episodeNumber":1}`),
		"season 65536":                  withEpisode(`{"tvdbId":7,"seasonNumber":65536,"episodeNumber":1}`),
		"season -1":                     withEpisode(`{"tvdbId":7,"seasonNumber":-1,"episodeNumber":1}`),
		"episode 65536":                 withEpisode(`{"tvdbId":7,"seasonNumber":1,"episodeNumber":65536}`),
		"absolute 65536":                withEpisode(`{"tvdbId":7,"seasonNumber":1,"episodeNumber":1,"absoluteEpisodeNumber":65536}`),
		"no season number":              withEpisode(`{"tvdbId":7,"episodeNumber":1}`),
		"a null episode number":         withEpisode(`{"tvdbId":7,"seasonNumber":1,"episodeNumber":null}`),
		"a null episode":                withEpisode(`null`),
		"a season number past int64":    withEpisode(`{"tvdbId":7,"seasonNumber":1e30,"episodeNumber":1}`),
		"a string title":                withEpisode(`{"tvdbId":7,"seasonNumber":1,"episodeNumber":1,"title":5}`),
		"a fractional episode number":   withEpisode(`{"tvdbId":7,"seasonNumber":1,"episodeNumber":1.5}`),
		"seasonTypes of the wrong kind": `{"seasonTypes":{"type":"dvd"},"episodes":[]}`,
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeSkyHook{answers: []answer{ok([]byte(body))}}
			_, found, err := fakeClient(f).Show(t.Context(), 78874, "eng", "dvd")
			if found || err == nil || httpclient.IsTransient(err) {
				t.Errorf("Show = %v, %v; want a permanent error", found, err)
			}
			if n := len(f.requests()); n != 1 {
				t.Errorf("requests = %d, want 1", n)
			}
		})
	}
}

func TestShowNullTitleEmpty(t *testing.T) {
	f := &fakeSkyHook{answers: []answer{ok([]byte(`{"episodes":[
		{"tvdbId":11,"seasonNumber":2,"episodeNumber":3,"absoluteEpisodeNumber":null,"airDate":null,"title":null},
		{"tvdbId":12,"seasonNumber":0,"episodeNumber":0}
	]}`))}}
	s, found, err := fakeClient(f).Show(t.Context(), 78874, "fra", "official")
	if !found || err != nil {
		t.Fatalf("Show = %v, %v", found, err)
	}
	want := []Episode{{ID: 11, Season: 2, Number: 3}, {ID: 12}}
	if !slices.Equal(s.Episodes, want) {
		t.Errorf("Episodes = %+v, want %+v", s.Episodes, want)
	}
}

func TestShowErrorText(t *testing.T) {
	for name, a := range map[string]answer{
		"a status with a body": {code: http.StatusInternalServerError, body: []byte("leak-me\nsecond line")},
		"a refused status":     {code: http.StatusTeapot, body: []byte("leak-me")},
		"a type mismatch":      ok([]byte(`{"episodes":[{"tvdbId":"leak-me"}]}`)),
		"a negative id":        ok([]byte(`{"episodes":[{"tvdbId":-98765}]}`)),
		"malformed JSON":       ok([]byte(`{"episodes":[leak-me]}`)),
		"an out-of-range id":   ok([]byte(`{"episodes":[{"tvdbId":0,"title":"leak-me"}]}`)),
	} {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				f := &fakeSkyHook{answers: []answer{a}}
				_, _, err := fakeClient(f).Show(t.Context(), 121361, "eng", "dvd")
				if err == nil {
					t.Fatal("Show error = nil")
				}
				text := err.Error()
				if !strings.HasPrefix(text, "skyhook: series 121361, order dvd in eng: ") {
					t.Errorf("error %q does not name the series and order", text)
				}
				if strings.Contains(text, "leak-me") || strings.Contains(text, "98765") || strings.ContainsAny(text, "\r\n") {
					t.Errorf("error %q carries the body or breaks the line", text)
				}
			})
		})
	}
}
