package scheduler_test

import (
	"context"
	"os"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/scheduler"
	"github.com/cplieger/subflux/internal/server/serveradapter"
	"github.com/cplieger/subflux/internal/testsupport"
)

// fakeStore embeds NopStore and overrides only ReconcileState so each test can
// drive the reconcile outcome RunDBMaintenance reacts to.
type fakeStore struct {
	*testsupport.NopStore
	reconcileErr error
	reconcile    api.ReconcileResult
}

var _ api.Store = (*fakeStore)(nil)

func (f *fakeStore) ReconcileState(context.Context) (api.ReconcileResult, error) {
	return f.reconcile, f.reconcileErr
}

// fakeReconcileMetrics records the arguments of the most recent RecordReconcile.
type fakeReconcileMetrics struct {
	deleted int
	reset   int64
	called  bool
}

func (f *fakeReconcileMetrics) RecordReconcile(deleted int, reset int64, _ time.Duration) {
	f.deleted, f.reset, f.called = deleted, reset, true
}

// --- RunDBMaintenance ---

func TestRunDBMaintenance_forwardsReconciledDeletionsAndMetrics(t *testing.T) {
	store := &fakeStore{
		NopStore: &testsupport.NopStore{},
		reconcile: api.ReconcileResult{
			Deleted:    api.CleanupResult{Paths: []string{"/m/a.fr.srt", "/m/b.en.srt"}},
			ResetCount: 3,
		},
	}
	metrics := &fakeReconcileMetrics{}
	var gotPaths []string
	var gotSource string
	deps := &scheduler.Deps{
		DB:               store,
		ReconcileMetrics: metrics,
		DeleteSubtitleFiles: func(paths []string, source string) {
			gotPaths, gotSource = paths, source
		},
	}

	scheduler.RunDBMaintenance(context.Background(), deps)

	if !slices.Equal(gotPaths, []string{"/m/a.fr.srt", "/m/b.en.srt"}) {
		t.Errorf("DeleteSubtitleFiles paths = %v, want the two reconciled paths", gotPaths)
	}
	if gotSource != "reconcile" {
		t.Errorf("DeleteSubtitleFiles source = %q, want %q", gotSource, "reconcile")
	}
	if !metrics.called {
		t.Fatal("RecordReconcile was not called")
	}
	if metrics.deleted != 2 || metrics.reset != 3 {
		t.Errorf("RecordReconcile(deleted=%d, reset=%d), want (2, 3)", metrics.deleted, metrics.reset)
	}
}

func TestRunDBMaintenance_diskFullReconcileError_raisesPersistentAlert(t *testing.T) {
	// os.ErrPermission is classified as a disk/IO failure by IsDiskFullError,
	// which must escalate to a persistent operator alert instead of a quiet log.
	store := &fakeStore{NopStore: &testsupport.NopStore{}, reconcileErr: os.ErrPermission}
	al := activity.NewAlertLog(10)
	deps := &scheduler.Deps{
		DB:                  store,
		Alerts:              &serveradapter.AlertAdapter{A: al},
		DeleteSubtitleFiles: func([]string, string) {},
		// ReconcileMetrics left nil: also exercises the nil-safe metrics path.
	}

	scheduler.RunDBMaintenance(context.Background(), deps)

	visible := al.VisibleAlerts()
	if len(visible) != 1 {
		t.Fatalf("got %d visible alerts, want exactly 1 persistent store alert", len(visible))
	}
	if visible[0].Kind != activity.AlertPersistent || visible[0].Source != "store" {
		t.Errorf("alert = {kind:%q source:%q}, want {persistent store}", visible[0].Kind, visible[0].Source)
	}
}

// --- GuardedScan ---

