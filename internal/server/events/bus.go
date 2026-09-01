// Package events is subflux's typed server-sent-events layer: the sealed
// Event/EventData types the app publishes, marshaled onto the shared
// webhttp/sse broadcast hub. The transport (fan-out, replay ring with
// Last-Event-ID resume, keepalives, proxy-defensive headers, slow-client
// eviction) is the library's; this package owns only the subflux event
// vocabulary and its wire encoding.
package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log/slog"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/webhttp/v2/sse"
)

// SSERing is the replay-ring capacity, sized at 4x ReplayBudget so the
// below-floor and over-budget verdicts stay distinct and the budget can rise
// without a server change.
const SSERing = 1024

// ReplayBudget is the largest replay a resume cursor may request; a cursor
// further behind head answers a gap epoch instead (a refetch transaction is
// cheaper than a bulk replay). It is the fourth disjunct of the handler's
// pre-check and covers hidden-window growth and native retries alike.
const ReplayBudget = 256

// EventBus publishes subflux's typed events to connected SSE clients.
// A nil *EventBus is safe to publish to (no-op), so optional wiring needs
// no guards.
type EventBus struct {
	hub    *sse.Hub
	bootID string
}

// New creates the event bus with the given concurrent-client cap (<= 0 means
// DefaultMaxSSEClients). The underlying hub keeps a replay ring (SSERing), so
// a browser that reconnects after a transient drop resumes via the standard
// Last-Event-ID header instead of silently missing events. The cap is
// enforced by the hub atomically at admission; SetMaxClients re-applies a
// hot-reloaded value without rebuilding the hub. The boot id is minted here,
// once per process start, and rides every connection's epoch handshake so
// clients can tell a server restart from a reconnect.
func New(maxClients int) *EventBus {
	if maxClients <= 0 {
		maxClients = DefaultMaxSSEClients
	}
	return &EventBus{
		hub:    sse.NewHub(sse.WithMaxClients(maxClients), sse.WithReplay(SSERing)),
		bootID: newBootID(),
	}
}

// newBootID returns a random 16-hex-char process identity. crypto/rand
// cannot fail on Go >= 1.24.
func newBootID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// SetMaxClients applies a new client cap (<= 0 means DefaultMaxSSEClients) to
// the running hub — used by config hot reload. Existing connections above a
// lowered cap are not evicted. No-op when the bus is nil.
func (eb *EventBus) SetMaxClients(n int) {
	if eb == nil {
		return
	}
	if n <= 0 {
		n = DefaultMaxSSEClients
	}
	eb.hub.SetMaxClients(n)
}

// Shutdown drains the hub: every connected stream is cancelled and later
// connection attempts are refused with 503, so graceful shutdown is not held
// open by long-lived SSE requests. No-op when the bus is nil.
func (eb *EventBus) Shutdown() {
	if eb == nil {
		return
	}
	eb.hub.Shutdown()
}

// Publish broadcasts an event to every connected client. The wire payload
// is the JSON-encoded Event (type + data), sent as a NAMED SSE event
// (`event: <type>`) so the browser dispatches it to the matching
// addEventListener handler. No-op when the bus is nil.
func (eb *EventBus) Publish(e Event) {
	if eb == nil {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		slog.Warn("SSE: failed to marshal event", "type", e.Type, "error", err)
		return
	}
	eb.hub.Publish(sse.Event{Name: string(e.Type), Data: data})
}

// PublishCoverageUpdate publishes a coverage-update event: a subtitle file
// appeared or disappeared for a media item, so the UI refreshes that row.
// Callers that know only the media identity (the scan engine) leave the
// language/source fields zero; the manual download/clear-lock path fills
// them. The payload is a STRUCT rather than positional arguments because it
// serves both call shapes with one signature. By pointer only to keep the
// 80-byte payload off the argument copy; it is dereferenced immediately and
// never retained.
func (eb *EventBus) PublishCoverageUpdate(ev *CoverageEvent) {
	eb.Publish(Event{Type: CoverageUpdate, Data: *ev})
}

// PublishScanStart publishes scan:start for a scan activity that has just
// been accepted. Outcome is meaningless here and is ignored if set.
func (eb *EventBus) PublishScanStart(ev *ScanEvent) {
	eb.Publish(Event{Type: ScanStart, Data: *ev})
}

// PublishScanDone publishes scan:done with the scan's four-valued terminal
// outcome (see ScanEvent.Outcome).
func (eb *EventBus) PublishScanDone(ev *ScanEvent) {
	eb.Publish(Event{Type: ScanDone, Data: *ev})
}

// PublishNotify publishes a user-facing toast notification at the given
// severity. It keeps positional arguments because the two are different
// types, so a transposition does not compile.
func (eb *EventBus) PublishNotify(level NotifyLevel, text string) {
	eb.Publish(Event{Type: Notify, Data: NotifyEvent{Level: level, Text: text}})
}

// PublishAlert publishes an alert delta: raised (new or refreshed) or
// dismissed. The alert is the under-lock snapshot the AlertLog hook carried;
// it is dereferenced into the payload and never retained.
func (eb *EventBus) PublishAlert(op AlertOp, a *activity.Alert) {
	eb.Publish(Event{Type: AlertDelta, Data: AlertEvent{Op: op, Alert: a}})
}

// PublishProvider publishes a provider timeout delta: raise when a provider
// trips into cooldown, clear when it leaves it.
func (eb *EventBus) PublishProvider(op ProviderOp, entry *ProviderTimeoutEntry) {
	eb.Publish(Event{Type: ProviderDelta, Data: ProviderEvent{Op: op, Entry: entry}})
}

// PublishSyncDone publishes one sync job's terminal result. The event is
// dereferenced into the payload and never retained.
func (eb *EventBus) PublishSyncDone(ev *SyncDoneEvent) {
	eb.Publish(Event{Type: SyncDone, Data: *ev})
}

// ClientCount returns the number of connected SSE clients.
func (eb *EventBus) ClientCount() int {
	return eb.hub.ClientCount()
}
