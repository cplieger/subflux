// Package serveradapter provides adapter types that bridge the server's
// activity, alert, and event subsystems to the interfaces consumed by the
// scanning and manualops packages.
package serveradapter

import (
	"log/slog"

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

func (a *ActivityAdapter) Start(action, detail string, source activity.ActivitySource) string {
	return a.A.Start(action, detail, source)
}
func (a *ActivityAdapter) End(id string)  { a.A.End(id) }
func (a *ActivityAdapter) Fail(id string) { a.A.Fail(id) }
func (a *ActivityAdapter) Progress(id string, current, total int, msg string) {
	a.A.Progress(id, current, total, msg)
}
func (a *ActivityAdapter) SetQueued(id string, queued bool) { a.A.SetQueued(id, queued) }
func (a *ActivityAdapter) IsCancelled(id string) bool       { return a.A.IsCancelled(id) }

// AlertAdapter bridges activity.AlertLog to scanning.AlertRecorder and
// activity.WarnRecorder (superset of methods).
type AlertAdapter struct{ A *activity.AlertLog }

func (a *AlertAdapter) Record(category, msg string)   { a.A.Record(category, msg) }
func (a *AlertAdapter) RecordInfo(msg string)         { a.A.RecordInfo(msg) }
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

func (a *ScanEventAdapter) PublishCoverageUpdate(mediaType api.MediaType, mediaID string) {
	a.E.Publish(events.Event{
		Type: events.CoverageUpdate,
		Data: events.CoverageEvent{MediaType: mediaType, MediaID: mediaID},
	})
}

func (a *ScanEventAdapter) PublishScanStart(action, detail string, source activity.ActivitySource) {
	a.E.Publish(events.Event{
		Type: events.ScanStart,
		Data: events.ScanEvent{Action: action, Detail: detail, Source: source},
	})
}

func (a *ScanEventAdapter) PublishScanDone(action, detail string, source activity.ActivitySource, ok bool) {
	a.E.Publish(events.Event{
		Type: events.ScanDone,
		Data: events.ScanEvent{Action: action, Detail: detail, Source: source, Succeeded: ok},
	})
}

// ManualEventAdapter bridges events.EventBus to manualops.EventPublisher.
type ManualEventAdapter struct{ E *events.EventBus }

func (a *ManualEventAdapter) PublishNotify(level events.NotifyLevel, text string) {
	a.E.Publish(events.Event{Type: events.Notify, Data: events.NotifyEvent{Level: level, Text: text}})
}

func (a *ManualEventAdapter) PublishCoverageUpdate(mediaType api.MediaType, mediaID, language, source, path string) {
	a.E.Publish(events.Event{
		Type: events.CoverageUpdate,
		Data: events.CoverageEvent{
			MediaType: mediaType,
			MediaID:   mediaID,
			Language:  language,
			Source:    source,
			Path:      path,
		},
	})
}
