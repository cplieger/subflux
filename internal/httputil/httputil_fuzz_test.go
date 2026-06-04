package httputil

import (
	"errors"
	"net/http"
	"testing"

	"github.com/cplieger/httpx"

	"subflux/internal/api"
)

// FuzzIsTransient exercises IsTransient with synthesized errors across the
// full HTTP status code range, verifying classification invariants.
func FuzzIsTransient(f *testing.F) {
	f.Add(200, false, false)
	f.Add(400, false, false)
	f.Add(401, true, false)
	f.Add(429, false, true)
	f.Add(502, false, false)
	f.Add(503, false, false)
	f.Add(504, false, false)
	f.Add(500, false, false)
	f.Add(0, false, false)
	f.Add(599, false, false)

	f.Fuzz(func(t *testing.T, code int, isAuth, isRateLimit bool) {
		var err error

		switch {
		case isAuth:
			err = &api.AuthError{Msg: "test auth"}
		case isRateLimit:
			err = &api.RateLimitError{Msg: "test rate"}
		case code > 0:
			err = &httpx.HTTPStatusError{Code: code}
		default:
			err = errors.New("generic error")
		}

		result := IsTransient(err)

		// Invariant 1: AuthError is never transient.
		if isAuth && result {
			t.Fatal("AuthError must not be transient")
		}

		// Invariant 2: RateLimitError is never transient.
		if isRateLimit && result {
			t.Fatal("RateLimitError must not be transient")
		}

		// Invariant 3: nil is never transient.
		if IsTransient(nil) {
			t.Fatal("nil must not be transient")
		}

		// Invariant 4: 502/503/504 HTTP errors are transient.
		if !isAuth && !isRateLimit && (code == 502 || code == 503 || code == 504) {
			if !result {
				t.Fatalf("HTTPStatusError{%d} should be transient", code)
			}
		}
	})
}

// FuzzCheckHTTPStatus exercises CheckHTTPStatus across status codes,
// verifying that 2xx/3xx return nil and error types are correctly bridged.
func FuzzCheckHTTPStatus(f *testing.F) {
	f.Add(200)
	f.Add(201)
	f.Add(301)
	f.Add(400)
	f.Add(401)
	f.Add(403)
	f.Add(429)
	f.Add(500)
	f.Add(503)
	f.Add(100)
	f.Add(599)

	f.Fuzz(func(t *testing.T, code int) {
		if code < 100 || code > 599 {
			return // not valid HTTP status codes
		}

		resp := &http.Response{StatusCode: code, Header: http.Header{}}
		err := CheckHTTPStatus(resp)

		// Invariant 1: 1xx/2xx/3xx return nil.
		if code >= 100 && code < 400 {
			if err != nil {
				t.Fatalf("CheckHTTPStatus(%d) = %v, want nil", code, err)
			}
			return
		}

		// Invariant 2: 4xx/5xx return non-nil.
		if err == nil {
			t.Fatalf("CheckHTTPStatus(%d) = nil, want error", code)
		}

		// Invariant 3: 401/403 return *api.AuthError.
		if code == 401 || code == 403 {
			var authErr *api.AuthError
			if !errors.As(err, &authErr) {
				t.Fatalf("CheckHTTPStatus(%d) = %T, want *api.AuthError", code, err)
			}
		}

		// Invariant 4: 429 returns *api.RateLimitError.
		if code == 429 {
			var rlErr *api.RateLimitError
			if !errors.As(err, &rlErr) {
				t.Fatalf("CheckHTTPStatus(%d) = %T, want *api.RateLimitError", code, err)
			}
		}
	})
}
