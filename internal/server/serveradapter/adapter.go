// Package serveradapter provides adapter types that bridge the server's
// activity, alert, and event subsystems to the interfaces consumed by the
// scanning and manualops packages.
package serveradapter

import (
	"log/slog"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/scanning"
)

// Compile-time assertions.
var (
	_ scanning.ActivityTracker = (*ActivityAdapter)(nil)
	_ scanning.AlertRecorder   = (*AlertAdapter)(nil)
	_ scanning.EventPublisher  = (*ScanEventAdapter)(nil)

	_ manualops.ActivityTracker = (*ActivityAdapter)(nil)
	_ activity.WarnRecorder     = (*AlertAdapter)(nil)
	_ manualops.EventPublisher  = (*ManualEventAdapter)(nil)
)

// ActivityAdapter bridges activity.Log to scanning.ActivityTracker and
// manualops.ActivityTracker (superset of methods).
type ActivityAdapter struct{ A *activity.Log }

// Start begins a new activity entry and returns its ID.
func (a *ActivityAdapter) Start(action, detail string, source activity.ActivitySource) string {
	return a.A.Start(action, detail, source)
}

// StartScan begins a new scan activity carrying its structured scope and
// cancel role; an active same-scope entry is returned instead of creating a
// duplicate (existing=true).
func (a *ActivityAdapter) StartScan(action, detail string, source activity.ActivitySource,
	scope activity.ScanScope, role auth.Role,
) (id string, existing bool) {
	return a.A.StartScan(action, detail, source, scope, role)
}

// End marks the activity with the given ID as successfully completed.
func (a *ActivityAdapter) End(id string) { a.A.End(id) }

// Fail marks the activity with the given ID as failed.
func (a *ActivityAdapter) Fail(id string) { a.A.Fail(id) }

// FinishCancelled marks the activity with the given ID as terminally
// cancelled (Done + Cancelled + EndedAt).
func (a *ActivityAdapter) FinishCancelled(id string) { a.A.FinishCancelled(id) }

// Progress updates the progress counters and message for an activity.
func (a *ActivityAdapter) Progress(id string, current, total int, msg string) {
	a.A.Progress(id, current, total, msg)
}

// SetQueued marks an activity as queued or active.
func (a *ActivityAdapter) SetQueued(id string, queued bool) { a.A.SetQueued(id, queued) }

// IsCancelled reports whether the activity with the given ID has been cancelled.
func (a *ActivityAdapter) IsCancelled(id string) bool { return a.A.IsCancelled(id) }

// AlertAdapter bridges activity.AlertLog to scanning.AlertRecorder and
// activity.WarnRecorder (superset of methods).
type AlertAdapter struct{ A *activity.AlertLog }

// Record emits an alert with the given category and message.
func (a *AlertAdapter) Record(category, msg string) { a.A.Record(category, msg) }

// RecordInfo emits an informational alert message.
func (a *AlertAdapter) RecordInfo(msg string) { a.A.RecordInfo(msg) }

// RecordWarn emits a warning alert with the given source and message.
func (a *AlertAdapter) RecordWarn(source, msg string) { a.A.RecordWarn(source, msg) }

// RecordStoreWriteError checks whether the error indicates a disk-full or
// unrecoverable I/O condition and raises a persistent alert so operators are
// notified before the system crash-loops. Non-disk-full write errors are logged
// at ERROR level with a distinctive message for Loki/Alertmanager matching.
func (a *AlertAdapter) RecordStoreWriteError(err error) {
	if err == nil {
		return
	}
	if boltstore.IsDiskFullError(err) {
		slog.Error("store write failed: disk full or I/O error — persistent alert raised",
			"error", err)
		a.A.RecordPersistent("store",
			"Database write failed (disk full or I/O error): "+err.Error()+
				". Free disk space or check filesystem permissions to resume normal operation.")
		return
	}
	// Non-disk-full write error: log distinctively for monitoring.
	slog.Error("store write failed", "error", err)
}

// ScanEventAdapter bridges events.EventBus to scanning.EventPublisher.
type ScanEventAdapter struct{ E *events.EventBus }

// PublishCoverageUpdate publishes a coverage-update SSE event for a media item.
func (a *ScanEventAdapter) PublishCoverageUpdate(mediaType api.MediaType, mediaID string) {
	a.E.Publish(events.Event{
		Type: events.CoverageUpdate,
		Data: events.CoverageEvent{MediaType: mediaType, MediaID: mediaID},
	})
}

// PublishScanStart publishes a scan-start SSE event carrying the activity id.
func (a *ScanEventAdapter) PublishScanStart(action, detail string, source activity.ActivitySource, actID string) {
	a.E.Publish(events.Event{
		Type: events.ScanStart,
		Data: events.ScanEvent{Action: action, Detail: detail, Source: source, ActivityID: actID},
	})
}

// PublishScanDone publishes a scan-done SSE event with the activity id and
// the four-valued terminal outcome.
func (a *ScanEventAdapter) PublishScanDone(action, detail string, source activity.ActivitySource,
	actID string, outcome activity.Outcome,
) {
	a.E.Publish(events.Event{
		Type: events.ScanDone,
		Data: events.ScanEvent{Action: action, Detail: detail, Source: source, ActivityID: actID, Outcome: outcome},
	})
}

// ManualEventAdapter bridges events.EventBus to manualops.EventPublisher.
type ManualEventAdapter struct{ E *events.EventBus }

// PublishNotify publishes a user-facing notification SSE event at the given severity level.
func (a *ManualEventAdapter) PublishNotify(level events.NotifyLevel, text string) {
	a.E.Publish(events.Event{Type: events.Notify, Data: events.NotifyEvent{Level: level, Text: text}})
}

// PublishCoverageUpdate publishes a coverage-update SSE event for the manual
// download/clear-lock case (no path on the wire; see events.CoverageEvent).
func (a *ManualEventAdapter) PublishCoverageUpdate(mediaType api.MediaType, mediaID, language, source string) {
	a.E.Publish(events.Event{
		Type: events.CoverageUpdate,
		Data: events.CoverageEvent{
			MediaType: mediaType,
			MediaID:   mediaID,
			Language:  language,
			Source:    source,
		},
	})
}
