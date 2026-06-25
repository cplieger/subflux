package arrapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// --- Test helpers ---

// newTestClient creates a Client pointing at the given test server.
func newTestClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return &Client{
		httpClient:  srv.Client(),
		baseURL:     srv.URL,
		apiKey:      "test-key",
		maxAttempts: 3,
		retryDelay:  time.Millisecond, // Fast retries for tests.
	}
}

// errSentinel is a simple error type for testing error propagation.
type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// --- NewClient ---

func TestNewClient_validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		url     string
		apiKey  string
		wantErr string // empty = success
		wantURL string // checked only when wantErr == ""
	}{
		{name: "valid", url: "http://localhost:8989", apiKey: "abc123", wantURL: "http://localhost:8989"},
		{name: "invalid_scheme", url: "ftp://invalid", apiKey: "key", wantErr: "must start with http"},
		{name: "empty_apiKey", url: "http://localhost:8989", apiKey: "", wantErr: "apiKey must not be empty"},
		{name: "whitespace_apiKey", url: "http://localhost:8989", apiKey: "   ", wantErr: "apiKey must not be empty"},
		{name: "strips_trailing_slash", url: "http://localhost:8989/", apiKey: "abc123", wantURL: "http://localhost:8989"},
		{name: "accepts_https", url: "https://sonarr.example.com", apiKey: "key123", wantURL: "https://sonarr.example.com"},
		{name: "strips_multiple_trailing_slashes", url: "http://localhost:8989///", apiKey: "abc123", wantURL: "http://localhost:8989"},
		{name: "preserves_path_component", url: "http://localhost:8989/sonarr/", apiKey: "abc123", wantURL: "http://localhost:8989/sonarr"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, err := NewClient(tt.url, tt.apiKey)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("NewClient() = nil error, want error containing %q", tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewClient() error = %q, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewClient() unexpected error: %v", err)
			}
			if c.baseURL != tt.wantURL {
				t.Errorf("baseURL = %q, want %q", c.baseURL, tt.wantURL)
			}
		})
	}
}

// --- RefreshSeries / RefreshMovie ---

func TestRefreshSeries_sends_correct_command(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.RefreshSeries(context.Background(), 99)
	if err != nil {
		t.Fatalf("RefreshSeries() unexpected error: %v", err)
	}
	if gotBody["name"] != "RescanSeries" {
		t.Errorf("RefreshSeries() command name = %v, want %q", gotBody["name"], "RescanSeries")
	}
	// seriesId is float64 after JSON round-trip.
	if id, ok := gotBody["seriesId"].(float64); !ok || int(id) != 99 {
		t.Errorf("RefreshSeries() seriesId = %v, want 99", gotBody["seriesId"])
	}
}

func TestRefreshMovie_sends_correct_command(t *testing.T) {
	t.Parallel()

	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("failed to decode body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := newTestClient(t, srv)
	err := c.RefreshMovie(context.Background(), 42)
	if err != nil {
		t.Fatalf("RefreshMovie() unexpected error: %v", err)
	}
	if gotBody["name"] != "RescanMovie" {
		t.Errorf("RefreshMovie() command name = %v, want %q", gotBody["name"], "RescanMovie")
	}
	if id, ok := gotBody["movieId"].(float64); !ok || int(id) != 42 {
		t.Errorf("RefreshMovie() movieId = %v, want 42", gotBody["movieId"])
	}
}

// --- Ping ---

func TestPing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		setup       func(t *testing.T) (*Client, context.Context)
		name        string
		errContains string
		wantErr     bool
	}{
		{
			name: "success",
			setup: func(t *testing.T) (*Client, context.Context) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != apiPrefix+"/system/status" {
						t.Errorf("Ping() path = %q, want %q", r.URL.Path, apiPrefix+"/system/status")
					}
					if r.Header.Get(api.HeaderXAPIKey) != "test-key" {
						t.Errorf("Ping() X-Api-Key = %q, want %q", r.Header.Get(api.HeaderXAPIKey), "test-key")
					}
					w.WriteHeader(http.StatusOK)
				}))
				t.Cleanup(srv.Close)
				return newTestClient(t, srv), context.Background()
			},
			wantErr: false,
		},
		{
			name: "unauthorized",
			setup: func(t *testing.T) (*Client, context.Context) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusUnauthorized)
				}))
				t.Cleanup(srv.Close)
				return newTestClient(t, srv), context.Background()
			},
			wantErr:     true,
			errContains: "invalid API key",
		},
		{
			name: "server_error",
			setup: func(t *testing.T) (*Client, context.Context) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusInternalServerError)
				}))
				t.Cleanup(srv.Close)
				return newTestClient(t, srv), context.Background()
			},
			wantErr:     true,
			errContains: "500",
		},
		{
			name: "connection_refused",
			setup: func(t *testing.T) (*Client, context.Context) {
				c := &Client{
					httpClient: &http.Client{Timeout: time.Second},
					baseURL:    "http://127.0.0.1:1",
					apiKey:     "test-key",
				}
				return c, context.Background()
			},
			wantErr:     true,
			errContains: "cannot reach",
		},
		{
			name: "cancelled_context",
			setup: func(t *testing.T) (*Client, context.Context) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusOK)
				}))
				t.Cleanup(srv.Close)
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return newTestClient(t, srv), ctx
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c, ctx := tt.setup(t)
			err := c.Ping(ctx)

			if tt.wantErr {
				if err == nil {
					t.Fatal("Ping() = nil, want error")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("Ping() error = %q, want containing %q", err, tt.errContains)
				}
			} else {
				if err != nil {
					t.Errorf("Ping() = %v, want nil", err)
				}
			}
		})
	}
}
