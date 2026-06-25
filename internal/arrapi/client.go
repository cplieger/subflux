// Package arrapi provides Sonarr and Radarr API clients.
package arrapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
	"golang.org/x/sync/singleflight"
)

// --- Client ---

// defaultRequestTimeout is the per-request timeout applied when the caller's
// context has no deadline. Covers large JSON responses from big libraries.
const defaultRequestTimeout = 120 * time.Second

// defaultMaxAttempts is the total number of attempts (including the first) for transient failures.
const defaultMaxAttempts = 3

// defaultRetryDelay is the base delay between retry attempts.
const defaultRetryDelay = 5 * time.Second

// safetyTimeout is the hard ceiling on any HTTP request. It exceeds
// defaultRequestTimeout by 25% so the per-request context deadline fires
// first in normal operation, but catches any code path that forgets to
// propagate a context deadline.
const safetyTimeout = 150 * time.Second

// apiPrefix is the versioned API path prefix for Sonarr/Radarr v3 APIs.
const apiPrefix = "/api/v3"

// defaultTransport returns a new pooled transport for arrapi clients.
func defaultTransport() *http.Transport {
	return &http.Transport{
		MaxIdleConns:        20,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
	}
}

// ClientOption configures optional Client parameters.
type ClientOption func(*Client)

// Client talks to a Sonarr or Radarr instance.
type Client struct {
	sfGroup        singleflight.Group
	httpClient     *http.Client
	baseURL        string
	apiKey         string
	maxAttempts    int
	retryDelay     time.Duration
	defaultTimeout time.Duration
}

// Compile-time assertion: *Client implements api.ArrClient.
var _ api.ArrClient = (*Client)(nil)

// NewClient creates an arr API client.
// Returns an error if baseURL or apiKey are invalid.
func NewClient(baseURL, apiKey string, opts ...ClientOption) (*Client, error) {
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("arrapi: baseURL must start with http:// or https://, got %s", baseURL)
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("arrapi: apiKey must not be empty")
	}
	c := &Client{
		httpClient:     &http.Client{Transport: defaultTransport(), Timeout: safetyTimeout},
		baseURL:        strings.TrimRight(baseURL, "/"),
		apiKey:         apiKey,
		maxAttempts:    defaultMaxAttempts,
		retryDelay:     defaultRetryDelay,
		defaultTimeout: defaultRequestTimeout,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.maxAttempts < 0 {
		c.maxAttempts = 0
	}
	if c.retryDelay < 100*time.Millisecond {
		c.retryDelay = 100 * time.Millisecond
	}
	return c, nil
}

// Ping tests connectivity to the arr instance by hitting the system/status endpoint.
// Returns nil on success, an error describing the failure otherwise.
// Uses a short timeout (5s) to fail fast during config validation.
func (c *Client) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(pingCtx, http.MethodGet,
		c.baseURL+apiPrefix+"/system/status", http.NoBody)
	if err != nil {
		return err
	}
	req.Header.Set(api.HeaderXAPIKey, c.apiKey)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	// Drain body to enable HTTP connection reuse (consistent with postCommand).
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, httputil.MaxErrorBodyBytes)); err != nil {
		slog.Debug("failed to drain ping response", "url", c.baseURL, "error", err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		return fmt.Errorf("%s returned 401: invalid API key", c.baseURL)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s returned %d", c.baseURL, resp.StatusCode)
	}
	return nil
}

// Close releases idle connections held by the client's transport.
// Safe to call multiple times.
func (c *Client) Close() {
	c.httpClient.CloseIdleConnections()
}
