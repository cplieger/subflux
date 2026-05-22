package events

import (
	"subflux/internal/api"
	"subflux/internal/server/activity"
)

// EventType is a typed string for server-sent event types.
type EventType string

// EventData is a sealed interface restricting Event.Data to known payload types.
// Implementors: CoverageEvent, NotifyEvent, ScanEvent.
type EventData interface{ eventData() }

// Event is a server-sent event pushed to connected browsers.
type Event struct {
	Data EventData `json:"data,omitempty"`
	Type EventType `json:"type"`
}

// Event type constants.
const (
	CoverageUpdate EventType = "coverage"   // subtitle file added/removed
	Notify         EventType = "notify"     // toast notification for the UI
	ScanStart      EventType = "scan:start" // scan activity started (any scope)
	ScanDone       EventType = "scan:done"  // scan activity finished (succeeded or failed)
)

// CoverageEvent is the data payload for coverage updates.
type CoverageEvent struct {
	MediaType api.MediaType `json:"media_type"`
	MediaID   string        `json:"media_id"`
	Language  string        `json:"language"`
	Variant   string        `json:"variant"`
	Source    string        `json:"source"`
	Path      string        `json:"path,omitempty"`
}

func (CoverageEvent) eventData() {}

// NotifyLevel is a typed string for notification severity.
type NotifyLevel string

// Notification level constants.
const (
	NotifyError   NotifyLevel = "error"
	NotifySuccess NotifyLevel = "success"
	NotifyInfo    NotifyLevel = "info"
)

// NotifyEvent is the data payload for toast notifications pushed to the UI.
type NotifyEvent struct {
	Level NotifyLevel `json:"level"`
	Text  string      `json:"text"`
}

func (NotifyEvent) eventData() {}

// ScanEvent is the data payload for scan:start and scan:done. Action and
// Detail mirror the activity log entry (e.g. "Full Scan" / "Searching
// library for missing subtitles"). Source is "scheduled" or "manual".
// Succeeded is meaningful only on scan:done; false indicates the scan
// ended via activity.fail rather than activity.end.
type ScanEvent struct {
	Action    string                  `json:"action"`
	Detail    string                  `json:"detail"`
	Source    activity.ActivitySource `json:"source"`
	Succeeded bool                    `json:"succeeded,omitempty"`
}

func (ScanEvent) eventData() {}
