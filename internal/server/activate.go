package server

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cplieger/auth/v2"
	authoidc "github.com/cplieger/auth/v2/oidc"
	authwebauthn "github.com/cplieger/auth/v2/webauthn"
	"github.com/cplieger/subflux/internal/api"
	"github.com/go-webauthn/webauthn/webauthn"
)

// This file owns the SINGLE activation path: activate() is the one operation
// that turns a validated config into live capabilities, shared by configured
// cold boot (Start) and unconfigured→configured hot activation / config save
// (hotReload). Its capability inventory is complete — engine/scorer/providers,
// arr clients, WebAuthn assembly, the OIDC lazy slot, logging reapplication,
// trusted proxies, session timeouts, the SSE client cap, configured metrics,
// alerts and drift cleanup, outgoing arr-client closure, and the background
// worker latch — so no second activation path can hide elsewhere.

// activationMode selects the per-capability failure policy for activate.
type activationMode int

const (
	// activateCold is boot-time activation of the on-disk config. WebAuthn
	// construction failure DEGRADES (warn + nil + persistent alert): a config
	// that boots today must keep booting, never silently dropping the app
	// into settings mode over an auth capability.
	activateCold activationMode = iota
	// activateHot is runtime activation of a candidate config (config save).
	// WebAuthn construction failure is FATAL to the save: a bad RP ID is
	// rejected loudly at save time instead of being persisted broken.
	activateHot
)

// String labels the mode for logs.
func (m activationMode) String() string {
	if m == activateCold {
		return "cold"
	}
	return "hot"
}

// activationCandidate is the full config-derived capability set built by the
// prepare phase. Nothing in it is externally visible until publish stores it
// in the live snapshot.
type activationCandidate struct {
	engine    api.SearchEngine
	scorer    api.Scorer
	sonarr    api.SonarrClient
	radarr    api.RadarrClient
	webauthn  *webauthn.WebAuthn
	oidc      *oidcSlot
	providers []api.Provider
	drift     api.ConfigDrift
	// webauthnDegraded records the cold-boot degrade path so finalize keeps
	// the persistent alert instead of dismissing it.
	webauthnDegraded bool
}

// activate is the single server-owned activation operation (R1.1). It runs
// three honest phases:
//
//   - prepare: fallible construction of the FULL candidate (engine, scorer,
//     providers, arr clients, WebAuthn, OIDC slot). No externally visible
//     mutation; any failure leaves the previous snapshot serving.
//   - publish: the one atomic.Pointer[liveState] store.
//   - finalize: non-failing best-effort committed effects — logging swap,
//     configured metrics, alerts, drift cleanup, old arr-client closure,
//     trusted proxies, session timeouts, SSE cap, and the worker latch.
//
// Process-global effects (logging) and post-publish cleanup are NOT inside
// the pointer's atomicity, and a post-publish persistence failure can leave
// memory ahead of disk (the config-save handler documents that case).
// Repeated activation of an identical config is harmless: the worker latch
// absorbs duplicates and the snapshot swap is idempotent in effect.
func (s *Server) activate(ctx context.Context, newCfg api.ConfigProvider, mode activationMode) error {
	oldState := s.state()

	cand, err := s.prepare(ctx, newCfg, oldState.cfg, mode)
	if err != nil {
		return err
	}

	// Publish: the single existing atomic snapshot store.
	s.live.Store(&liveState{
		cfg:       newCfg,
		providers: cand.providers,
		scorer:    cand.scorer,
		engine:    cand.engine,
		sonarr:    cand.sonarr,
		radarr:    cand.radarr,
		webauthn:  cand.webauthn,
		oidc:      cand.oidc,
	})

	s.finalize(ctx, oldState, newCfg, cand, mode)
	return nil
}

