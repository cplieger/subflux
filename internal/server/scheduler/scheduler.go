// Package scheduler provides the periodic full-scan pipeline, DB maintenance,
// and auth cleanup scheduling for the subflux server.
package scheduler

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/cplieger/auth/v4"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/showskip"
)

// StartupDelay is the delay before the first scan after startup.
const StartupDelay = 30 * time.Second

// AuthCleanupInterval is how often expired sessions and stale auth state
// are purged from the database.
const AuthCleanupInterval = 15 * time.Minute

// Store is the two rows RunDBMaintenance touches: the reconcile pass and the
// aggregate counts it logs afterwards. Two of the 36 methods the store offers,
// which is why this is declared here and not taken as a wide type.
type Store interface {
	ReconcileState(ctx context.Context) (api.ReconcileResult, error)
	Stats(ctx context.Context) (downloads, attempts int, err error)
}

// ReconcileMetrics is the narrow observability interface for reconcile passes.
// The concrete *obs.Metrics satisfies this via structural typing.
type ReconcileMetrics interface {
	RecordReconcile(deleted int, reset int64, dur time.Duration)
}

// Deps holds all dependencies for the scheduler.
type Deps struct {
	DB Store
	// ScanDB is the scan-state surface the full scan needs (recency set,
	// stamps, cycle mark); the composition root passes the same store as DB.
	ScanDB scanning.ScanStore
	// Backoff feeds season-tracker earlyStop seeding; same store again.
	Backoff          scanning.BackoffPrefixReader
	Metrics          scanning.ScanMetrics
	ReconcileMetrics ReconcileMetrics // nil-safe; omit to skip reconcile metrics
	// Events, Activity and Alerts are the scan-engine surfaces; the
	// scheduler uses StartScan/PublishScanStart itself and hands the same
	// three straight to scanning.Deps, so they are scanning's interfaces
	// rather than duplicates. *events.EventBus, *activity.Log and
	// *activity.AlertLog satisfy them directly.
	Events   scanning.EventPublisher
	Activity scanning.ActivityTracker
	Alerts   scanning.AlertRecorder
	// RecordStoreWriteError escalates a failed store write to a persistent
	// operator alert when the error looks like disk exhaustion. Owned by the
	// composition root because classification needs the storage engine and
	// the same escalation serves the backup and poll-heartbeat writes.
	// Nil-safe; omit to skip the escalation.
	RecordStoreWriteError func(err error)
	// Stops registers the graceful stop callback of the running full scan;
	// scheduled scans register too (stoppable by admins).
	Stops               *activity.StopRegistry
	ShowSkipCache       *showskip.Cache
	StateFunc           func() *LiveState
	ScanningFlag        *atomic.Bool
	DeleteSubtitleFiles func(paths []string, source string)
}

// LiveState holds the live state needed by the scheduler.
//
// Cfg and Engine are both typed as scanning's own interfaces, for the same
// reason Deps.ScanDB is. The scheduler reads exactly ONE of the 37 values the
// config offers — Search(), for the scan interval and the upgrade flag it logs
// — and otherwise only carries the value into scanning.LiveState. A separate
// one-method declaration here would have to be assignable to scanning's
// four-method surface anyway, so it could only drift.
type LiveState struct {
	Cfg       scanning.ScanCfg
	Engine    scanning.ScanEngine
	Sonarr    api.SonarrClient
	Radarr    api.RadarrClient
	Providers []provider.Provider
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
	_, run := PrepareFullScan(deps, activity.SourceScheduled)
	run(ctx)
}

// FullScanAction and FullScanDetail are the activity strings every full
// library scan carries (manual and scheduled; the UI keys its last-scan
// timing row on the action string).
const (
	FullScanAction = "Full Scan"
	FullScanDetail = "Searching library for missing subtitles"
)

// PrepareFullScan starts the full-scan activity entry (with its structured
// scope and admin-only cancel role), publishes scan:start, and registers the
// graceful stop callback — the accept sequence, hoisted out of the scan body
// so the HTTP handler can return the activity id BEFORE the scan runs. The
// returned run func executes the scan and applies its terminal outcome; the
// caller owns the ScanningFlag guard and decides whether to run it inline
// (scheduler tick) or in a background goroutine (HTTP handler).
func PrepareFullScan(deps *Deps, source activity.ActivitySource) (actID string, run func(ctx context.Context)) {
	actID, _ = deps.Activity.StartScan(FullScanAction, FullScanDetail, source,
		activity.ScanScope{Kind: activity.ScanKindFull}, auth.RoleAdmin)
	deps.Events.PublishScanStart(&events.ScanEvent{
		Action: FullScanAction, Detail: FullScanDetail, Source: source, ActivityID: actID,
	})
	stopCh := make(chan struct{})
	unregister := deps.Stops.RegisterStop(actID, func() { close(stopCh) })
	run = func(ctx context.Context) {
		// Panic fallback only: FinishScanActivity releases the registration
		// explicitly BEFORE the terminal transition on every normal return
		// (idempotent), so a done entry never reports cancellable. The
		// defer covers a panicking scan body.
		defer unregister()
		outcome := runFullScan(ctx, stopCh, deps, actID)
		scanning.FinishScanActivity(unregister, deps.Activity, deps.Events,
			actID, FullScanAction, FullScanDetail, source, outcome)
	}
	return actID, run
}

// runFullScan assembles the scanning package's deps and executes the scan.
func runFullScan(ctx context.Context, stop <-chan struct{}, deps *Deps, actID string) activity.Outcome {
	ls := deps.StateFunc()
	if deps.ShowSkipCache != nil {
		deps.ShowSkipCache.Prune()
	}
	scanDeps := &scanning.Deps{
		DB:            deps.ScanDB,
		Backoff:       deps.Backoff,
		Metrics:       deps.Metrics,
		Events:        deps.Events,
		Activity:      deps.Activity,
		Alerts:        deps.Alerts,
		ShowSkipCache: deps.ShowSkipCache,
		ClearCaches:   provider.ClearCaches,
	}
	scanLS := &scanning.LiveState{
		Cfg:         ls.Cfg,
		Engine:      ls.Engine,
		Sonarr:      ls.Sonarr,
		Radarr:      ls.Radarr,
		Providers:   ls.Providers,
		ShowCounter: provider.ResolveShowCounter(ls.Providers),
	}
	return scanning.RunFullScan(ctx, stop, scanDeps, scanLS, actID)
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
		if deps.RecordStoreWriteError != nil {
			deps.RecordStoreWriteError(err)
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
