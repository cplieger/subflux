// Package events is subflux's typed server-sent-events layer: the sealed
// Event/EventData types the app publishes, marshaled onto the shared
// cplieger/sse broadcast hub. The transport (fan-out, replay ring with
// Last-Event-ID resume, the hello handshake and its verdict, keepalives,
// proxy-defensive headers, slow-client eviction, the state digest) is the
// library's; this package owns the subflux event vocabulary, its wire
// encoding, and the subject versions the digest compares.
package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"github.com/cplieger/sse"
	"github.com/cplieger/subflux/internal/server/activity"
)

// SSERing is the replay-ring capacity in events (sse.WithReplay).
const SSERing = 1024

// ReplyMaxEvents is the largest replay one reconnect may receive
// (sse.WithReplyMaxEvents); a cursor further behind head earns gap_budget
// and the client reconciles through the digest, which is cheaper than a
// bulk replay.
const ReplyMaxEvents = 256

// SSEReplayTTL is the longest a ring entry may be replayed after it was
// published (sse.WithReplayTTL).
const SSEReplayTTL = 10 * time.Minute

// SSEReconnectDelay is the stream's advertised `retry:` field: the reconnect
// wait a legacy EventSource performs by itself after a transient drop and the
// floor of the v3 client's post-EOF backoff. Without it Chrome waits 3s and
// Firefox 5s; it is deliberately not lower because the spec routes a
// connection to a DOWN server through the same timer.
const SSEReconnectDelay = 1500 * time.Millisecond

// Metrics is the observability sink the bus records into: one counter per
// hello verdict split by the legacy overlap, one per presence departure
// cause, one per presence-table transition, and the three hub gauges.
// *obs.Metrics satisfies it; nil at construction records nothing.
type Metrics interface {
	RecordSSEConnect(verdict string, legacy bool)
	RecordSSEDisconnect(cause string)
	RecordSSEPresenceTransition(kind string)
	SetSSEClients(n int)
	SetSSEQueuedFrames(n int)
	SetSSEHead(n uint64)
}

// nopMetrics is the sink a nil Metrics resolves to.
type nopMetrics struct{}

func (nopMetrics) RecordSSEConnect(string, bool)      {}
func (nopMetrics) RecordSSEDisconnect(string)         {}
func (nopMetrics) RecordSSEPresenceTransition(string) {}
func (nopMetrics) SetSSEClients(int)                  {}
func (nopMetrics) SetSSEQueuedFrames(int)             {}
func (nopMetrics) SetSSEHead(uint64)                  {}

// EventBus publishes subflux's typed events to connected SSE clients.
// A nil *EventBus is safe to publish to (no-op), so optional wiring needs
// no guards.
type EventBus struct {
	hub      *sse.Hub
	versions *Versions
	presence *Presence
	metrics  Metrics
}

// New creates the event bus with the given concurrent-client cap (<= 0 means
// DefaultMaxSSEClients) and metrics sink (nil records nothing). Every hub
// option is a package constant, so a refused option set is a defect fixed
// here rather than an error a caller could handle, which is what licenses
// MustNew outside main.
func New(maxClients int, m Metrics) *EventBus {
	if maxClients <= 0 {
		maxClients = DefaultMaxSSEClients
	}
	if m == nil {
		m = nopMetrics{}
	}
	eb := &EventBus{metrics: m, presence: newPresence(m)}
	eb.hub = sse.MustNew(
		sse.WithMaxClients(maxClients),
		sse.WithReplay(SSERing),
		sse.WithReplayTTL(SSEReplayTTL),
		sse.WithReplyMaxEvents(ReplyMaxEvents),
		sse.WithReconnectDelay(SSEReconnectDelay),
		sse.WithPresence(func(ev sse.PresenceEvent) { eb.onPresence(&ev) }),
	)
	eb.versions = newVersions(eb.hub.Position().Epoch)
	return eb
}

// Versions is the digest's version table: Publish mints the event-fed
// subjects into it, the sync-job registry mints jobs, and the stamp
// middleware and the digest resolver read it.
func (eb *EventBus) Versions() *Versions {
	return eb.versions
}

// onPresence is the hub's presence hook. Connects are counted by the
// OnConnect hook in Handle (which knows the legacy split), so this side
// counts departures only; the client gauge and the presence table move on
// both.
func (eb *EventBus) onPresence(ev *sse.PresenceEvent) {
	switch ev.Kind {
	case sse.PresenceConnected:
		eb.presence.connected(ev.Tag, ev.At)
	case sse.PresenceDisconnected:
		eb.presence.disconnected(ev.Tag)
		eb.metrics.RecordSSEDisconnect(string(ev.Cause))
	}
	eb.metrics.SetSSEClients(eb.hub.ClientCount())
}

// Presence is the per-tag presence table the alive route and the prune
// ticker drive.
func (eb *EventBus) Presence() *Presence {
	return eb.presence
}

// DigestHandler is the library's state digest over this bus's version
// table. Mount it inside the authentication and cross-origin middleware,
// under a route timeout (it does not stream).
func (eb *EventBus) DigestHandler() http.Handler {
	return eb.hub.DigestHandler(eb.versions.Resolve)
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

// Shutdown drains the hub: every connected stream is sent sse:reset and
// waited for, and later connection attempts are refused with 503, so
// graceful shutdown is not held open by long-lived SSE requests. Returns
// ctx.Err() when the drain outlives ctx. No-op when the bus is nil.
func (eb *EventBus) Shutdown(ctx context.Context) error {
	if eb == nil {
		return nil
	}
	return eb.hub.Shutdown(ctx)
}

// Publish mints the subject versions the event moved and broadcasts it to
// every connected client. The wire payload is the JSON-encoded Event (type +
// data), sent as a NAMED SSE event (`event: <type>`), the key the client's
// frame table decodes by. No-op when the bus is nil.
func (eb *EventBus) Publish(e Event) {
	if eb == nil {
		return
	}
	data, err := json.Marshal(e)
	if err != nil {
		slog.Warn("SSE: failed to marshal event", "type", e.Type, "error", err)
		return
	}
	eb.versions.bumpFor(e)
	if _, err := eb.hub.Publish(sse.Event{Name: string(e.Type), Data: data}); err != nil {
		slog.Warn("SSE: event refused by the hub", "type", e.Type, "error", err)
	}
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

// SampleGauges records the three hub gauges (clients, queued frames, head)
// into the metrics sink. Driven by the server's prune ticker; the client
// gauge also moves on every presence event.
func (eb *EventBus) SampleGauges() {
	eb.metrics.SetSSEClients(eb.hub.ClientCount())
	eb.metrics.SetSSEQueuedFrames(eb.hub.QueuedFrames())
	eb.metrics.SetSSEHead(eb.hub.Position().Head)
}
