// Package serveradapter provides adapter types that bridge the server's
// activity, alert, and event subsystems to the interfaces consumed by the
// scanning and manualops packages.
package serveradapter

import (
	"subflux/internal/api"
	"subflux/internal/server/activity"
	"subflux/internal/server/events"
	"subflux/internal/server/manualops"
	"subflux/internal/server/scanning"
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
