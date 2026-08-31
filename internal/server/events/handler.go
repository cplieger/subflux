package events

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/cplieger/webhttp/v2/sse"
)

// DefaultMaxSSEClients is the upper bound on concurrent SSE connections
// when no config is loaded or the configured value is zero.
const DefaultMaxSSEClients = 32

// maxEventID is the largest resume cursor accepted (2^53-1): event ids ride
// JSON numbers into a JS client, so anything above the safe-integer bound is
// noise and reads as "no resume".
const maxEventID = 1<<53 - 1

// Handle streams server-sent events to the browser, wrapping the sse hub
// with subflux's resume protocol:
//
//   - The resume cursor is the Last-Event-ID header when present (a native
//     EventSource retry — the header wins, on recreates too); when absent, a
//     valid ?last_id= query derives one (the client's synthetic cursor —
//     EventSource has no header seam). Zero, malformed, or over-2^53 values
//     mean no resume, whatever the source.
//   - The pre-check computes the gap verdict against the ring bounds before
//     subscribe; a covered cursor is handed to the hub for replay, a gapped
//     or invalid one is STRIPPED so no partial replay can precede the
//     verdict.
//   - Exactly one `epoch` frame per connection is written via OnConnect —
//     after any replay, before live delivery, with no id field — carrying
//     {boot_id, gap, head} in the same {type,data} envelope Publish uses.
//
// Admission — the client cap and the shutdown-drain refusal (both 503 with
// the standard webhttp error envelope) — is enforced by the sse hub
// atomically at subscribe time. Headers, keepalives, replay, and frame
// encoding are the sse library's.
func Handle(bus *EventBus, w http.ResponseWriter, r *http.Request) {
	lastID, derived := resolveCursor(r)
	floor, head := bus.hub.Bounds()
	// Ordered disjuncts: the first keeps head-lastID from underflowing, the
	// cursor cap keeps lastID+1 from overflowing.
	gap := lastID > 0 && (lastID > head || lastID+1 < floor || head-lastID > ReplayBudget)

	// The strip follows the verdict whatever the cursor's source: a covered
	// cursor rides the header into the hub's replay (injected for a derived
	// request), a gapped or invalid one is removed so the client sees the
	// verdict before any frame. The caller's request is left unchanged.
	serveReq := r
	switch {
	case gap || lastID == 0:
		if r.Header.Get("Last-Event-ID") != "" {
			serveReq = r.Clone(r.Context())
			serveReq.Header.Del("Last-Event-ID")
		}
	case derived:
		serveReq = r.Clone(r.Context())
		serveReq.Header.Set("Last-Event-ID", strconv.FormatUint(lastID, 10))
	}

	bus.hub.Serve(w, serveReq, sse.OnConnect(func(sw *sse.Writer, b sse.ReplayBounds) error {
		data, err := json.Marshal(Event{Type: Epoch, Data: EpochEvent{
			BootID: bus.bootID,
			Head:   b.Head,
			Gap:    gap,
		}})
		if err != nil {
			return err
		}
		// id 0 omits the id: field — the epoch must never become a cursor.
		return sw.Event(0, string(Epoch), data)
	}))
}

// resolveCursor returns the connection's effective resume cursor (0 = no
// resume) and whether it was derived from the ?last_id= query. The header
// wins whenever present; the query is consulted only when the header is
// absent, so a native EventSource retry's header outranks a stale synthetic
// cursor in the recreate URL.
func resolveCursor(r *http.Request) (id uint64, derived bool) {
	if raw := r.Header.Get("Last-Event-ID"); raw != "" {
		return parseCursor(raw), false
	}
	if q := parseCursor(r.URL.Query().Get("last_id")); q > 0 {
		return q, true
	}
	return 0, false
}

// parseCursor parses one cursor value: positive decimal <= 2^53-1; anything
// else (zero, malformed, larger) is 0 — no resume.
func parseCursor(raw string) uint64 {
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || n == 0 || n > maxEventID {
		return 0
	}
	return n
}
