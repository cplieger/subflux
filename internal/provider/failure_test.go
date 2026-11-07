package provider_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/internal/dialhook"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

// answerOnce builds an entry over quirk and fields, serves h for host and
// POSTs once, so no retry runs.
func answerOnce(t *testing.T, ctx context.Context, h http.Handler, quirk func(*http.Response, []byte) error, fields ...formschema.Field) error {
	t.Helper()
	s := &scripted{}
	e := entry(s, []string{host}, fields...)
	e.Quirk = quirk
	values := ""
	if len(fields) > 0 {
		values = "api_key: k"
	}
	providertest.Build(t, e, values)
	resp, err := s.c.Do(request(t, providertest.Serve(t, ctx, h, host), http.MethodPost, "https://"+host+"/"))
	if err == nil {
		resp.Body.Close()
		t.Fatalf("status %d answered", resp.StatusCode)
	}
	return err
}

func TestFailureTable(t *testing.T) {
	cases := []struct {
		name       string
		code       int
		header     string
		keyed      bool
		kind       provider.FailureKind
		retryAfter time.Duration
	}{
		{"401 with a Secret", http.StatusUnauthorized, "", true, provider.Auth, 0},
		{"403 with a Secret", http.StatusForbidden, "", true, provider.Auth, 0},
		{"401 with no Secret", http.StatusUnauthorized, "", false, provider.Permanent, 0},
		{"403 with no Secret", http.StatusForbidden, "", false, provider.Permanent, 0},
		{"429 with Retry-After", http.StatusTooManyRequests, "7", false, provider.RateLimited, 7 * time.Second},
		{"429 without Retry-After", http.StatusTooManyRequests, "", false, provider.RateLimited, 0},
		{"404", http.StatusNotFound, "", false, provider.NotFound, 0},
		{"410", http.StatusGone, "", false, provider.NotFound, 0},
		{"408", http.StatusRequestTimeout, "", false, provider.Transient, 0},
		{"500", http.StatusInternalServerError, "", false, provider.Transient, 0},
		{"502", http.StatusBadGateway, "", false, provider.Transient, 0},
		{"503", http.StatusServiceUnavailable, "", false, provider.Transient, 0},
		{"504", http.StatusGatewayTimeout, "", false, provider.Transient, 0},
		{"507", http.StatusInsufficientStorage, "", false, provider.Transient, 0},
		{"599", 599, "", false, provider.Transient, 0},
		{"501", http.StatusNotImplemented, "", false, provider.Permanent, 0},
		{"505", http.StatusHTTPVersionNotSupported, "", false, provider.Permanent, 0},
		{"506", http.StatusVariantAlsoNegotiates, "", false, provider.Permanent, 0},
		{"508", http.StatusLoopDetected, "", false, provider.Permanent, 0},
		{"510", http.StatusNotExtended, "", false, provider.Permanent, 0},
		{"511", http.StatusNetworkAuthenticationRequired, "", false, provider.Permanent, 0},
		{"400", http.StatusBadRequest, "", false, provider.Permanent, 0},
		{"418", http.StatusTeapot, "", false, provider.Permanent, 0},
		{"300", http.StatusMultipleChoices, "", false, provider.Permanent, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if tc.header != "" {
					w.Header().Set("Retry-After", tc.header)
				}
				w.WriteHeader(tc.code)
			})
			var fields []formschema.Field
			if tc.keyed {
				fields = []formschema.Field{keyField}
			}
			f := failure(t, answerOnce(t, t.Context(), h, nil, fields...))
			if f.Kind != tc.kind || f.Status != tc.code || f.RetryAfter != tc.retryAfter || f.Host != host {
				t.Errorf("%+v, want %s, status %d, RetryAfter %v on %s", f, tc.kind, tc.code, tc.retryAfter, host)
			}
		})
	}
	t.Run("a transport error is Transient with its cause", func(t *testing.T) {
		s := &scripted{}
		providertest.Build(t, entry(s, []string{host}), "")
		ctx := dialhook.With(t.Context(), func(context.Context, string, string) (net.Conn, error) {
			return nil, &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}
		})
		_, err := send(s.c, request(t, ctx, http.MethodPost, "https://"+host+"/"))
		f := failure(t, err)
		if f.Kind != provider.Transient || f.Cause != httpclient.CauseRefused || f.Status != 0 || f.Host != host {
			t.Errorf("%+v, want Transient, refused, no status, on %s", f, host)
		}
	})
}

