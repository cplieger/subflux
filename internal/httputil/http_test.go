package httputil

import (
	"errors"
	"net/http"
	"testing"
	"time"

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
		wantStatus bool // expect *HTTPStatusError
	}{
		{"200 OK", 200, true, false, false, false},
		{"201 Created", 201, true, false, false, false},
		{"400 Bad Request", 400, false, false, false, false},
		{"401 Unauthorized", 401, false, true, false, false},
		{"403 Forbidden", 403, false, true, false, false},
		{"429 Too Many Requests", 429, false, false, true, false},
		{"500 Internal Server Error", 500, false, false, false, true},
		{"502 Bad Gateway", 502, false, false, false, true},
		{"503 Service Unavailable", 503, false, false, false, true},
		{"504 Gateway Timeout", 504, false, false, false, true},
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
			if tt.wantAuth {
				if !errors.As(err, &authErr) {
					t.Errorf("CheckHTTPStatus(%d) = %T, want *api.AuthError", tt.statusCode, err)
				}
			}
			if tt.wantRate {
				if !errors.As(err, &rateErr) {
					t.Errorf("CheckHTTPStatus(%d) = %T, want *api.RateLimitError", tt.statusCode, err)
				}
			}
			if tt.wantStatus {
				if !errors.As(err, &statusErr) {
					t.Errorf("CheckHTTPStatus(%d) = %T, want *HTTPStatusError", tt.statusCode, err)
				} else if statusErr.Code != tt.statusCode {
					t.Errorf("HTTPStatusError.Code = %d, want %d", statusErr.Code, tt.statusCode)
				}
			}
		})
	}
}

// TestHTTPStatusError_preserves_wire_format asserts that the typed error
// stringifies to "HTTP <code>" so providers that still match on the error
// message (or tests that do) keep working during the migration.
func TestHTTPStatusError_preserves_wire_format(t *testing.T) {
	t.Parallel()
	got := (&HTTPStatusError{Code: 503}).Error()
	want := "HTTP 503"
	if got != want {
		t.Errorf("HTTPStatusError{503}.Error() = %q, want %q", got, want)
	}
}

// TestCheckHTTPStatus_429_parses_retry_after verifies Retry-After is
// decoded from both delta-seconds and HTTP-date forms.
func TestCheckHTTPStatus_429_parses_retry_after(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		header      string
		wantAtLeast time.Duration
		wantAtMost  time.Duration
	}{
		{"no header", "", 0, 0},
		{"delta-seconds", "30", 30 * time.Second, 30 * time.Second},
		{"zero seconds", "0", 0, 0},
		{"negative seconds clamped", "-10", 0, 0},
		{"invalid string ignored", "soon", 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			h := http.Header{}
			if tt.header != "" {
				h.Set("Retry-After", tt.header)
			}
			resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: h}
			err := CheckHTTPStatus(resp)
			var rl *api.RateLimitError
			if !errors.As(err, &rl) {
				t.Fatalf("expected *api.RateLimitError, got %T", err)
			}
			if rl.RetryAfter < tt.wantAtLeast || rl.RetryAfter > tt.wantAtMost {
				t.Errorf("RetryAfter = %v, want [%v, %v]",
					rl.RetryAfter, tt.wantAtLeast, tt.wantAtMost)
			}
		})
	}
}

// TestCheckHTTPStatus_429_parses_http_date isolates the HTTP-date path
// because the range is dynamic (time.Until).
func TestCheckHTTPStatus_429_parses_http_date(t *testing.T) {
	t.Parallel()
	future := time.Now().Add(45 * time.Second).UTC().Format(http.TimeFormat)
	h := http.Header{}
	h.Set("Retry-After", future)
	resp := &http.Response{StatusCode: http.StatusTooManyRequests, Header: h}
	err := CheckHTTPStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("expected *api.RateLimitError, got %T", err)
	}
	// Parsed time has 1-second precision; allow a wide tolerance
	// for slow test runners.
	if rl.RetryAfter < 30*time.Second || rl.RetryAfter > 60*time.Second {
		t.Errorf("RetryAfter = %v, want ~45s", rl.RetryAfter)
	}
}
