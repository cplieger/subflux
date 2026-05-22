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
		name        string
		ctxFunc     func() (context.Context, context.CancelFunc)
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
		{408, false},
		{429, true},
		{500, true},
		{502, true},
		{503, true},
		{504, true},
		{400, false},
		{401, false},
		{403, false},
		{404, false},
		{422, false},
		{200, false},
	}

	for _, tc := range tests {
		se := newStatusError(tc.code, "/test", "body")
		got := se.IsTransient()
		if got != tc.transient {
			t.Errorf("StatusError{Code: %d}.IsTransient() = %v, want %v", tc.code, got, tc.transient)
		}
	}
}
