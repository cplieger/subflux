package server

import (
	"context"
	"log/slog"
	"net"
	"slices"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/authhandlers"
	"github.com/cplieger/webhttp"
)

// enabledProviders returns the sorted names of enabled providers in a config,
// used by activation's drift detection.
func enabledProviders(cfg interface {
	ProviderConfigs() map[api.ProviderID]api.ProviderCfg
},
) []api.ProviderID {
	var names []api.ProviderID
	for name, pcfg := range cfg.ProviderConfigs() {
		if pcfg.Enabled {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b api.ProviderID) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	return names
}

// applyHostAllowlist re-wraps the middleware chain with the live config's
// exact-match Host allowlist (webhttp.HostPolicy) and swaps it in atomically,
// so an allowed_hosts edit takes effect on the next request without a
// restart. Called by buildHandler once the inner chain exists and by
// activation's finalize phase on every reload; before buildHandler has
// assembled the chain it is a no-op (activation at cold boot runs first).
// A nil or non-*config.Config live config yields a nil policy: the inactive
// pass-through default (any Host accepted).
func (s *Server) applyHostAllowlist() {
	if s.hostGateInner == nil {
		return // cold-boot activation before buildHandler; it re-applies
	}
	var policy *webhttp.HostPolicy
	if ls := s.state(); ls != nil {
		if cfg, ok := ls.cfg.(*config.Config); ok {
			policy = cfg.HostAllowlist()
		}
	}
	if !policy.Active() {
		slog.Warn("allowed_hosts not configured: any Host header is accepted, leaving DNS rebinding open; " +
			"set allowed_hosts in the server settings to engage the gate")
	}
	h := policy.Middleware()(s.hostGateInner)
	s.hostGated.Store(&h)
}

// applyTrustedProxies pushes the live config's parsed trusted-proxy CIDR set
// into the shared client-IP resolver (authhandlers.ClientIP), so every
// client-IP site — audit log, login rate limiter, session IPAddress, admin
// bootstrap, and the access log — resolves the real client behind a reverse
// proxy. Called at serve start and by activation's finalize phase so the set
// re-parses and takes effect without a restart. A nil or non-*config.Config
// live config yields an empty set: the trust-nothing default (socket peer,
// XFF ignored).
func (s *Server) applyTrustedProxies() {
	var nets []*net.IPNet
	if cfg, ok := s.state().cfg.(*config.Config); ok {
		nets = cfg.TrustedProxyNets()
	}
	authhandlers.SetTrustedProxies(nets)
}

// hotReload applies a candidate config at runtime: the config-save handlers
// call it after parse/validate/arr-ping, before persisting. It is a thin
// serialized wrapper over the single activation operation (activate.go), so
// hot reload and cold boot cannot drift — the capability inventory lives in
// one place. Failure contract: activation's prepare phase rejects the
// candidate with no externally visible mutation, the previous snapshot keeps
// serving, and the save handler surfaces config_reload_failed.
//
// Locking layers: reloadMu is the INNER guard — no two activations ever run
// concurrently, whoever the caller is. The confighandlers save-transaction
// lock (Handler.saveMu) is the OUTER guard serializing the complete save
// (secret merge, canonicalization, arr pings, this call, and the file
// write), so activation order always matches persist order. Both are needed:
// reloadMu alone allowed two saves to interleave publish-A, publish-B,
// write-B, write-A. Lock order is always saveMu → reloadMu.
func (s *Server) hotReload(ctx context.Context, newCfg api.ConfigProvider) error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	slog.Debug("hot reload: activating candidate config",
		"providers", len(newCfg.ProviderConfigs()),
		"sonarr", newCfg.SonarrConfig().URL != "",
		"radarr", newCfg.RadarrConfig().URL != "")

	return s.activate(ctx, newCfg, activateHot)
}

// closeArrClient closes an outgoing arr client replaced by activation, when
// the concrete type exposes Close (the api.SonarrClient / api.RadarrClient
// interfaces deliberately don't; test doubles have no resources to release).
// A nil interface value is a no-op.
func closeArrClient(c any) {
	if closer, ok := c.(interface{ Close() }); ok {
		closer.Close()
	}
}
