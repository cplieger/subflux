// Package scheduler provides the periodic full-scan pipeline, DB maintenance,
// and auth cleanup scheduling for the subflux server.
package scheduler

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/httputil"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/serveradapter"
	"github.com/cplieger/subflux/internal/server/showskip"
)

// StartupDelay is the delay before the first scan after startup.
const StartupDelay = 30 * time.Second

// AuthCleanupInterval is how often expired sessions and stale auth state
// are purged from the database.
const AuthCleanupInterval = 15 * time.Minute

// OIDCStateTTL is how long an OIDC authorization flow can remain pending
// before garbage collection.
const OIDCStateTTL = 10 * time.Minute

// Store is the narrow store interface used by RunDBMaintenance.
type Store interface {
	ReconcileState(ctx context.Context) (api.ReconcileResult, error)
	Stats(ctx context.Context) (downloads, attempts int, err error)
}

// Compile-time assertion: api.Store satisfies Store.
var _ Store = api.Store(nil)

// ReconcileMetrics is the narrow observability interface for reconcile passes.
// The concrete *metrics.Metrics satisfies this via structural typing.
type ReconcileMetrics interface {
	RecordReconcile(deleted int, reset int64, dur time.Duration)
}

// Deps holds all dependencies for the scheduler.
type Deps struct {
	DB                  api.Store
	Metrics             scanning.ScanMetrics
	ReconcileMetrics    ReconcileMetrics // nil-safe; omit to skip reconcile metrics
	Events              *serveradapter.ScanEventAdapter
	Activity            *serveradapter.ActivityAdapter
	Alerts              *serveradapter.AlertAdapter
	ShowSkipCache       *showskip.Cache
	StateFunc           func() *LiveState
	ScanningFlag        *atomic.Bool
	DeleteSubtitleFiles func(paths []string, source string)
}

// LiveState holds the live state needed by the scheduler.
type LiveState struct {
	Cfg       api.ConfigProvider
	Engine    api.SearchEngine
	Sonarr    api.SonarrClient
	Radarr    api.RadarrClient
	Providers []api.Provider
}

// Run runs the periodic scan and DB maintenance tickers until ctx is cancelled.
func Run(ctx context.Context, deps *Deps) {
	ls := deps.StateFunc()
	scanInterval := ls.Cfg.Search().ScanInterval
	slog.Info("scheduler started",
		"scan_interval", scanInterval.String(),
		"upgrade_enabled", ls.Cfg.Search().UpgradeEnabled)

	startDelay := time.NewTimer(StartupDelay)
	defer startDelay.Stop()
	select {
	case <-startDelay.C:
	case <-ctx.Done():
		return
	}

	RunDBMaintenance(ctx, deps)
	if ctx.Err() != nil {
		return
	}
	GuardedScan(ctx, deps)

	scanTimer := time.NewTimer(scanInterval)
	defer scanTimer.Stop()

	for {
		select {
		case <-scanTimer.C:
			RunDBMaintenance(ctx, deps)
			if ctx.Err() != nil {
				return
			}
			GuardedScan(ctx, deps)
			nextInterval := deps.StateFunc().Cfg.Search().ScanInterval
			scanTimer.Reset(nextInterval)
			slog.Info("next scheduled scan", "in", nextInterval.String())
		case <-ctx.Done():
			return
		}
	}
}

// GuardedScan acquires the scanning flag before running a full scan.
func GuardedScan(ctx context.Context, deps *Deps) {
	if !deps.ScanningFlag.CompareAndSwap(false, true) {
		slog.Debug("scheduler: scan skipped, already in progress")
		return
	}
	defer deps.ScanningFlag.Store(false)
	RunFullScan(ctx, deps)
}

// RunFullScan delegates to the scanning package's RunFullScan.
func RunFullScan(ctx context.Context, deps *Deps) {
	ls := deps.StateFunc()
	if deps.ShowSkipCache != nil {
		deps.ShowSkipCache.Prune()
	}
	scanDeps := &scanning.Deps{
		DB:            deps.DB,
		Metrics:       deps.Metrics,
		Events:        deps.Events,
		Activity:      deps.Activity,
		Alerts:        deps.Alerts,
		ShowSkipCache: deps.ShowSkipCache,
		SleepCtx:      httputil.SleepCtx,
		ClearCaches:   provider.ClearProviderCaches,
	}
	scanLS := &scanning.LiveState{
		Cfg:         ls.Cfg,
		Engine:      ls.Engine,
		Sonarr:      ls.Sonarr,
		Radarr:      ls.Radarr,
		Providers:   ls.Providers,
		ShowCounter: provider.ResolveShowCounter(ls.Providers),
	}
	scanning.RunFullScan(ctx, scanDeps, scanLS)
}

// RunDBMaintenance prunes old state and stale search attempts.
func RunDBMaintenance(ctx context.Context, deps *Deps) {
	start := time.Now()
	slog.Debug("db maintenance starting")
	result, err := deps.DB.ReconcileState(ctx)
	if err != nil {
		slog.Warn("db maintenance: reconcile failed", "error", err)
		// Surface a persistent alert on disk-full or repeated write failure
		// so operators are notified before the system crash-loops.
		if deps.Alerts != nil {
			deps.Alerts.RecordStoreWriteError(err)
		}
	} else if len(result.Deleted.Paths) > 0 || result.ResetCount > 0 {
		slog.Info("db maintenance: reconciled stale entries",
			"deleted", len(result.Deleted.Paths), "reset", result.ResetCount,
			"duration", time.Since(start).Round(time.Millisecond).String())
	}

	// Record reconcile metrics (nil-safe).
	if deps.ReconcileMetrics != nil {
		deps.ReconcileMetrics.RecordReconcile(len(result.Deleted.Paths), result.ResetCount, time.Since(start))
	}

	deps.DeleteSubtitleFiles(result.Deleted.Paths, "reconcile")

	downloads, attempts, err := deps.DB.Stats(ctx)
	if err != nil {
		slog.Warn("db maintenance: stats query failed", "error", err)
	}
	slog.Debug("db maintenance complete",
		"downloads", downloads, "attempts", attempts,
		"duration", time.Since(start).Round(time.Millisecond).String())
}
