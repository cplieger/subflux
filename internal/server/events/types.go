package events

import (
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/activity"
)

// EventType is a typed string for server-sent event types.
type EventType string

// EventData is a sealed interface restricting Event.Data to known payload types.
// Implementors: CoverageEvent, NotifyEvent, ScanEvent.
//
// The wiregen directive below emits the TS union
// (export type EventData = CoverageEvent | NotifyEvent | ScanEvent) plus its
// runtime decoders; the discriminator is the SSE envelope's "type" key
// (Event.Type), which is also the named SSE event the browser dispatches on.
//
//wiregen:union discriminator=type variants=CoverageEvent,NotifyEvent,ScanEvent
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

// CoverageEvent is the data payload for coverage updates. It deliberately
// carries no file path (S7: no filesystem paths on the wire; the UI keys
// refreshes on media identity alone).
type CoverageEvent struct {
	MediaType api.MediaType `json:"media_type"`
	MediaID   string        `json:"media_id"`
	Language  string        `json:"language"`
	Variant   string        `json:"variant"`
	Source    string        `json:"source"`
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
// ActivityID correlates the event with its activity entry. Outcome is
// meaningful only on scan:done and carries the four-valued terminal outcome
// (completed | failed | cancelled | shutdown) — a cancelled scan is neither
// a success nor a failure.
type ScanEvent struct {
	Action     string                  `json:"action"`
	Detail     string                  `json:"detail"`
	Source     activity.ActivitySource `json:"source"`
	ActivityID string                  `json:"activity_id,omitempty"`
	Outcome    activity.Outcome        `json:"outcome,omitempty"`
}

func (ScanEvent) eventData() {}