func TestQuirkFirst(t *testing.T) {
	big := strings.Repeat("b", 10<<10)
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusLocked)
		_, _ = w.Write([]byte(big))
	})
	t.Run("a quirk's failure, with the body capped at 4 KiB", func(t *testing.T) {
		var seen int
		quirk := func(r *http.Response, b []byte) error {
			seen = len(b)
			if r.StatusCode == http.StatusLocked {
				return provider.Fail(provider.RateLimited, "locked")
			}
			return nil
		}
		f := failure(t, answerOnce(t, t.Context(), h, quirk))
		if f.Kind != provider.RateLimited || f.Status != http.StatusLocked || f.Host != host || f.Msg != "locked" {
			t.Errorf("%+v, want the quirk's RateLimited with status and host filled", f)
		}
		if seen != 4<<10 {
			t.Errorf("quirk saw %d bytes, want 4096", seen)
		}
	})
	t.Run("a quirk answering nil leaves the table", func(t *testing.T) {
		f := failure(t, answerOnce(t, t.Context(), h, func(*http.Response, []byte) error { return nil }))
		if f.Kind != provider.Permanent || f.Status != http.StatusLocked {
			t.Errorf("%+v, want the table's Permanent", f)
		}
	})
	t.Run("a quirk's plain error is Permanent", func(t *testing.T) {
		f := failure(t, answerOnce(t, t.Context(), h, func(*http.Response, []byte) error { return errors.New("odd answer") }))
		if f.Kind != provider.Permanent || f.Msg != "odd answer" || f.Status != http.StatusLocked {
			t.Errorf("%+v, want Permanent odd answer", f)
		}
	})
}

func TestGETRetriesTransientOnly(t *testing.T) {
	for _, tc := range []struct {
		code     int
		kind     provider.FailureKind
		requests int64
	}{
		{http.StatusServiceUnavailable, provider.Transient, 3},
		{http.StatusTooManyRequests, provider.RateLimited, 1},
		{http.StatusNotFound, provider.NotFound, 1},
	} {
		t.Run(http.StatusText(tc.code), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				var n atomic.Int64
				ctx := providertest.Serve(t, t.Context(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					n.Add(1)
					w.WriteHeader(tc.code)
				}), host)
				for _, method := range []string{http.MethodGet, http.MethodHead} {
					n.Store(0)
					_, err := send(clientFor(t, host), request(t, ctx, method, "https://"+host+"/"))
					if f := failure(t, err); f.Kind != tc.kind || n.Load() != tc.requests {
						t.Errorf("%s: %+v after %d requests, want %s after %d", method, f, n.Load(), tc.kind, tc.requests)
					}
				}
			})
		})
	}
}

func TestFailureKindNames(t *testing.T) {
	want := map[provider.FailureKind]string{
		provider.Auth: "auth", provider.RateLimited: "rate_limited", provider.Transient: "transient",
		provider.Permanent: "permanent", provider.NotFound: "not_found", 0: "FailureKind(0)", 9: "FailureKind(9)",
	}
	for k, s := range want {
		if k.String() != s {
			t.Errorf("%d.String() = %q, want %q", k, k.String(), s)
		}
	}
	f := &provider.Failure{Kind: provider.NotFound, Host: host, Status: 404, Msg: "Not Found"}
	if got := f.Error(); got != "not_found "+host+" status 404: Not Found" {
		t.Errorf("Error() = %q", got)
	}
	if got := provider.Fail(provider.Permanent, "x").Error(); got != "permanent: x" {
		t.Errorf("Fail Error() = %q", got)
	}
}
