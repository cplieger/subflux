// Package scheduler provides the periodic full-scan pipeline, DB maintenance,
// and auth cleanup scheduling for the subflux server.
package scheduler

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/cplieger/auth/v6"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/showskip"
	"github.com/cplieger/subflux/internal/subflux"
)

// StartupDelay is the delay before the first scan after startup.
const StartupDelay = 30 * time.Second

// AuthCleanupInterval is how often expired sessions and stale auth state
// are purged from the database.
const AuthCleanupInterval = 15 * time.Minute

// MediaRetryInterval replaces scan_interval after a pass an unwritable media
// folder refused or stopped.
const MediaRetryInterval = 15 * time.Minute

// Store is the two rows RunDBMaintenance touches: the reconcile pass and the
// aggregate counts it logs afterwards. Two of the 36 methods the store offers,
// which is why this is declared here and not taken as a wide type.
type Store interface {
	ReconcileState(ctx context.Context, gone func(context.Context, string) (bool, error),
		unavailable func(string) (string, bool)) (subflux.ReconcileResult, error)
	Stats(ctx context.Context) (downloads, attempts int, err error)
}

// presence decides which files reconcile may treat as gone;
// *mediapresence.Checker satisfies it.
type presence interface {
	Gone(ctx context.Context, path string) (bool, error)
	Unavailable(path string) (root string, unavailable bool)
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
	Media               scanning.MediaGuard
	Presence            presence
	StateFunc           func() *LiveState
	ScanningFlag        *atomic.Bool
	DeleteSubtitleFiles func(paths []string, source string)
}

// LiveState holds the live state needed by the scheduler.
//
// Every field is typed as scanning's own interface, for the same reason
// Deps.ScanDB is. The scheduler reads exactly ONE of the 37 values the config
// offers — Search(), for the scan interval and the upgrade flag it logs — reads
// nothing at all off the arr clients, and otherwise only carries the values into
// scanning.LiveState. A separate declaration here would have to be assignable to
// scanning's surface anyway, so it could only drift.
type LiveState struct {
	Cfg    scanning.ScanCfg
	Engine scanning.ScanEngine
	// ShowCounter is nil when no provider offers the show-level count.
	ShowCounter scanning.ShowCounter
	Sonarr      scanning.ScanSonarrClient
	Radarr      scanning.ScanRadarrClient
	Providers   []provider.Provider
}

// Run runs the periodic scan and DB maintenance until ctx is cancelled: the
// first pass after StartupDelay, then each pass scan_interval after the last
// one ends, or MediaRetryInterval after one a media folder refused.
func Run(ctx context.Context, deps *Deps) {
	ls := deps.StateFunc()
	slog.Info("scheduler started",
		"scan_interval", ls.Cfg.Search().ScanInterval.String(),
		"upgrade_enabled", ls.Cfg.Search().UpgradeEnabled)

	timer := time.NewTimer(StartupDelay)
	defer timer.Stop()
	retry := false
	for {
		select {
		case <-timer.C:
		case <-ctx.Done():
			return
		}
		var next time.Duration
		next, retry = runCycle(ctx, deps, retry)
		if ctx.Err() != nil {
			return
		}
		timer.Reset(next)
		slog.Info("next scheduled scan", "in", next.String(), "media_retry", retry)
	}
}

// runCycle runs one scheduled pass. mediaRetry reports that the previous
// pass was refused or stopped by an unwritable media folder: the roots are
// write-tested first, and while they still refuse, DB maintenance and the
// scan are skipped. retry reports the same of this pass.
func runCycle(ctx context.Context, deps *Deps, mediaRetry bool) (next time.Duration, retry bool) {
	if mediaRetry {
		if err := deps.Media.Preflight(ctx, mediawrite.PreflightRequest{Roots: true, Raise: true}); err != nil {
			return MediaRetryInterval, true
		}
	}
	RunDBMaintenance(ctx, deps)
	if ctx.Err() != nil {
		return 0, false
	}
	if res := GuardedScan(ctx, deps); res.MediaUnwritable {
		return MediaRetryInterval, true
	}
	return deps.StateFunc().Cfg.Search().ScanInterval, false
}

// GuardedScan acquires the scanning flag before running a full scan. The
// zero result means the scan was skipped because one is already running.
func GuardedScan(ctx context.Context, deps *Deps) scanning.FullScanResult {
	if !deps.ScanningFlag.CompareAndSwap(false, true) {
		slog.Debug("scheduler: scan skipped, already in progress")
		return scanning.FullScanResult{}
	}
	defer deps.ScanningFlag.Store(false)
	_, run := PrepareFullScan(deps, activity.SourceScheduled)
	return run(ctx)
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
func PrepareFullScan(deps *Deps, source activity.Source) (actID string, run func(ctx context.Context) scanning.FullScanResult) {
	actID, _ = deps.Activity.StartScan(FullScanAction, FullScanDetail, source,
		activity.ScanScope{Kind: activity.ScanKindFull}, auth.RoleAdmin)
	deps.Events.PublishScanStart(&events.ScanEvent{
		Action: FullScanAction, Detail: FullScanDetail, Source: source, ActivityID: actID,
	})
	stopCh := make(chan struct{})
	unregister := deps.Stops.RegisterStop(actID, func() { close(stopCh) })
	run = func(ctx context.Context) scanning.FullScanResult {
		// Panic fallback only: FinishScanActivity releases the registration
		// explicitly BEFORE the terminal transition on every normal return
		// (idempotent), so a done entry never reports cancellable. The
		// defer covers a panicking scan body.
		defer unregister()
		res := runFullScan(ctx, stopCh, deps, actID)
		scanning.FinishScanActivity(unregister, deps.Activity, deps.Events,
			actID, FullScanAction, FullScanDetail, source, res.Outcome)
		return res
	}
	return actID, run
}

// runFullScan assembles the scanning package's deps and executes the scan.
func runFullScan(ctx context.Context, stop <-chan struct{}, deps *Deps, actID string) scanning.FullScanResult {
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
		Media:         deps.Media,
		ClearCaches:   provider.ClearCaches,
	}
	scanLS := &scanning.LiveState{
		Cfg:         ls.Cfg,
		Engine:      ls.Engine,
		Sonarr:      ls.Sonarr,
		Radarr:      ls.Radarr,
		Providers:   ls.Providers,
		ShowCounter: ls.ShowCounter,
	}
	return scanning.RunFullScan(ctx, stop, scanDeps, scanLS, actID)
}

// RunDBMaintenance prunes old state and stale search attempts.
func RunDBMaintenance(ctx context.Context, deps *Deps) {
	start := time.Now()
	slog.Debug("db maintenance starting")
	result, err := deps.DB.ReconcileState(ctx, deps.Presence.Gone, deps.Presence.Unavailable)
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
