package provider

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
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
