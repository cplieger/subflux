package provider_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

func redirectTo(url string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, url, http.StatusFound) }
}

func TestDialHookKeepsHostPin(t *testing.T) {
	t.Run("an unlisted host never reaches the hook", func(t *testing.T) {
		ctx, calls := recordingHook(t.Context())
		c := clientFor(t, host)
		_, err := get(t, ctx, c, "https://"+other+"/")
		if f := failure(t, err); f.Kind != provider.Permanent || f.Host != other {
			t.Errorf("direct: %+v, want Permanent on %s", f, other)
		}
		ctx = providertest.Serve(t, ctx, redirectTo("https://"+other+"/x"), host)
		_, err = get(t, ctx, c, "https://"+host+"/")
		if f := failure(t, err); f.Kind != provider.Permanent || f.Host != other {
			t.Errorf("redirect: %+v, want Permanent on %s", f, other)
		}
		if n := calls.Load(); n != 0 {
			t.Errorf("hook called %d times, want 0", n)
		}
	})
	t.Run("a listed host answers through Serve", func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), text("ok"), host)
		got, err := get(t, ctx, clientFor(t, host), "https://"+host+"/")
		if err != nil {
			t.Fatal(err)
		}
		if got != "ok" {
			t.Errorf("body %q, want ok", got)
		}
	})
	t.Run("a GET retries in virtual time", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			var n atomic.Int64
			ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if n.Add(1) == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte("ok"))
			}), host)
			start := time.Now()
			if _, err := get(t, ctx, clientFor(t, host), "https://"+host+"/"); err != nil {
				t.Fatal(err)
			}
			// httpclient.Do's first wait is the 2 s base with equal jitter.
			if d := time.Since(start); d < time.Second || d > 2*time.Second || n.Load() != 2 {
				t.Errorf("elapsed %v with %d requests, want 1 to 2 s and 2", d, n.Load())
			}
		})
	})
}

func TestServeIsolatedUnderParallel(t *testing.T) {
	for _, name := range []string{"a", "b"} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ctx := providertest.Serve(t, t.Context(), text(name), host)
			c := clientFor(t, host, other)
			for i := range 50 {
				got, err := get(t, ctx, c, fmt.Sprintf("https://%s/%d", host, i))
				if err != nil {
					t.Fatal(err)
				}
				if got != name {
					t.Fatalf("request %d answered %q, want %q", i, got, name)
				}
			}
			if _, err := send(c, request(t, ctx, http.MethodPost, "https://"+other+"/")); err == nil {
				t.Error("a host no Serve lists answered")
			}
		})
	}
}

func TestHTTPClientKeepsHostPin(t *testing.T) {
	const gh = "github.com"
	c := clientFor(t, gh)
	t.Run("every hop is pinned", func(t *testing.T) {
		ctx, calls := recordingHook(t.Context())
		ctx = providertest.Serve(t, ctx, redirectTo("https://"+other+"/asset"), gh)
		hc := c.HTTPClient()
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return nil }
		resp, err := hc.Do(request(t, ctx, http.MethodGet, "https://"+gh+"/release"))
		if err == nil {
			resp.Body.Close()
			t.Fatal("redirect to an unlisted host answered")
		}
		if f := failure(t, err); f.Kind != provider.Permanent || f.Host != other {
			t.Errorf("%+v, want Permanent on %s", f, other)
		}
		if n := calls.Load(); n != 0 {
			t.Errorf("unlisted host dialed %d times", n)
		}
	})
	t.Run("a fresh client each call", func(t *testing.T) {
		if a, b := c.HTTPClient(), c.HTTPClient(); a == b {
			t.Error("HTTPClient returned one client twice")
		}
	})
}

// TestHTTPClientTimeout: a call under a context with no deadline ends at
// the client's 3-minute Timeout.
func TestHTTPClientTimeout(t *testing.T) {
	const gh = "github.com"
	synctest.Test(t, func(t *testing.T) {
		ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}), gh)
		start := time.Now()
		resp, err := clientFor(t, gh).HTTPClient().Do(request(t, ctx, http.MethodGet, "https://"+gh+"/stall"))
		if err != nil {
			t.Fatal(err)
		}
		// Over net.Pipe the cancelled read can end as EOF or as an error;
		// either way it ends at the Timeout, and with no Timeout the bubble
		// deadlocks.
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if len(b) != 0 {
			t.Fatalf("read %q from a stalled body", b)
		}
		if d := time.Since(start); d != 3*time.Minute {
			t.Errorf("ended after %v, want 3m", d)
		}
	})
}

func TestUnsolicited304Permanent(t *testing.T) {
	ctx := providertest.Serve(t, t.Context(), status(http.StatusNotModified), host)
	c := clientFor(t, host)
	_, err := get(t, ctx, c, "https://"+host+"/")
	if f := failure(t, err); f.Kind != provider.Permanent || f.Status != http.StatusNotModified {
		t.Errorf("unsolicited: %+v, want Permanent 304", f)
	}
	for _, h := range []string{"If-None-Match", "If-Modified-Since"} {
		req := request(t, ctx, http.MethodGet, "https://"+host+"/")
		req.Header.Set(h, `"v1"`)
		resp, err := c.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", h, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusNotModified {
			t.Errorf("%s: status %d, want 304", h, resp.StatusCode)
		}
	}
}

func TestClientOwnsUserAgent(t *testing.T) {
	var got atomic.Value
	ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got.Store(r.UserAgent())
	}), host)
	req := request(t, ctx, http.MethodGet, "https://"+host+"/")
	req.Header.Set("User-Agent", "adapter/2")
	if _, err := send(clientFor(t, host), req); err != nil {
		t.Fatal(err)
	}
	if ua, _ := got.Load().(string); !strings.HasPrefix(ua, "subflux/") || !strings.HasSuffix(ua, " (+https://github.com/cplieger/subflux)") {
		t.Errorf("User-Agent %q, want subflux's own over the adapter's", ua)
	}
	e := entry(&scripted{}, []string{host})
	e.UserAgent = "Mozilla/5.0"
	if _, err := provider.Build(e, providertest.Config(t, e, "")); err == nil {
		t.Error("built an entry that is no Exception with its own User-Agent")
	}
}
