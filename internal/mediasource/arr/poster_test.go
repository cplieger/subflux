package arr_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/arr"
)

func servePoster(size int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(bytes.Repeat([]byte{0xff}, size))
	}
}

func TestPosterOverCapRefused(t *testing.T) {
	u := newUpstream(t)
	u.on("/big.jpg", servePoster(3<<20))
	u.on("/ok.jpg", servePoster(1<<20))
	p := arr.NewPoster(nil)
	_, _, _, err := p.Fetch(t.Context(), u.srv.URL+"/big.jpg", testKey)
	if got := classifiedCause(err); got != mediasource.Protocol {
		t.Errorf("a 3 MiB poster: %v, want protocol", err)
	}
	body, ctype, ok, err := p.Fetch(t.Context(), u.srv.URL+"/ok.jpg", testKey)
	if err != nil || !ok || ctype != "image/jpeg" {
		t.Fatalf("a 1 MiB poster = %v %q %v", ok, ctype, err)
	}
	defer body.Close()
	if b, _ := io.ReadAll(body); len(b) != 1<<20 {
		t.Errorf("read %d bytes, want the whole 1 MiB body", len(b))
	}
	if got := u.keys(); got[0].key != testKey {
		t.Errorf("the arr leg sent key %q, want the configured key", got[0].key)
	}
}

func TestPoster404False(t *testing.T) {
	u := newUpstream(t)
	u.on("/none.jpg", http.NotFound)
	u.on("/err.jpg", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) })
	p := arr.NewPoster(&http.Client{})
	if body, _, ok, err := p.Fetch(t.Context(), u.srv.URL+"/none.jpg", testKey); ok || err != nil || body != nil {
		t.Errorf("a 404 poster = %v %v, want false with no error", ok, err)
	}
	if _, _, _, err := p.Fetch(t.Context(), u.srv.URL+"/err.jpg", testKey); classifiedCause(err) != mediasource.Unreachable {
		t.Errorf("a 503 poster = %v, want unreachable", err)
	}
}

func TestPosterPublicLegRefusesPrivate(t *testing.T) {
	for _, loc := range []string{"https://10.0.0.1/p.jpg", "https://localhost/p.jpg", "http://cdn.example/p.jpg"} {
		u := newUpstream(t)
		u.on("/poster.jpg", redirectTo(loc))
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, _, ok, err := arr.NewPoster(nil).Fetch(ctx, u.srv.URL+"/poster.jpg", testKey)
		cancel()
		if ok || classifiedCause(err) != mediasource.Protocol {
			t.Errorf("a redirect to %s = %v %v, want protocol and nothing fetched", loc, ok, err)
		}
	}
}

// hung is an upstream that accepts every request and never answers.
type hung struct{}

func (hung) RoundTrip(r *http.Request) (*http.Response, error) {
	<-r.Context().Done()
	return nil, r.Context().Err()
}

func TestPosterUndeadlinedFetchBounded(t *testing.T) {
	cases := map[string]struct {
		budget time.Duration
		want   time.Duration
	}{
		"no deadline":     {want: 30 * time.Second},
		"caller deadline": {budget: time.Minute, want: time.Minute},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := t.Context()
				if tc.budget > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, tc.budget)
					defer cancel()
				}
				start := time.Now()
				_, _, ok, err := arr.NewPoster(&http.Client{Transport: hung{}}).Fetch(ctx, "http://arr.example/poster.jpg", testKey)
				if ok || classifiedCause(err) != mediasource.Unreachable || time.Since(start) != tc.want {
					t.Errorf("Fetch = %v %v after %v, want unreachable after %v", ok, err, time.Since(start), tc.want)
				}
			})
		})
	}
}

func classifiedCause(err error) mediasource.Cause {
	se, ok := err.(*mediasource.SourceError) //nolint:errorlint // Fetch answers a *SourceError itself
	if !ok {
		return 0
	}
	return se.Cause
}
