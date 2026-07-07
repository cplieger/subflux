package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/server/authhandlers"
)

// applyTrustedProxies pushes the live config's parsed trusted-proxy CIDR set
// into the shared client-IP resolver (authhandlers.ClientIP), so every
// client-IP site — audit log, login rate limiter, session IPAddress, admin
// bootstrap, and the access log — resolves the real client behind a reverse
// proxy. Called at serve start and on every hot reload so the set re-parses
// and takes effect without a restart. A nil or non-*config.Config live config
// yields an empty set: the trust-nothing default (socket peer, XFF ignored).
func (s *Server) applyTrustedProxies() {
	var nets []*net.IPNet
	if cfg, ok := s.state().cfg.(*config.Config); ok {
		nets = cfg.TrustedProxyNets()
	}
	authhandlers.SetTrustedProxies(nets)
}

// hotReload swaps the live config and reinitializes providers and engine.
func (s *Server) hotReload(ctx context.Context, newCfg api.ConfigProvider) error {
	s.reloadMu.Lock()
	defer s.reloadMu.Unlock()

	slog.Debug("hotReload: starting",
		"providers", len(newCfg.ProviderConfigs()),
		"sonarr", newCfg.SonarrConfig().URL != "",
		"radarr", newCfg.RadarrConfig().URL != "")

	// Detect config drift before swapping.
	// Skip when transitioning from unconfigured (no old config to compare).
	oldCfg := s.state().cfg
	var drift api.ConfigDrift
	if oldCfg != nil {
		drift = api.DetectDrift(
			oldCfg.LanguageCodes(), newCfg.LanguageCodes(),
			enabledProviders(oldCfg), enabledProviders(newCfg),
			oldCfg.Adaptive().Enabled, newCfg.Adaptive().Enabled,
		)
	}

	engine, sc, providers, err := s.wire(ctx, newCfg, s.db, s.metrics)
	if err != nil {
		return fmt.Errorf("wire: %w", err)
	}

	// Apply drift cleanup after successful wire so that a wire failure
	// doesn't leave the DB in an inconsistent state (attempts cleared
	// but old config still active, causing unexpected re-searches).
	if !drift.Empty() {
		slog.Debug("config drift detected",
			"removed_languages", drift.RemovedLanguages,
			"removed_providers", drift.RemovedProviders,
			"adaptive_disabled", drift.AdaptiveDisabled)
		if err := s.db.CleanupDrift(ctx, drift); err != nil {
			slog.Warn("config drift cleanup failed", "error", err)
		}
	}

	var sonarrClient api.ArrClient
	if sonarrCfg := newCfg.SonarrConfig(); sonarrCfg.URL != "" {
		c, err := s.newArrClient(sonarrCfg.URL, sonarrCfg.APIKey)
		if err != nil {
			return fmt.Errorf("invalid sonarr config: %w", err)
		}
		sonarrClient = c
	}
	var radarrClient api.ArrClient
	if radarrCfg := newCfg.RadarrConfig(); radarrCfg.URL != "" {
		c, err := s.newArrClient(radarrCfg.URL, radarrCfg.APIKey)
		if err != nil {
			return fmt.Errorf("invalid radarr config: %w", err)
		}
		radarrClient = c
	}

	s.live.Store(&liveState{
		cfg:       newCfg,
		providers: providers,
		scorer:    sc,
		engine:    engine,
		sonarr:    sonarrClient,
		radarr:    radarrClient,
	})

	// Re-parse the trusted-proxy set from the new config so client-IP
	// resolution reflects the hot-reloaded value without a restart.
	s.applyTrustedProxies()

	// Mark as configured. This is the transition point when the server
	// starts in unconfigured mode and the user saves a valid config.
	wasUnconfigured := !s.configured.Load()
	s.configured.Store(true)
	s.metrics.SetConfigured(true)

	slog.Info("hot reload complete",
		"providers", len(providers),
		"languages", newCfg.LanguageCodes())

	// Log config changes for debugging.
	if oldCfg != nil {
		slog.Info("hot reload: config changes",
			"old_providers", len(enabledProviders(oldCfg)),
			"new_providers", len(providers),
			"old_languages", oldCfg.LanguageCodes(),
			"new_languages", newCfg.LanguageCodes())
	}

	// Clear startup and config alerts on successful reload.
	s.alerts.DismissBySource("startup")
	s.alerts.DismissBySource("config")

	// If transitioning from unconfigured to configured, start the
	// scheduler and poller that were skipped during StartUnconfigured.
	if wasUnconfigured {
		slog.Info("configuration activated; starting scheduler, poller, and backup")
		s.bgWg.Add(4)
		go func() { defer s.bgWg.Done(); s.runScheduler(s.ctx) }()
		go func() { defer s.bgWg.Done(); s.runPoller(s.ctx) }()
		go func() { defer s.bgWg.Done(); s.runBackup(s.ctx) }()
		go func() { defer s.bgWg.Done(); s.runStoreMetrics(s.ctx) }()
	}

	return nil
}