// prepare builds the full activation candidate. Fallible; performs no
// externally visible mutation, so a failure rejects the candidate config
// while the previous snapshot keeps serving.
func (s *Server) prepare(ctx context.Context, newCfg, oldCfg api.ConfigProvider, mode activationMode) (*activationCandidate, error) {
	cand := &activationCandidate{}

	// Detect config drift against the outgoing config before anything is
	// swapped. Skipped when transitioning from unconfigured (no old config).
	if oldCfg != nil {
		cand.drift = api.DetectDrift(
			oldCfg.LanguageCodes(), newCfg.LanguageCodes(),
			enabledProviders(oldCfg), enabledProviders(newCfg),
			oldCfg.Adaptive().Enabled, newCfg.Adaptive().Enabled,
		)
	}

	engine, sc, providers, err := s.wire(ctx, newCfg, s.db, s.metrics)
	if err != nil {
		return nil, fmt.Errorf("wire: %w", err)
	}
	cand.engine, cand.scorer, cand.providers = engine, sc, providers

	// Arr clients are constructed HERE in both modes: activation owns their
	// lifecycle (main.go builds none), so a failed construction rejects the
	// candidate without side effects.
	if sonarrCfg := newCfg.SonarrConfig(); sonarrCfg.URL != "" {
		c, sonarrErr := s.newSonarr(sonarrCfg.URL, sonarrCfg.APIKey)
		if sonarrErr != nil {
			return nil, fmt.Errorf("invalid sonarr config: %w", sonarrErr)
		}
		cand.sonarr = c
	}
	if radarrCfg := newCfg.RadarrConfig(); radarrCfg.URL != "" {
		c, radarrErr := s.newRadarr(radarrCfg.URL, radarrCfg.APIKey)
		if radarrErr != nil {
			return nil, fmt.Errorf("invalid radarr config: %w", radarrErr)
		}
		cand.radarr = c
	}

	// WebAuthn assembly with the explicit per-mode failure policy (R1.7).
	cand.webauthn, cand.webauthnDegraded, err = s.buildWebAuthn(newCfg, mode)
	if err != nil {
		return nil, err
	}

	// OIDC: an immutable per-config lazy slot. Static shape was validated by
	// config validation in the load path; network discovery stays lazy at
	// request time (getOIDC). A new config gets a FRESH slot so a stale
	// cached provider can never outlive an issuer edit; disabled = nil slot.
	cand.oidc = newOIDCSlot(newCfg)

	return cand, nil
}

// buildWebAuthn constructs the WebAuthn instance for the candidate config.
// An empty RP ID means WebAuthn is not configured (nil, no error, no alert).
// Construction failure is FATAL on a hot save and DEGRADED (warn + nil +
// persistent alert) on cold boot.
func (s *Server) buildWebAuthn(cfg api.ConfigProvider, mode activationMode) (wa *webauthn.WebAuthn, degraded bool, err error) {
	rpID := cfg.WebAuthnRPID()
	if rpID == "" {
		return nil, false, nil
	}
	wa, err = authwebauthn.NewWebAuthn(rpID, "Subflux", []string{"https://" + rpID})
	if err == nil {
		return wa, false, nil
	}
	if mode == activateHot {
		return nil, false, fmt.Errorf("webauthn: invalid RP ID %q: %w", rpID, err)
	}
	slog.Warn("WebAuthn initialization failed; passkeys unavailable", "rp_id", rpID, "error", err)
	s.alerts.RecordPersistent("webauthn",
		"WebAuthn initialization failed for RP ID "+rpID+": "+err.Error()+
			". Passkey login is unavailable until auth.webauthn_rp_id is fixed.")
	return nil, true, nil
}

// finalize applies the non-failing, best-effort committed effects after the
// snapshot is published. Nothing here may fail the activation.
func (s *Server) finalize(ctx context.Context, oldState *liveState, newCfg api.ConfigProvider, cand *activationCandidate, mode activationMode) {
	// Re-run the process-global logging setup when the logging section
	// changed (R1.5). Uniform handler re-setup; cold boot re-applies the
	// same values main already installed, which is a no-op by comparison.
	s.applyLogging(oldState.cfg, newCfg)

	// Drift cleanup (clear stale search attempts for removed languages /
	// providers / disabled adaptive). Deliberately non-fatal: stale attempts
	// age out; the failure is only logged.
	if !cand.drift.Empty() {
		slog.Debug("config drift detected",
			"removed_languages", cand.drift.RemovedLanguages,
			"removed_providers", cand.drift.RemovedProviders,
			"adaptive_disabled", cand.drift.AdaptiveDisabled)
		if err := s.db.CleanupDrift(ctx, cand.drift); err != nil {
			slog.Warn("config drift cleanup failed", "error", err)
		}
	}

	// Release the outgoing arr clients' transports now that the new state is
	// published. Safe against concurrent users of the old snapshot: arrapi's
	// Close only calls (*http.Client).CloseIdleConnections, which never
	// interrupts in-flight requests; Close is idempotent.
	closeArrClient(oldState.sonarr)
	closeArrClient(oldState.radarr)

	// Re-parse the trusted-proxy set from the new config so client-IP
	// resolution reflects the activated value without a restart.
	s.applyTrustedProxies()

	// Push the new session timeouts into the auth-store sweeper so eviction
	// uses the same cutoffs the request-path validator now enforces (the
	// validator reads live config; a stale sweeper would hard-logout
	// sessions early). Optional-capability pattern like backupStore.
	if ts, ok := s.authStore.(interface {
		SetSessionTimeouts(idle, absolute time.Duration)
	}); ok {
		ts.SetSessionTimeouts(newCfg.SessionIdleTimeout(), newCfg.SessionAbsoluteTimeout())
	}

	// Re-apply the SSE client cap on the running hub (admission-time
	// enforced; existing streams above a lowered cap drain naturally).
	s.events.SetMaxClients(sseClientCap(newCfg))

	// Configured-mode flip + gauge.
	s.configured.Store(true)
	s.metrics.SetConfigured(true)

	// Clear startup and config alerts; the webauthn alert clears unless this
	// very activation degraded (cold-boot WebAuthn failure keeps it).
	s.alerts.DismissBySource("startup")
	s.alerts.DismissBySource("config")
	if !cand.webauthnDegraded {
		s.alerts.DismissBySource("webauthn")
	}

	slog.Info("configuration activated",
		"mode", mode.String(),
		"providers", len(cand.providers),
		"languages", newCfg.LanguageCodes(),
		"sonarr", cand.sonarr != nil,
		"radarr", cand.radarr != nil,
		"webauthn", cand.webauthn != nil,
		"oidc", cand.oidc != nil)

	// Background-worker latch: launch the worker set at most once per
	// process, invoked after EVERY successful activation (R1.6). The latch
	// absorbs re-activation duplicates (repeated identical PUTs are legal).
	s.startWorkers()
}

