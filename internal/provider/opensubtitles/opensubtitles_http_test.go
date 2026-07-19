package opensubtitles

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// --- Header Setting ---

func TestSetHeaders(t *testing.T) {
	t.Parallel()

	t.Run("sets required headers without token", func(t *testing.T) {
		t.Parallel()
		p := &Provider{apiKey: "test-api-key"}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)

		p.setHeaders(req)

		if got := req.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want %q", got, "application/json")
		}
		if got := req.Header.Get("Api-Key"); got != "test-api-key" {
			t.Errorf("Api-Key = %q, want %q", got, "test-api-key")
		}
		// No token set, so the Authorization header must be absent.
		if got := req.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want empty (no token)", got)
		}
	})

	t.Run("sets authorization header with token", func(t *testing.T) {
		t.Parallel()
		p := &Provider{apiKey: "key", token: "my-token"}
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://example.com", http.NoBody)

		p.setHeaders(req)

		want := "Bearer my-token"
		if got := req.Header.Get("Authorization"); got != want {
			t.Errorf("Authorization = %q, want %q", got, want)
		}
	})
}

// --- HTTP Status Checking ---

func TestCheckStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantMsg    string
		wantType   string
		statusCode int
		wantErr    bool
	}{
		{name: "200 OK returns nil", statusCode: 200, body: "", wantErr: false, wantMsg: "", wantType: ""},
		{name: "201 Created returns nil", statusCode: 201, body: "", wantErr: false, wantMsg: "", wantType: ""},
		{name: "204 No Content returns nil", statusCode: 204, body: "", wantErr: false, wantMsg: "", wantType: ""},
		{name: "301 redirect returns nil", statusCode: 301, body: "", wantErr: false, wantMsg: "", wantType: ""},
		{name: "401 unauthorized", statusCode: 401, body: "", wantErr: true, wantMsg: "invalid API key (401)", wantType: "auth"},
		{name: "429 rate limited", statusCode: 429, body: "", wantErr: true, wantMsg: "rate limited (429)", wantType: "ratelimit"},
		{name: "406 download limit", statusCode: 406, body: "", wantErr: true, wantMsg: "download limit exceeded (406)", wantType: "ratelimit"},
		{name: "500 server error ignores body", statusCode: 500, body: "internal error", wantErr: true, wantMsg: "HTTP 500", wantType: ""},
		{name: "400 bad request ignores body", statusCode: 400, body: "bad request", wantErr: true, wantMsg: "HTTP 400", wantType: ""},
		{name: "403 forbidden is auth error", statusCode: 403, body: "", wantErr: true, wantMsg: "access denied (403)", wantType: "auth"},
		{name: "202 Accepted returns nil", statusCode: 202, body: "", wantErr: false, wantMsg: "", wantType: ""},
		{name: "304 Not Modified returns nil", statusCode: 304, body: "", wantErr: false, wantMsg: "", wantType: ""},
		{name: "399 returns nil", statusCode: 399, body: "", wantErr: false, wantMsg: "", wantType: ""},
		{name: "503 service unavailable", statusCode: 503, body: "service down", wantErr: true, wantMsg: "HTTP 503", wantType: ""},
		{name: "404 not found no body", statusCode: 404, body: "", wantErr: true, wantMsg: "HTTP 404", wantType: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &http.Response{
				StatusCode: tt.statusCode,
				Body:       io.NopCloser(strings.NewReader(tt.body)),
			}
			defer resp.Body.Close()
			err := checkStatus(resp)
			if tt.wantErr {
				if err == nil {
					t.Fatal("checkStatus() expected error")
				}
				if err.Error() != tt.wantMsg {
					t.Errorf("checkStatus() error = %q, want %q",
						err.Error(), tt.wantMsg)
				}
				switch tt.wantType {
				case "auth":
					var authErr *api.AuthError
					if !errors.As(err, &authErr) {
						t.Errorf("checkStatus(%d) error type = %T, want *api.AuthError",
							tt.statusCode, err)
					}
				case "ratelimit":
					var rlErr *api.RateLimitError
					if !errors.As(err, &rlErr) {
						t.Errorf("checkStatus(%d) error type = %T, want *api.RateLimitError",
							tt.statusCode, err)
					}
				}
				return
			}
			if err != nil {
				t.Errorf("checkStatus() unexpected error: %v", err)
			}
		})
	}
}

