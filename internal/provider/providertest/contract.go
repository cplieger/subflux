package providertest

import (
	"bytes"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
)

// Harness is one adapter's input to Contract.
type Harness struct {
	// Recorded serves the provider's recorded answers on every host of
	// Entry, robots.txt included for an Exception entry, so that Query finds
	// candidates and the first one fetches.
	Recorded http.Handler
	// Refused serves the provider's recorded answer to a refused credential;
	// nil only for an entry whose schema holds no Secret, which is then
	// checked to read a 401 as Permanent.
	Refused http.Handler
	Values  string // the settings YAML Build decodes
	Entry   provider.Entry
	Query   provider.Query
}

// Contract is the suite every adapter passes (design 5.1 and 5.3): its
// recorded search and fetch, a refused credential, a 429 with Retry-After,
// a 503 and an oversized body, each classified as the failure table says.
// Every case runs inside a synctest bubble, so retries cost no time.
func Contract(t *testing.T, h Harness) { //nolint:gocritic // hugeParam: each adapter's test passes its Harness inline
	t.Run("recorded", func(t *testing.T) { synctest.Test(t, func(t *testing.T) { h.recorded(t) }) })
	t.Run("refused", func(t *testing.T) { synctest.Test(t, func(t *testing.T) { h.refused(t) }) })
	t.Run("rate limited", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := h.searchFails(t, func(w http.ResponseWriter) {
				w.Header().Set("Retry-After", "120")
				w.WriteHeader(http.StatusTooManyRequests)
			})
			if f.Kind != provider.RateLimited || f.RetryAfter < h.backoff(2*time.Minute) {
				t.Errorf("429: %v (retry after %v), want RateLimited for at least %v", f, f.RetryAfter, h.backoff(2*time.Minute))
			}
		})
	})
	t.Run("server error", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			f := h.searchFails(t, func(w http.ResponseWriter) { w.WriteHeader(http.StatusServiceUnavailable) })
			want := provider.Transient
			if h.Entry.Exception {
				want = provider.RateLimited
			}
			if f.Kind != want || f.RetryAfter < h.backoff(0) {
				t.Errorf("503: %v (retry after %v), want %v for at least %v", f, f.RetryAfter, want, h.backoff(0))
			}
		})
	})
	t.Run("oversized body", func(t *testing.T) {
		synctest.Test(t, func(t *testing.T) {
			body := bytes.Repeat([]byte(" "), provider.MaxBody+1)
			f := h.searchFails(t, func(w http.ResponseWriter) {
				w.Header().Set("Content-Length", strconv.Itoa(len(body)))
				_, _ = w.Write(body)
			})
			if f.Kind != provider.Permanent || f.Cause != httpclient.CauseTooLarge {
				t.Errorf("oversized: %v (cause %v), want Permanent and CauseTooLarge", f, f.Cause)
			}
		})
	})
}

// backoff is the least RetryAfter a failure answered by a status carries: an
// Exception entry's hour (design 5.8), else d.
func (h *Harness) backoff(d time.Duration) time.Duration {
	if h.Entry.Exception {
		return max(d, time.Hour)
	}
	return d
}

func (h *Harness) recorded(t *testing.T) {
	ctx := Serve(t, t.Context(), h.Recorded, h.Entry.Hosts...)
	a := Build(t, h.Entry, h.Values)
	got, err := a.Search(ctx, h.Query)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("search found no candidate")
	}
	offered := h.Entry.Languages.Tags()
	for i := range got {
		c := &got[i]
		if !slices.Contains(offered, c.Lang) || !slices.Contains(h.Query.Langs, c.Lang) {
			t.Errorf("candidate %s: language %v is not one asked and mapped", c.Ref.Token(), c.Lang)
		}
	}
	b, err := a.Fetch(ctx, got[0].Ref)
	if err != nil || len(b) == 0 {
		t.Fatalf("fetch %s: %d bytes, %v", got[0].Ref.Token(), len(b), err)
	}
}

func (h *Harness) refused(t *testing.T) {
	secret := slices.ContainsFunc(h.Entry.Schema.Fields, func(f formschema.Field) bool { return f.Kind == formschema.Secret })
	if h.Refused == nil {
		if secret {
			t.Fatal("an entry with a Secret has no recorded refusal")
		}
		f := h.searchFails(t, func(w http.ResponseWriter) { w.WriteHeader(http.StatusUnauthorized) })
		if f.Kind != provider.Permanent {
			t.Errorf("401 with no Secret: %v, want Permanent, since no credential can fix it", f)
		}
		return
	}
	ctx := Serve(t, t.Context(), h.Refused, h.Entry.Hosts...)
	_, err := Build(t, h.Entry, h.Values).Search(ctx, h.Query)
	if f, ok := errors.AsType[*provider.Failure](err); !ok || f.Kind != provider.Auth {
		t.Errorf("refused credential: %v, want Auth", err)
	}
}

// searchFails runs Query against a server answering every request but
// robots.txt with answer, and returns the Failure the search must end in.
func (h *Harness) searchFails(t *testing.T, answer func(http.ResponseWriter)) *provider.Failure {
	t.Helper()
	ctx := Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/robots.txt" {
			h.Recorded.ServeHTTP(w, r)
			return
		}
		answer(w)
	}), h.Entry.Hosts...)
	_, err := Build(t, h.Entry, h.Values).Search(ctx, h.Query)
	f, ok := errors.AsType[*provider.Failure](err)
	if !ok {
		t.Fatalf("search: %v, want a *provider.Failure", err)
	}
	return f
}