// applyLogging re-runs the injected process-global logging setup when the
// logging section differs between the outgoing and incoming configs (R1.5).
func (s *Server) applyLogging(oldCfg, newCfg api.ConfigProvider) {
	if s.logSetup == nil {
		return
	}
	level, format := string(newCfg.LoggingLevel()), string(newCfg.LoggingFormat())
	if oldCfg != nil &&
		string(oldCfg.LoggingLevel()) == level &&
		string(oldCfg.LoggingFormat()) == format {
		return
	}
	s.logSetup(level, format)
	// Logged after the swap so the line lands on the new handler.
	slog.Info("logging reconfigured", "level", level, "format", format)
}

// --- Background-worker latch ---

// startWorkers launches the background worker set (scheduler, poller, backup,
// store metrics) AT MOST ONCE per process. It replaces the former duplicated
// launch blocks in Start and hotReload and their broken "iff wasUnconfigured"
// guard (WithConfig stores configured=true at construction, so a configured
// cold boot computed false and launched nothing).
func (s *Server) startWorkers() {
	s.workersOnce.Do(func() {
		launch := s.launchWorkers
		if launch == nil {
			launch = s.launchWorkerSet
		}
		launch()
	})
}

// launchWorkerSet starts the real background goroutines on the server
// context. Reconcile is a scheduler-internal stage, not a worker.
func (s *Server) launchWorkerSet() {
	slog.Info("starting background workers", "workers", []string{"scheduler", "poller", "backup", "store_metrics"})
	s.bgWg.Add(4)
	go func() { defer s.bgWg.Done(); s.runScheduler(s.ctx) }()
	go func() { defer s.bgWg.Done(); s.runPoller(s.ctx) }()
	go func() { defer s.bgWg.Done(); s.runBackup(s.ctx) }()
	go func() { defer s.bgWg.Done(); s.runStoreMetrics(s.ctx) }()
}

// --- OIDC lazy slot ---

// oidcRetryBackoff is the minimum interval between OIDC discovery attempts.
const oidcRetryBackoff = 30 * time.Second

// oidcSlot is the immutable per-config lazy OIDC cell carried by the live
// snapshot: the config it was built from, the (lazily discovered) provider,
// and the retry timestamp live together, so replacing the config replaces
// the WHOLE cell. That makes stale-provider reuse impossible: an issuer edit
// activates a fresh slot whose next resolution re-discovers, where a
// server-global cached provider was served forever.
type oidcSlot struct {
	provider    atomic.Pointer[authoidc.Provider]
	cfg         auth.OIDCConfig
	mu          sync.Mutex
	lastAttempt atomic.Int64 // unix nanos of the last discovery attempt
}

// newOIDCSlot builds the slot for a config, or nil when OIDC is disabled
// (publishing a nil slot is how "disable OIDC" takes effect immediately).
func newOIDCSlot(cfg api.ConfigProvider) *oidcSlot {
	if !cfg.OIDCEnabled() {
		return nil
	}
	return &oidcSlot{cfg: cfg.OIDCConfig()}
}

// get returns the slot's provider, performing lazy network discovery on
// first use with a retry backoff. The backoff fast-path runs outside the
// mutex so request paths never queue behind a hanging discovery.
func (sl *oidcSlot) get(ctx context.Context) *authoidc.Provider {
	if p := sl.provider.Load(); p != nil {
		return p
	}
	if last := sl.lastAttempt.Load(); last != 0 && time.Since(time.Unix(0, last)) < oidcRetryBackoff {
		return nil
	}

	sl.mu.Lock()
	defer sl.mu.Unlock()

	if p := sl.provider.Load(); p != nil {
		return p
	}
	if last := sl.lastAttempt.Load(); last != 0 && time.Since(time.Unix(0, last)) < oidcRetryBackoff {
		return nil
	}

	sl.lastAttempt.Store(time.Now().UnixNano())
	p, err := authoidc.NewProvider(ctx, sl.cfg)
	if err != nil {
		slog.Error("lazy OIDC provider initialization failed (will retry after backoff)",
			"error", err, "retry_after", oidcRetryBackoff)
		return nil
	}
	sl.provider.Store(p)
	return p
}
