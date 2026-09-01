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

	"github.com/cplieger/httpx/v5"
	"github.com/cplieger/ssrf/v4"
)

const (
	settingUsername = "username"
	hostOpenSubs    = "opensubtitles.com"
	langPtBR        = "pt-BR"
)

// --- Authentication ---

// ensureToken refreshes the API token if expired or missing, using
// singleflight to deduplicate concurrent login attempts.
func (p *Provider) ensureToken(ctx context.Context) error {
	if p.tokenValid() {
		return nil
	}

	_, err, _ := p.tokenSfg.Do("login", func() (any, error) {
		// Re-check after winning the race: another goroutine may have
		// already logged in.
		if p.tokenValid() {
			return nil, nil
		}
		return nil, p.login(ctx)
	})
	return err
}

// tokenValid reports whether a cached, unexpired token is present.
func (p *Provider) tokenValid() bool {
	p.tokenMu.RLock()
	defer p.tokenMu.RUnlock()
	return p.token != "" && time.Since(p.tokenTime) < tokenExpiry
}

// login performs the unauthenticated /login round trip and stores the
// returned token, server host, and VIP status. A suspicious base_url
// redirect is dropped so a compromised response can't divert the token.
func (p *Provider) login(ctx context.Context) error {
	slog.Debug("opensubtitles logging in")
	loginPayload, marshalErr := json.Marshal(map[string]string{
		settingUsername: p.username, string(settingPassword): p.password,
	})
	if marshalErr != nil {
		return fmt.Errorf("marshal login: %w", marshalErr)
	}
	body, err := p.doPostUnauthed(ctx, "/login", bytes.NewReader(loginPayload))
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer func() { httpx.DrainClose(body) }()

	var resp loginResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return fmt.Errorf("decode login: %w", err)
	}

	if resp.Token == "" {
		return errors.New("empty token in login response")
	}

	if resp.BaseURL != "" && !isValidServerHost(resp.BaseURL.Raw()) {
		slog.Warn("opensubtitles: rejecting suspicious server redirect",
			"base_url", resp.BaseURL)
		resp.BaseURL = ""
	}

	p.tokenMu.Lock()
	p.token = resp.Token
	p.serverHost = resp.BaseURL.Raw()
	p.vip = resp.User.VIP
	p.tokenTime = time.Now()
	host := p.serverHost
	vip := p.vip
	p.tokenMu.Unlock()

	slog.Info("opensubtitles authenticated", "server", host, "vip", vip)
	return nil
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
// clean opensubtitles.com hostname, rejecting path separators, ports,
// userinfo, and any host that isn't opensubtitles.com or a subdomain — so a
// compromised login response can't redirect our Bearer token elsewhere.
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

// rateLimit enforces the per-account request rate (VIP: 5/s, free: 1/s) with
// a channel-based token bucket, letting response parsing overlap the next
// request's rate-limit wait instead of holding a lock during the sleep.
func (p *Provider) rateLimit(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-p.rateCh:
	}

	p.tokenMu.RLock()
	vip := p.vip
	p.tokenMu.RUnlock()

	limit := freeRateLimit
	if vip {
		limit = vipRateLimit
	}

	// time.AfterFunc uses a runtime timer, avoiding unbounded goroutine
	// accumulation under burst load.
	time.AfterFunc(limit, func() {
		select {
		case p.rateCh <- struct{}{}:
		default:
		}
	})

	return nil
}
