package arrapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClient_get_timeout_matrix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ctxFunc     func() (context.Context, context.CancelFunc)
		name        string
		defTimeout  time.Duration
		serverDelay time.Duration
		wantErr     bool
	}{
		{
			name: "no_deadline_applies_default_timeout",
			ctxFunc: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			defTimeout:  50 * time.Millisecond,
			serverDelay: 200 * time.Millisecond,
			wantErr:     true,
		},
		{
			name: "caller_deadline_shorter_than_default",
			ctxFunc: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 50*time.Millisecond)
			},
			defTimeout:  5 * time.Second,
			serverDelay: 200 * time.Millisecond,
			wantErr:     true,
		},
		{
			name: "caller_deadline_longer_than_default_not_overridden",
			ctxFunc: func() (context.Context, context.CancelFunc) {
				return context.WithTimeout(context.Background(), 2*time.Second)
			},
			defTimeout:  5 * time.Second,
			serverDelay: 10 * time.Millisecond,
			wantErr:     false,
		},
		{
			name: "cancelled_context_immediate_error",
			ctxFunc: func() (context.Context, context.CancelFunc) {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx, func() {}
			},
			defTimeout:  5 * time.Second,
			serverDelay: 10 * time.Millisecond,
			wantErr:     true,
		},
		{
			name: "no_deadline_fast_server_succeeds",
			ctxFunc: func() (context.Context, context.CancelFunc) {
				return context.WithCancel(context.Background())
			},
			defTimeout:  500 * time.Millisecond,
			serverDelay: 5 * time.Millisecond,
			wantErr:     false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(tc.serverDelay)
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			c := &Client{
				httpClient:     srv.Client(),
				baseURL:        srv.URL,
				apiKey:         "test-key",
				defaultTimeout: tc.defTimeout,
			}

			ctx, cancel := tc.ctxFunc()
			defer cancel()

			resp, err := c.get(ctx, "/test")
			if tc.wantErr {
				if err == nil {
					resp.Body.Close()
					t.Fatal("get() = nil error, want error")
				}
			} else {
				if err != nil {
					t.Fatalf("get() = %v, want nil", err)
				}
				resp.Body.Close()
			}
		})
	}
}

func TestStatusError_IsTransient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		code      int
		transient bool
	}{
		{code: 408, transient: false},
		{code: 429, transient: true},
		{code: 500, transient: true},
		{code: 502, transient: true},
		{code: 503, transient: true},
		{code: 504, transient: true},
		{code: 400, transient: false},
		{code: 401, transient: false},
		{code: 403, transient: false},
		{code: 404, transient: false},
		{code: 422, transient: false},
		{code: 200, transient: false},
	}

	for _, tc := range tests {
		se := newStatusError(tc.code, "/test", "body")
		got := se.IsTransient()
		if got != tc.transient {
			t.Errorf("StatusError{Code: %d}.IsTransient() = %v, want %v", tc.code, got, tc.transient)
		}
	}
}

func TestIdPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		base   string
		suffix string
		want   string
		id     int
	}{
		{name: "series_no_suffix", base: apiPrefix + "/series/", id: 42, suffix: "", want: apiPrefix + "/series/42"},
		{name: "episode_with_query_suffix", base: apiPrefix + "/episode/", id: 100, suffix: "?includeEpisodeFile=true", want: apiPrefix + "/episode/100?includeEpisodeFile=true"},
		{name: "empty_base", base: "", id: 7, suffix: "/x", want: "7/x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := idPath(tt.base, tt.id, tt.suffix); got != tt.want {
				t.Errorf("idPath(%q, %d, %q) = %q, want %q", tt.base, tt.id, tt.suffix, got, tt.want)
			}
		})
	}
}
