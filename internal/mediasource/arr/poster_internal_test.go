package arr

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/cplieger/subflux/internal/mediasource"
)

// cdn is an in-memory public origin at https://cdn.example: /hop/<n>
// redirects to /hop/<n-1>, /hop/0 answers the image; it records each
// request's key header.
type cdn struct {
	mu   sync.Mutex
	keys []string
}

func (c *cdn) RoundTrip(r *http.Request) (*http.Response, error) {
	c.mu.Lock()
	c.keys = append(c.keys, r.Header.Get("X-Api-Key"))
	c.mu.Unlock()
	rec := httptest.NewRecorder()
	n, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/hop/"))
	if n > 0 {
		http.Redirect(rec, r, "https://cdn.example/hop/"+strconv.Itoa(n-1), http.StatusFound)
	} else {
		rec.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(rec, "png")
	}
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

func TestPosterCDNRedirectRefetchedWithoutKey(t *testing.T) {
	var mu sync.Mutex
	var arrKeys []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrKeys = append(arrKeys, r.Header.Get("X-Api-Key"))
		mu.Unlock()
		switch r.URL.Path {
		case "/same":
			http.Redirect(w, r, "/cover?"+r.URL.RawQuery, http.StatusFound)
		default:
			http.Redirect(w, r, "https://cdn.example"+r.URL.Query().Get("to"), http.StatusFound)
		}
	}))
	defer srv.Close()
	fetch := func(t *testing.T, to string) (*cdn, error) {
		c := &cdn{}
		p := NewPoster(nil)
		p.public.Transport = c
		body, ctype, ok, err := p.Fetch(t.Context(), srv.URL+"/same?to="+to, "test-key-arr")
		if err == nil && (!ok || ctype != "image/png") {
			t.Errorf("Fetch = %v %q, want the CDN's png", ok, ctype)
		}
		if body != nil {
			_ = body.Close()
		}
		return c, err
	}
	t.Run("within the hop cap", func(t *testing.T) {
		c, err := fetch(t, "/hop/3")
		if err != nil {
			t.Fatalf("Fetch over 5 hops = %v, want the poster", err)
		}
		for _, k := range c.keys {
			if k != "" {
				t.Errorf("the public leg sent key %q", k)
			}
		}
		mu.Lock()
		last := arrKeys[len(arrKeys)-1]
		mu.Unlock()
		if len(c.keys) != 4 || last != "test-key-arr" {
			t.Errorf("%d public requests and arr key %q, want 4 and the key on the arr leg", len(c.keys), last)
		}
	})
	t.Run("past the hop cap", func(t *testing.T) {
		_, err := fetch(t, "/hop/4")
		se, ok := err.(*mediasource.SourceError) //nolint:errorlint // Fetch answers a *SourceError itself
		if !ok || se.Cause != mediasource.Protocol {
			t.Errorf("Fetch over 6 hops = %v, want protocol", err)
		}
	})
}
