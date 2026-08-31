package activityhandlers_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cplieger/auth/v5"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/activityhandlers"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/syncjobs"
)

// DELETE /api/activity?id= routing for sync jobs (D1): a QUEUED sync job
// goes through the dispatcher BEFORE the legacy scan path; a terminal sync
// row falls through to the plain dismiss; a non-sync id never consults the
// dispatcher's verdict for its answer.

// fakeSyncCanceller scripts the dispatcher lookup.
type fakeSyncCanceller struct {
	out   syncjobs.CancelOutcome
	calls []string
}

func (f *fakeSyncCanceller) Cancel(id string) syncjobs.CancelOutcome {
	f.calls = append(f.calls, id)
	return f.out
}

func newDismissHarness(canceller activityhandlers.SyncJobCanceller) (*activityhandlers.Handler, *activity.Log) {
	log := activity.New(10)
	h := activityhandlers.New(activityhandlers.Deps{
		Activity: log,
		Alerts:   activity.NewAlertLog(10),
		Stops:    &activity.StopRegistry{},
		Events:   events.New(1),
		SyncJobs: canceller,
	})
	return h, log
}

func deleteActivity(h *activityhandlers.Handler, id string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.HandleDismissActivity(rec, httptest.NewRequest(http.MethodDelete, "/api/activity?id="+id, nil))
	return rec
}

func TestHandleDismissActivity_queued_sync_job_routes_through_the_dispatcher(t *testing.T) {
	t.Parallel()
	canceller := &fakeSyncCanceller{out: syncjobs.CancelledQueued}
	h, log := newDismissHarness(canceller)
	id := log.StartQueued("Audio Sync", "movie.en.srt", activity.SourceManual)

	rec := deleteActivity(h, id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if len(canceller.calls) != 1 || canceller.calls[0] != id {
		t.Errorf("dispatcher consulted with %v, want [%s] before the legacy path", canceller.calls, id)
	}
	// The dispatcher owned the cancel: the legacy path must not have
	// touched the entry (the dispatcher's own settle marks it terminal).
	entry, ok := log.Get(id)
	if !ok || entry.Cancelled {
		t.Errorf("entry after routed cancel = (%+v, %v), want untouched by the legacy path", entry, ok)
	}
}

func TestHandleDismissActivity_race_lost_conversion_answers_204(t *testing.T) {
	t.Parallel()
	canceller := &fakeSyncCanceller{out: syncjobs.CancelConverted}
	h, log := newDismissHarness(canceller)
	id := log.StartQueued("Audio Sync", "movie.en.srt", activity.SourceManual)

	if rec := deleteActivity(h, id); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (the conversion is a success)", rec.Code)
	}
}

func TestHandleDismissActivity_terminal_sync_row_dismisses_only(t *testing.T) {
	t.Parallel()
	canceller := &fakeSyncCanceller{out: syncjobs.CancelTerminal}
	h, log := newDismissHarness(canceller)
	id := log.Start("Audio Sync", "movie.en.srt", activity.SourceManual)
	log.End(id)

	rec := deleteActivity(h, id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	// The terminal row leaves the LOG (a plain dismiss); the job registry is
	// the dispatcher's and stays untouched (pinned in syncjobs' own tests).
	if _, ok := log.Get(id); ok {
		t.Error("terminal entry still in the log; want dismissed")
	}
}

func TestHandleDismissActivity_non_sync_id_takes_the_legacy_path(t *testing.T) {
	t.Parallel()
	canceller := &fakeSyncCanceller{out: syncjobs.CancelUnknown}
	h, log := newDismissHarness(canceller)
	id, _ := log.StartScan("Scan", "d", activity.SourceManual, activity.ScanScope{Kind: activity.ScanKindSeries, MediaID: 1}, auth.RoleUser)
	log.SetQueued(id, true)

	rec := deleteActivity(h, id)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	entry, ok := log.Get(id)
	if !ok || !entry.Cancelled {
		t.Errorf("entry = (%+v, %v), want the legacy queued-cancel to have marked it", entry, ok)
	}
}
