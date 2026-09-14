package events

import (
	"sync"
	"time"
)

// aliveWindow is how long a tag stays present after its last acknowledged
// keepalive: two beats at the 15s keepalive, so a tag that has not
// acknowledged in two is a process that stopped receiving. A row lingers
// this long after its last socket left, which bounds the table at the
// client cap plus one window of departures.
const aliveWindow = 30 * time.Second

// Presence transition kinds, the labels of sse_presence_transitions_total.
const (
	presenceAlive   = "alive"
	presenceExpired = "expired"
)

// Presence folds the hub's connect/disconnect feed with the client's
// keepalive acknowledgements (POST /api/events/alive) per SSE-Client tag.
// Nothing reads it back; its two transition counters are the deliverable:
// alive (the first acknowledgement after a connect or after an expiry) and
// expired (a still-connected tag whose acknowledgement is older than
// aliveWindow, the count of how often the socket-only view was wrong).
// Safe for concurrent use.
type Presence struct {
	metrics Metrics
	rows    map[string]*presenceRow
	mu      sync.Mutex
}

type presenceRow struct {
	lastAliveAt time.Time
	connected   int
	acked       bool
	expired     bool
}

func newPresence(m Metrics) *Presence {
	return &Presence{metrics: m, rows: make(map[string]*presenceRow)}
}

// connected seeds lastAliveAt on a new row: the hello just completed a round
// trip, so the tag is present before its first acknowledgement.
func (p *Presence) connected(tag string, now time.Time) {
	if tag == "" {
		return
	}
	p.mu.Lock()
	r, ok := p.rows[tag]
	if !ok {
		r = &presenceRow{lastAliveAt: now}
		p.rows[tag] = r
	}
	r.connected++
	p.mu.Unlock()
}

func (p *Presence) disconnected(tag string) {
	if tag == "" {
		return
	}
	p.mu.Lock()
	if r, ok := p.rows[tag]; ok && r.connected > 0 {
		r.connected--
	}
	p.mu.Unlock()
}

// Alive records one acknowledged keepalive for tag at now. Only the hub's
// connected event mints a row, so an acknowledgement for a tag no socket has
// presented to this process records nothing (aliveWindow states the bound
// that buys). The caller has already validated the tag.
func (p *Presence) Alive(tag string, now time.Time) {
	p.mu.Lock()
	r, ok := p.rows[tag]
	if !ok {
		p.mu.Unlock()
		return
	}
	r.lastAliveAt = now
	turned := !r.acked || r.expired
	r.acked, r.expired = true, false
	p.mu.Unlock()
	if turned {
		p.metrics.RecordSSEPresenceTransition(presenceAlive)
	}
}

// Sweep drops rows with no socket whose last acknowledgement is older than
// aliveWindow and counts one expired transition for a connected row that
// crossed the window since the last sweep. Driven by the server's prune
// ticker.
func (p *Presence) Sweep(now time.Time) {
	expired := 0
	p.mu.Lock()
	for tag, r := range p.rows {
		stale := now.Sub(r.lastAliveAt) > aliveWindow
		switch {
		case r.connected == 0 && stale:
			delete(p.rows, tag)
		case r.connected > 0 && stale && !r.expired:
			r.expired = true
			expired++
		}
	}
	p.mu.Unlock()
	for range expired {
		p.metrics.RecordSSEPresenceTransition(presenceExpired)
	}
}