func TestGuardedScan_skipsWhenScanAlreadyInProgress(t *testing.T) {
	var flag atomic.Bool
	flag.Store(true) // a scan is already running
	stateFuncCalled := false
	deps := &scheduler.Deps{
		ScanningFlag: &flag,
		StateFunc: func() *scheduler.LiveState {
			stateFuncCalled = true
			return &scheduler.LiveState{}
		},
	}

	scheduler.GuardedScan(context.Background(), deps)

	if stateFuncCalled {
		t.Error("GuardedScan started a scan (read live state) despite one already being in progress")
	}
	if !flag.Load() {
		t.Error("GuardedScan cleared the in-progress flag it never acquired")
	}
}

// --- PrepareFullScan: the hoisted accept sequence for full scans ---

// prepDeps builds Deps sufficient for a full-scan pass with no arr clients
// configured (empty queue: collect is skipped, the scan completes
// immediately).
func prepDeps(log *activity.Log, stops *activity.StopRegistry, bus *events.EventBus) *scheduler.Deps {
	var flag atomic.Bool
	return &scheduler.Deps{
		DB:       &fakeStore{NopStore: &testsupport.NopStore{}},
		ScanDB:   &testsupport.NopStore{},
		Metrics:  nopMetrics{},
		Events:   &serveradapter.ScanEventAdapter{E: bus},
		Activity: &serveradapter.ActivityAdapter{A: log},
		Alerts:   &serveradapter.AlertAdapter{A: activity.NewAlertLog(10)},
		Stops:    stops,
		StateFunc: func() *scheduler.LiveState {
			return &scheduler.LiveState{Cfg: &testsupport.NopConfig{}}
		},
		ScanningFlag:        &flag,
		DeleteSubtitleFiles: func([]string, string) {},
	}
}

type nopMetrics struct{}

func (nopMetrics) RecordScan(int, int, time.Duration) {}
func (nopMetrics) AdaptiveSkip()                      {}

func TestPrepareFullScan_hoists_activity_and_registration(t *testing.T) {
	log := activity.New(10)
	stops := &activity.StopRegistry{}
	deps := prepDeps(log, stops, nil)

	actID, run := scheduler.PrepareFullScan(deps, activity.SourceScheduled)

	// The id exists — with its scope, admin-only cancel role, and a LIVE
	// stop registration — BEFORE the scan body runs. Scheduled scans
	// register too: they are stoppable by admins.
	if actID == "" {
		t.Fatal("PrepareFullScan returned no activity id")
	}
	entry, ok := log.Get(actID)
	if !ok {
		t.Fatal("activity entry missing before run")
	}
	if entry.Kind != activity.ScanKindFull {
		t.Errorf("entry.Kind = %q, want full", entry.Kind)
	}
	if entry.RequiredRole != "admin" {
		t.Errorf("entry.RequiredRole = %q, want admin", entry.RequiredRole)
	}
	if entry.Source != activity.SourceScheduled {
		t.Errorf("entry.Source = %q, want scheduled", entry.Source)
	}
	if !stops.Cancellable(actID) {
		t.Fatal("no live stop registration before run")
	}

	run(context.Background())

	entry, _ = log.Get(actID)
	if !entry.Done || entry.Failed || entry.Cancelled {
		t.Errorf("entry after run = done=%v failed=%v cancelled=%v, want clean completion",
			entry.Done, entry.Failed, entry.Cancelled)
	}
	if stops.Cancellable(actID) {
		t.Error("stop registration leaked after the run's terminal transition")
	}
}

func TestPrepareFullScan_shutdown_leaves_no_terminal_marking(t *testing.T) {
	log := activity.New(10)
	stops := &activity.StopRegistry{}
	deps := prepDeps(log, stops, nil)

	actID, run := scheduler.PrepareFullScan(deps, activity.SourceManual)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	run(ctx)

	entry, _ := log.Get(actID)
	if entry.Done || entry.Cancelled || entry.Failed {
		t.Errorf("entry after shutdown run = done=%v cancelled=%v failed=%v, want no user-facing marking",
			entry.Done, entry.Cancelled, entry.Failed)
	}
	if stops.Cancellable(actID) {
		t.Error("stop registration leaked after shutdown")
	}
}
