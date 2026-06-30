package arrapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
)

func TestPostCommand(t *testing.T) {
	t.Parallel()

	t.Run("request_properties", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			check func(t *testing.T, r *http.Request, body []byte)
			name  string
		}{
			{
				name: "sets_content_type",
				check: func(t *testing.T, r *http.Request, _ []byte) {
					if got := r.Header.Get("Content-Type"); got != httputil.ContentTypeJSON {
						t.Errorf("Content-Type = %q, want %q", got, httputil.ContentTypeJSON)
					}
				},
			},
			{
				name: "posts_to_command_endpoint",
				check: func(t *testing.T, r *http.Request, _ []byte) {
					if r.URL.Path != apiPrefix+"/command" {
						t.Errorf("path = %q, want %q", r.URL.Path, apiPrefix+"/command")
					}
					if r.Method != http.MethodPost {
						t.Errorf("method = %q, want %q", r.Method, http.MethodPost)
					}
				},
			},
			{
				name: "sends_api_key_header",
				check: func(t *testing.T, r *http.Request, _ []byte) {
					if got := r.Header.Get(api.HeaderXAPIKey); got != "test-key" {
						t.Errorf("X-Api-Key = %q, want %q", got, "test-key")
					}
				},
			},
			{
				name: "merges_name_into_body",
				check: func(t *testing.T, _ *http.Request, body []byte) {
					var m map[string]any
					if err := json.Unmarshal(body, &m); err != nil {
						t.Fatalf("unmarshal body: %v", err)
					}
					if m["name"] != "RescanSeries" {
						t.Errorf("body.name = %v, want %q", m["name"], "RescanSeries")
					}
					if id, ok := m["seriesId"].(float64); !ok || int(id) != 7 {
						t.Errorf("body.seriesId = %v, want 7", m["seriesId"])
					}
				},
			},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				var capturedReq *http.Request
				var capturedBody []byte
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					capturedReq = r.Clone(r.Context())
					capturedBody, _ = readBody(r)
					w.WriteHeader(http.StatusOK)
				}))
				defer srv.Close()

				c := newTestClient(t, srv)
				err := c.postCommand(context.Background(), commandBody{Name: "RescanSeries", SeriesID: 7})
				if err != nil {
					t.Fatalf("postCommand() unexpected error: %v", err)
				}
				tt.check(t, capturedReq, capturedBody)
			})
		}
	})

	t.Run("status_and_error_handling", func(t *testing.T) {
		t.Parallel()
		tests := []struct {
			name        string
			body        string
			errContains string
			status      int
			wantErr     bool
		}{
			{name: "status_200_succeeds", status: http.StatusOK, body: "", wantErr: false, errContains: ""},
			{name: "status_399_succeeds", status: 399, body: "", wantErr: false, errContains: ""},
			{name: "status_400_returns_error", status: http.StatusBadRequest, body: "bad request body", wantErr: true, errContains: "400"},
			{name: "error_body_included_in_message", status: http.StatusInternalServerError, body: "detailed error info", wantErr: true, errContains: "detailed error info"},
			{name: "empty_error_body_returns_status", status: http.StatusForbidden, body: "", wantErr: true, errContains: "403"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tt.status)
					if tt.body != "" {
						_, _ = w.Write([]byte(tt.body))
					}
				}))
				defer srv.Close()

				c := newTestClient(t, srv)
				err := c.postCommand(context.Background(), commandBody{Name: CommandRescanSeries})

				if tt.wantErr && err == nil {
					t.Fatal("expected error, got nil")
				}
				if !tt.wantErr && err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err, tt.errContains)
				}
			})
		}
	})

	t.Run("invalid_url_returns_error", func(t *testing.T) {
		t.Parallel()
		c := &Client{
			httpClient: &http.Client{Timeout: time.Second},
			baseURL:    "http://[::1]:namedport",
			apiKey:     "test-key",
		}
		err := c.postCommand(context.Background(), commandBody{Name: CommandRescanSeries})
		if err == nil {
			t.Fatal("expected error for invalid URL")
		}
		if !strings.Contains(err.Error(), "build command request") {
			t.Errorf("error = %q, want containing %q", err, "build command request")
		}
	})

	t.Run("cancelled_context", func(t *testing.T) {
		t.Parallel()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		c := newTestClient(t, srv)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		err := c.postCommand(ctx, commandBody{Name: "RescanSeries"})
		if err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})
}

func readBody(r *http.Request) ([]byte, error) {
	defer r.Body.Close()
	return io.ReadAll(r.Body)
}

// TestPostCommand_silent_drain_on_success asserts the body-drain step stays
// silent when draining a successful command response. The drain-failure log is
// guarded by `err != nil`; negating that guard would emit a spurious "failed
// to drain command response" on every successful command, which this detects.
func TestPostCommand_silent_drain_on_success(t *testing.T) {
	// Non-parallel: captureLogs swaps the global slog default.
	h := captureLogs(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	if err := c.postCommand(context.Background(), commandBody{Name: CommandRescanSeries}); err != nil {
		t.Fatalf("postCommand() unexpected error: %v", err)
	}
	if _, ok := h.find("failed to drain command response"); ok {
		t.Error("postCommand() logged a drain failure on a successful drain")
	}
}
