package events

import (
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/subflux"
)

// EventType is a typed string for server-sent event types.
type EventType string

// EventData is a sealed interface restricting Event.Data to known payload types.
// Implementors: CoverageEvent, NotifyEvent, ScanEvent, EpochEvent, ActivityEvent,
// AlertEvent, ProviderEvent, SyncDoneEvent.
//
// The wiregen directive below emits the TS union
// (export type EventData = CoverageEvent | NotifyEvent | ... | SyncDoneEvent)
// plus its runtime decoders; the discriminator is the SSE envelope's "type" key
// (Event.Type), which is also the named SSE event the browser dispatches on.
//
//wiregen:union discriminator=type variants=CoverageEvent,NotifyEvent,ScanEvent,EpochEvent,ActivityEvent,AlertEvent,ProviderEvent,SyncDoneEvent
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
	Epoch          EventType = "epoch"      // per-connection handshake (never replayed, no id)
	ActivityDelta  EventType = "activity"   // activity log delta (upsert/remove)
	AlertDelta     EventType = "alert"      // alert raised or dismissed
	ProviderDelta  EventType = "provider"   // provider timeout raised or cleared
	SyncDone       EventType = "sync:done"  // one sync job's terminal result
)

// CoverageEvent is the data payload for coverage updates. It deliberately
// carries no file path (S7: no filesystem paths on the wire; the UI keys
// refreshes on media identity alone).
type CoverageEvent struct {
	MediaType subflux.MediaType `json:"media_type"`
	MediaID   string            `json:"media_id"`
	Language  string            `json:"language"`
	Variant   string            `json:"variant"`
	Source    string            `json:"source"`
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
	Action     string           `json:"action"`
	Detail     string           `json:"detail"`
	Source     activity.Source  `json:"source"`
	ActivityID string           `json:"activity_id,omitempty"`
	Outcome    activity.Outcome `json:"outcome,omitempty"`
}

func (ScanEvent) eventData() {}

// EpochEvent is the per-connection SSE handshake, written exactly once per
// connection by the events handler: after any Last-Event-ID replay, before
// live delivery, with NO id field (it must never become a resume cursor).
// BootID identifies the server process (one random id per process start), so
// the client can tell a restart from a reconnect. Gap is the server's
// authoritative replay verdict: true means the presented cursor could not be
// covered (above head, below the ring floor, or past the replay budget) and
// the replay was withheld. Head is the newest event id the connection's
// replay covered; every id at or below it was either replayed or predates
// the client's cursor.
type EpochEvent struct {
	BootID string `json:"boot_id"`
	Head   uint64 `json:"head"`
	Gap    bool   `json:"gap"`
}

func (EpochEvent) eventData() {}

// ActivityOp discriminates an activity event: an entry changed or appeared
// (upsert) or left the log (remove).
type ActivityOp string

// Activity event operations.
const (
	ActivityUpsert ActivityOp = "upsert"
	ActivityRemove ActivityOp = "remove"
)

// ActivityEvent is the data payload for activity log deltas (E1). Upserts
// carry the post-mutation entry snapshot; removes carry the entry as it was
// when it left the log, on every removal path (dismiss, prune, cap
// eviction). Terminal transitions are published immediately; non-terminal
// progress coalesces per activity (see ActivityPublisher).
type ActivityEvent struct {
	Entry *activity.Entry `json:"entry,omitempty"`
	Op    ActivityOp      `json:"op"`
}

func (ActivityEvent) eventData() {}

// AlertOp discriminates an alert event: raised (new or refreshed) or
// dismissed. TTL expiry publishes nothing — the client's reconcile poll owns
// it, matching the lazy expiry in AlertLog.VisibleAlerts.
type AlertOp string

// Alert event operations.
const (
	AlertRaise   AlertOp = "raise"
	AlertDismiss AlertOp = "dismiss"
)

// AlertEvent is the data payload for alert deltas (E1): a raise carries the
// alert as recorded (a persistent re-raise carries the refreshed snapshot),
// a dismiss carries the dismissed alert so the client can drop it by id.
type AlertEvent struct {
	Alert *activity.Alert `json:"alert,omitempty"`
	Op    AlertOp         `json:"op"`
}

func (AlertEvent) eventData() {}

// ProviderOp discriminates a provider event: a provider tripped into timeout
// cooldown (raise) or left it (clear).
type ProviderOp string

// Provider event operations.
const (
	ProviderRaise ProviderOp = "raise"
	ProviderClear ProviderOp = "clear"
)

// ProviderTimeoutEntry pairs a provider with its timeout status, the same
// per-provider shape GET /api/providers/timeout serves in ProvidersResponse.
type ProviderTimeoutEntry struct {
	Provider subflux.ProviderID     `json:"provider"`
	Status   subflux.ProviderStatus `json:"status"`
}

// ProviderEvent is the data payload for provider timeout deltas (E1): raise
// when a provider trips into cooldown (Status carries the trip snapshot),
// clear when it leaves it — expiry observed, success reset, or operator
// reset. A cooldown nobody asks about expires silently; the client's
// reconcile poll converges that case.
type ProviderEvent struct {
	Entry *ProviderTimeoutEntry `json:"entry,omitempty"`
	Op    ProviderOp            `json:"op"`
}

func (ProviderEvent) eventData() {}

// SyncDoneEvent is the data payload for sync:done (D1): one sync job's
// terminal result, published when the job's worker ran (a queued
// cancellation publishes nothing). JobID is the dialog's correlation key —
// the 202 handed it over, and replay is idempotent per job_id. OffsetMs is
// the CUMULATIVE offset (stored plus this run's correction); Error is set
// for timeout/crash/cancelled outcomes.
type SyncDoneEvent struct {
	BatchActivityID string          `json:"batch_activity_id,omitempty"`
	Method          string          `json:"method,omitempty"`
	Error           string          `json:"error,omitempty"`
	FileRef         resolve.FileRef `json:"file_ref"`
	JobID           int64           `json:"job_id"`
	OffsetMs        int64           `json:"offset_ms"`
	Confidence      float64         `json:"confidence"`
	Applied         bool            `json:"applied"`
	DryRun          bool            `json:"dry_run"`
}

func (SyncDoneEvent) eventData() {}
