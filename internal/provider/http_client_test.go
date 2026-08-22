package provider

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/subflux/internal/httpwire"
)

// capTestClient builds a client via the NewHTTPClient factory and swaps only
// the innermost SSRF dial layer for the default transport, because the SSRF
// transport rejects loopback addresses by design (pinned by provider tests),
// which would block the httptest server before the body cap is reached. The
// factory's layering (body cap outermost, then User-Agent injection), timeout,
// and redirect policy all remain in effect.
func capTestClient(t *testing.T) *http.Client {
	t.Helper()
	c := NewHTTPClient(HTTPTimeoutStandard)
	capT, ok := c.Transport.(*bodyCapTransport)
	if !ok {
		t.Fatalf("NewHTTPClient Transport = %T, want *bodyCapTransport outermost", c.Transport)
	}
	uaT, ok := capT.base.(*userAgentTransport)
	if !ok {
		t.Fatalf("bodyCapTransport.base = %T, want *userAgentTransport", capT.base)
	}
	uaT.base = http.DefaultTransport
	return c
}

func TestNewHTTPClient_responseBodyCeiling(t *testing.T) {
	t.Run("body beyond ceiling errors", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			chunk := bytes.Repeat([]byte("x"), 1<<20)
			for written := int64(0); written <= maxResponseBodyBytes; written += int64(len(chunk)) {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		}))
		defer srv.Close()

		resp, err := capTestClient(t).Get(srv.URL)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		defer resp.Body.Close()

		_, err = io.ReadAll(resp.Body)
		if !errors.Is(err, errResponseBodyTooLarge) {
			t.Fatalf("ReadAll() error = %v, want errResponseBodyTooLarge", err)
		}
	})

	t.Run("body under ceiling passes through byte-identical", func(t *testing.T) {
		want := bytes.Repeat([]byte("subflux-cap-probe."), 1024)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if _, err := w.Write(want); err != nil {
				return
			}
		}))
		defer srv.Close()

		resp, err := capTestClient(t).Get(srv.URL)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		defer resp.Body.Close()

		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll() unexpected error: %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("body mismatch: got %d bytes, want %d bytes", len(got), len(want))
		}
	})
	t.Run("body of exactly the ceiling reads cleanly", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			chunk := bytes.Repeat([]byte("y"), 1<<20)
			for written := int64(0); written < maxResponseBodyBytes; written += int64(len(chunk)) {
				if _, err := w.Write(chunk); err != nil {
					return
				}
			}
		}))
		defer srv.Close()

		resp, err := capTestClient(t).Get(srv.URL)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		defer resp.Body.Close()

		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll() of a body at the ceiling = %v, want nil (only data BEYOND the ceiling errors)", err)
		}
		if int64(len(got)) != maxResponseBodyBytes {
			t.Errorf("ReadAll() returned %d bytes, want %d", len(got), maxResponseBodyBytes)
		}
	})
}

// TestNewHTTPClient_userAgentInjection asserts the factory identifies subflux on
// every request that does not carry a User-Agent, and never overrides one the
// caller set (a provider that has to look like a browser sets its own).
func TestNewHTTPClient_userAgentInjection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := io.WriteString(w, r.Header.Get("User-Agent")); err != nil {
			return
		}
	}))
	defer srv.Close()

	t.Run("injected when the caller sets none", func(t *testing.T) {
		resp, err := capTestClient(t).Get(srv.URL)
		if err != nil {
			t.Fatalf("Get() unexpected error: %v", err)
		}
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll() unexpected error: %v", err)
		}
		if string(got) != httpwire.UserAgent {
			t.Errorf("received User-Agent = %q, want %q", got, httpwire.UserAgent)
		}
	})

	t.Run("caller value preserved", func(t *testing.T) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
		if err != nil {
			t.Fatalf("NewRequestWithContext: %v", err)
		}
		const browserUA = "Mozilla/5.0 (subflux provider override)"
		req.Header.Set("User-Agent", browserUA)

		resp, err := capTestClient(t).Do(req)
		if err != nil {
			t.Fatalf("Do() unexpected error: %v", err)
		}
		defer resp.Body.Close()
		got, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Fatalf("ReadAll() unexpected error: %v", err)
		}
		if string(got) != browserUA {
			t.Errorf("received User-Agent = %q, want the caller's %q", got, browserUA)
		}
	})
}

// The no-client-timeout factory must wire the same ceiling; anidb's large
// mapping downloads are still well under it (MaxDownloadBytes+1).
func TestNewHTTPClientNoClientTimeout_hasBodyCap(t *testing.T) {
	t.Parallel()
	c := NewHTTPClientNoClientTimeout()
	if _, ok := c.Transport.(*bodyCapTransport); !ok {
		t.Fatalf("NewHTTPClientNoClientTimeout Transport = %T, want *bodyCapTransport outermost", c.Transport)
	}
}
