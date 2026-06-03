package opensubtitles

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"subflux/internal/httputil"
	"github.com/cplieger/ssrf"
)

const (
	settingUsername = "username"
	hostOpenSubs   = "opensubtitles.com"
	langPtBR       = "pt-BR"
)

// --- Authentication ---

// ensureToken refreshes the API token if expired or missing. Uses singleflight
// to deduplicate concurrent login attempts (replaces the previous thundering
// herd pattern where multiple goroutines could login simultaneously).
func (p *Provider) ensureToken(ctx context.Context) error {
	p.tokenMu.RLock()
	if p.token != "" && time.Since(p.tokenTime) < tokenExpiry {
		p.tokenMu.RUnlock()
		return nil
	}
	p.tokenMu.RUnlock()

	_, err, _ := p.tokenSfg.Do("login", func() (any, error) {
		// Double-check after winning the singleflight race.
		p.tokenMu.RLock()
		if p.token != "" && time.Since(p.tokenTime) < tokenExpiry {
			p.tokenMu.RUnlock()
			return nil, nil
		}
		p.tokenMu.RUnlock()

		slog.Debug("opensubtitles logging in")
		loginPayload, marshalErr := json.Marshal(map[string]string{
			settingUsername: p.username, string(settingPassword): p.password,
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal login: %w", marshalErr)
		}
		body, err := p.doPostUnauthed(ctx, "/login", bytes.NewReader(loginPayload))
		if err != nil {
			slog.Warn("opensubtitles login failed", "error", err)
			return nil, fmt.Errorf("login: %w", err)
		}
		defer func() { httputil.DrainClose(body) }()

		var resp loginResponse
		if err := json.NewDecoder(body).Decode(&resp); err != nil {
			slog.Warn("opensubtitles login response decode failed", "error", err)
			return nil, fmt.Errorf("decode login: %w", err)
		}

		if resp.Token == "" {
			return nil, errors.New("empty token in login response")
		}

		if resp.BaseURL != "" && !isValidServerHost(resp.BaseURL) {
			slog.Warn("opensubtitles: rejecting suspicious server redirect",
				"base_url", resp.BaseURL)
			resp.BaseURL = ""
		}

		p.tokenMu.Lock()
		p.token = resp.Token
		p.serverHost = resp.BaseURL
		p.vip = resp.User.VIP
		p.tokenTime = time.Now()
		host := p.serverHost
		vip := p.vip
		p.tokenMu.Unlock()

		slog.Info("opensubtitles authenticated", "server", host, "vip", vip)
		return nil, nil
	})
	return err
}

func (p *Provider) serverURL() string {
	p.tokenMu.RLock()
	host := p.serverHost
	p.tokenMu.RUnlock()
	if host != "" {
		return "https://" + host + "/api/v1"
	}
	return baseURL
}

// isValidServerHost validates that a base_url from the login response is a
// clean hostname in the opensubtitles.com domain family. Rejects hosts
// containing path separators, port suffixes, userinfo (@), or other
// URL-special characters that could redirect authenticated requests to
// unintended destinations. Requires the host to pass SSRF public-host
// checks and to be either opensubtitles.com or a subdomain thereof so a
// compromised login response can't redirect our Bearer token to an
// attacker-controlled host.
func isValidServerHost(host string) bool {
	if strings.ContainsAny(host, "/:@?#") {
		return false
	}
	if !ssrf.IsPublicHost(host) {
		return false
	}
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	return h == hostOpenSubs || strings.HasSuffix(h, ".opensubtitles.com")
}

// --- Rate Limiting ---

// rateLimit enforces the per-account request rate (VIP: 5/s, free: 1/s)
// using a channel-based token bucket. Concurrent callers block on the channel
// until a token is available, then schedule a refill after the rate-limit
// interval. This allows overlapping response parsing with the next request's
// rate-limit wait (unlike the previous mutex pattern which held the lock
// during the sleep, blocking all work).
func (p *Provider) rateLimit(ctx context.Context) error {
	// Wait for a token (blocks if another request is in-flight within the interval).
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.rateCh:
	}

	// Determine the refill interval based on VIP status.
	p.tokenMu.RLock()
	vip := p.vip
	p.tokenMu.RUnlock()

	limit := freeRateLimit
	if vip {
		limit = vipRateLimit
	}

	// Schedule a token refill after the rate-limit interval.
	// time.AfterFunc uses a runtime timer instead of a parked goroutine,
	// avoiding unbounded goroutine accumulation under burst load.
	time.AfterFunc(limit, func() {
		select {
		case p.rateCh <- struct{}{}:
		default:
			// Channel already has a token; discard to avoid overfilling.
		}
	})

	return nil
}
