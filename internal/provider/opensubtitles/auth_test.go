package opensubtitles

import (
	"context"
	"errors"
	"testing"
	"time"
)

// --- Server host validation ---

func TestIsValidServerHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		host string
		want bool
	}{
		{name: "valid hostname", host: "vip-api.opensubtitles.com", want: true},
		{name: "valid subdomain", host: "api.opensubtitles.com", want: true},
		{name: "apex domain accepted", host: "opensubtitles.com", want: true},
		{name: "trailing dot tolerated", host: "api.opensubtitles.com.", want: true},
		{name: "mixed case accepted", host: "API.OpenSubtitles.com", want: true},
		{name: "unrelated public host rejected", host: "example.com", want: false},
		{name: "lookalike suffix rejected", host: "evil-opensubtitles.com", want: false},
		{name: "path injection", host: "evil.com/steal-creds", want: false},
		{name: "port injection", host: "evil.com:8080", want: false},
		{name: "userinfo injection", host: "user@evil.com", want: false},
		{name: "query injection", host: "evil.com?redirect=true", want: false},
		{name: "fragment injection", host: "evil.com#frag", want: false},
		{name: "bare hostname", host: "localhost", want: false},
		{name: "private IP", host: "192.168.1.1", want: false},
		{name: "loopback IP", host: "127.0.0.1", want: false},
		{name: "empty string", host: "", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := isValidServerHost(tt.host)
			if got != tt.want {
				t.Errorf("isValidServerHost(%q) = %v, want %v",
					tt.host, got, tt.want)
			}
		})
	}
}

func TestServerURL(t *testing.T) {
	t.Parallel()

	t.Run("default when no server host set", func(t *testing.T) {
		t.Parallel()
		p := &Provider{}
		got := p.serverURL()
		if got != baseURL {
			t.Errorf("serverURL() = %q, want %q", got, baseURL)
		}
	})

	t.Run("custom server host", func(t *testing.T) {
		t.Parallel()
		p := &Provider{serverHost: "vip-api.opensubtitles.com"}
		got := p.serverURL()
		want := "https://vip-api.opensubtitles.com/api/v1"
		if got != want {
			t.Errorf("serverURL() = %q, want %q", got, want)
		}
	})

	t.Run("empty server host uses default", func(t *testing.T) {
		t.Parallel()
		p := &Provider{serverHost: ""}
		got := p.serverURL()
		if got != baseURL {
			t.Errorf("serverURL() = %q, want %q", got, baseURL)
		}
	})
}

// --- Rate Limiting ---

func TestRateLimit_no_wait_when_token_available(t *testing.T) {
	t.Parallel()

	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{} // pre-fill token
	p := &Provider{vip: false, rateCh: rateCh}

	start := time.Now()
	err := p.rateLimit(context.Background())
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("rateLimit() unexpected error: %v", err)
	}
	if elapsed > 50*time.Millisecond {
		t.Errorf("rateLimit() elapsed = %v, want < 50ms when token available", elapsed)
	}
}

func TestRateLimit_blocks_when_no_token_available(t *testing.T) {
	t.Parallel()

	rateCh := make(chan struct{}, 1)
	// Don't pre-fill — no token available, so rateLimit blocks.
	p := &Provider{vip: false, rateCh: rateCh}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := p.rateLimit(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("rateLimit() error = %v, want context.DeadlineExceeded", err)
	}
	if elapsed < 50*time.Millisecond || elapsed > 300*time.Millisecond {
		t.Errorf("rateLimit() cancelled at %v, want ~100ms", elapsed)
	}
}

func TestRateLimit_refills_token_after_interval(t *testing.T) {
	t.Parallel()

	rateCh := make(chan struct{}, 1)
	rateCh <- struct{}{}                      // pre-fill
	p := &Provider{vip: true, rateCh: rateCh} // VIP = 200ms refill

	// Consume the token.
	if err := p.rateLimit(context.Background()); err != nil {
		t.Fatalf("first rateLimit() unexpected error: %v", err)
	}

	// Wait for refill (VIP = 200ms, give 400ms budget).
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()

	start := time.Now()
	err := p.rateLimit(ctx)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("second rateLimit() unexpected error: %v (elapsed=%v)", err, elapsed)
	}
	if elapsed < 150*time.Millisecond || elapsed > 350*time.Millisecond {
		t.Errorf("rateLimit() VIP refill elapsed = %v, want ~200ms", elapsed)
	}
}

func TestRateLimit_respects_context_cancellation(t *testing.T) {
	t.Parallel()

	rateCh := make(chan struct{}, 1)
	// No token — will block until context cancelled.
	p := &Provider{vip: false, rateCh: rateCh}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	err := p.rateLimit(ctx)
	elapsed := time.Since(start)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("rateLimit() error = %v, want context.Canceled", err)
	}
	if elapsed > 200*time.Millisecond {
		t.Errorf("rateLimit() returned at %v, want ~50ms (cancelled early)", elapsed)
	}
}
