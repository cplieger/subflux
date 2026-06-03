package httputil

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/cplieger/httpx"

	"subflux/internal/api"
)

func TestCheckHTTPStatus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		statusCode int
		wantNil    bool
		wantAuth   bool
		wantRate   bool
		wantStatus bool
	}{
		{"200 OK", 200, true, false, false, false},
		{"201 Created", 201, true, false, false, false},
		{"401 Unauthorized", 401, false, true, false, false},
		{"403 Forbidden", 403, false, true, false, false},
		{"429 Too Many Requests", 429, false, false, true, false},
		{"500 Internal Server Error", 500, false, false, false, true},
		{"502 Bad Gateway", 502, false, false, false, true},
		{"503 Service Unavailable", 503, false, false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{StatusCode: tt.statusCode, Header: http.Header{}}
			err := CheckHTTPStatus(resp)
			if tt.wantNil {
				if err != nil {
					t.Errorf("CheckHTTPStatus(%d) = %v, want nil", tt.statusCode, err)
				}
				return
			}
			if err == nil {
				t.Fatalf("CheckHTTPStatus(%d) = nil, want error", tt.statusCode)
			}
			var authErr *api.AuthError
			var rateErr *api.RateLimitError
			var statusErr *HTTPStatusError
			if tt.wantAuth && !errors.As(err, &authErr) {
				t.Errorf("CheckHTTPStatus(%d) = %T, want *api.AuthError", tt.statusCode, err)
			}
			if tt.wantRate && !errors.As(err, &rateErr) {
				t.Errorf("CheckHTTPStatus(%d) = %T, want *api.RateLimitError", tt.statusCode, err)
			}
			if tt.wantStatus && !errors.As(err, &statusErr) {
				t.Errorf("CheckHTTPStatus(%d) = %T, want *HTTPStatusError", tt.statusCode, err)
			}
		})
	}
}

func TestCheckHTTPStatus_429_parses_retry_after(t *testing.T) {
	t.Parallel()
	h := http.Header{}
	h.Set("Retry-After", "30")
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: h}
	err := CheckHTTPStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *api.RateLimitError, got %T", err)
	}
	if rl.RetryAfter != 30*time.Second {
		t.Errorf("RetryAfter = %v, want 30s", rl.RetryAfter)
	}
}

func TestHTTPStatusError_preserves_wire_format(t *testing.T) {
	t.Parallel()
	got := (&HTTPStatusError{Code: 503}).Error()
	want := "HTTP 503"
	if got != want {
		t.Errorf("HTTPStatusError{503}.Error() = %q, want %q", got, want)
	}
}

func TestIsTransient_api_errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"api.AuthError not transient", &api.AuthError{Msg: "bad"}, false},
		{"api.RateLimitError not transient", &api.RateLimitError{Msg: "slow"}, false},
		{"httpx.HTTPStatusError 502 transient", &httpx.HTTPStatusError{Code: 502}, true},
		{"httpx.HTTPStatusError 400 not transient", &httpx.HTTPStatusError{Code: 400}, false},
		{"generic error not transient", errors.New("something"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsTransient(tt.err); got != tt.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsTransient_suffixBypassFixed(t *testing.T) {
	t.Parallel()
	// Verify httpx.HTTPStatusError (from httpx.CheckHTTPStatus) is correctly
	// classified as transient for 502/503/504 — this confirms the suffix-bypass
	// security fix is in effect since we now delegate to httpx.
	for _, code := range []int{502, 503, 504} {
		err := &httpx.HTTPStatusError{Code: code}
		if !IsTransient(err) {
			t.Errorf("IsTransient(HTTPStatusError{%d}) = false, want true", code)
		}
	}
}