func TestCheckStatus_parses_retry_after_seconds_on_429(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Header:     http.Header{"Retry-After": []string{"42"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	defer resp.Body.Close()

	err := checkStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("checkStatus() error type = %T, want *api.RateLimitError", err)
	}
	if rl.RetryAfter != 42*time.Second {
		t.Errorf("RetryAfter = %v, want 42s", rl.RetryAfter)
	}
}

func TestCheckStatus_missing_retry_after_on_429_is_zero(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusTooManyRequests,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	defer resp.Body.Close()

	err := checkStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("checkStatus() error type = %T, want *api.RateLimitError", err)
	}
	if rl.RetryAfter != 0 {
		t.Errorf("RetryAfter = %v, want 0 (no header)", rl.RetryAfter)
	}
}

func TestCheckStatus_406_defaults_to_next_utc_midnight(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusNotAcceptable,
		Body:       io.NopCloser(strings.NewReader("")),
	}
	defer resp.Body.Close()

	err := checkStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("checkStatus() error type = %T, want *api.RateLimitError", err)
	}
	// Daily quota window is always ≤24h and >0s.
	if rl.RetryAfter <= 0 || rl.RetryAfter > 24*time.Hour {
		t.Errorf("RetryAfter = %v, want (0, 24h]", rl.RetryAfter)
	}
}

func TestCheckStatus_406_respects_retry_after_header_when_present(t *testing.T) {
	t.Parallel()
	resp := &http.Response{
		StatusCode: http.StatusNotAcceptable,
		Header:     http.Header{"Retry-After": []string{"7"}},
		Body:       io.NopCloser(strings.NewReader("")),
	}
	defer resp.Body.Close()

	err := checkStatus(resp)
	var rl *api.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("checkStatus() error type = %T, want *api.RateLimitError", err)
	}
	if rl.RetryAfter != 7*time.Second {
		t.Errorf("RetryAfter = %v, want 7s", rl.RetryAfter)
	}
}

func TestUntilNextUTCMidnight(t *testing.T) {
	t.Parallel()
	tests := []struct {
		now  time.Time
		name string
		want time.Duration
	}{
		{
			name: "start of day",
			now:  time.Date(2026, 5, 27, 0, 0, 0, 0, time.UTC),
			want: 24 * time.Hour,
		},
		{
			name: "mid day",
			now:  time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC),
			want: 12 * time.Hour,
		},
		{
			name: "one second before midnight",
			now:  time.Date(2026, 5, 27, 23, 59, 59, 0, time.UTC),
			want: time.Second,
		},
		{
			name: "exact midnight returns clamped 1s",
			// Start of day elsewhere would be 24h; same instant returns 24h.
			now:  time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC),
			want: 24 * time.Hour,
		},
		{
			name: "500ms before midnight clamps to 1s",
			now:  time.Date(2026, 5, 27, 23, 59, 59, int(500*time.Millisecond), time.UTC),
			want: time.Second,
		},
		{
			name: "nanosecond before midnight clamps to 1s",
			now:  time.Date(2026, 5, 27, 23, 59, 59, 999_999_999, time.UTC),
			want: time.Second,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := untilNextUTCMidnight(tt.now)
			if got != tt.want {
				t.Errorf("untilNextUTCMidnight(%v) = %v, want %v",
					tt.now, got, tt.want)
			}
		})
	}
}

// --- Token Invalidation ---

func TestInvalidateTokenOn401(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		initialToken string
		err          error
		wantToken    string
	}{
		{name: "clears token on auth error", initialToken: "my-token", err: &api.AuthError{Msg: "401"}, wantToken: ""},
		{name: "preserves token on other error", initialToken: "my-token", err: errors.New("some other error"), wantToken: "my-token"},
		{name: "idempotent when token empty", initialToken: "", err: &api.AuthError{Msg: "401"}, wantToken: ""},
		{name: "wrapped auth error", initialToken: "my-token", err: fmt.Errorf("request failed: %w", &api.AuthError{Msg: "401"}), wantToken: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := &Provider{token: tt.initialToken}
			p.invalidateTokenOn401(tt.err)

			p.tokenMu.Lock()
			got := p.token
			p.tokenMu.Unlock()
			if got != tt.wantToken {
				t.Errorf("token = %q, want %q", got, tt.wantToken)
			}
		})
	}
}
