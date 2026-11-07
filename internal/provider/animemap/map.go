// Package animemap is what the anime adapters share: animap's anime map,
// read through mapread and refreshed on demand once per process, its alert,
// the landing lookups over it, and the one reader of what an anime release's
// file name claims.
package animemap

import (
	"context"
	"sync"
	"time"

	"github.com/cplieger/animap/mapread"
	"github.com/cplieger/subflux/internal/provider"
)

// Map holds one process's anime map: the index, its tag and its refresh
// state, shared by every adapter an engine builds and kept across engine
// swaps. The zero Map is not usable; New makes one.
type Map struct {
	good      time.Time // the last good refresh: the held tag confirmed or a new one indexed
	holdUntil time.Time
	index     *mapread.Index
	refused   map[string]error // a tag whose Fetch failed permanently, and why
	reports   map[string]func(text string, active bool)
	flight    chan struct{} // closed when the refresh in flight ends
	cond      condition
	held      string
	mu        sync.Mutex
	streak    bool // a failed attempt's WARN was logged since the last good one
}

// condition is the map's alert state as of the last attempt.
type condition struct {
	since  time.Time // the last good refresh, zero when none
	cause  error
	active bool
}

// New is an empty Map, which downloads nothing until an adapter asks for its
// index.
func New() *Map {
	return &Map{refused: map[string]error{}, reports: map[string]func(string, bool){}}
}

// Hosts are the hosts a refresh reaches: GitHub's release pages and its two
// release-asset hosts, which mapread's redirect policy allows.
func Hosts() []string {
	return []string{"github.com", "objects.githubusercontent.com", "release-assets.githubusercontent.com"}
}

// Index is the newest index, or nil when none was ever read. When the held
// one is missing or 6 hours old and no failed attempt holds refreshes off, it
// starts one refresh over c, the calling adapter's Client, or joins the one
// in flight, and waits for the
// earliest of its end, 10 seconds and ctx's end. The refresh runs on its own
// 2-minute timeout whatever happens to ctx, and a failed one keeps the held
// index.
func (m *Map) Index(ctx context.Context, c provider.Client) *mapread.Index {
	m.mu.Lock()
	now := time.Now()
	if m.index != nil && now.Sub(m.good) < maxAge || now.Before(m.holdUntil) {
		defer m.mu.Unlock()
		return m.index
	}
	if m.flight == nil {
		m.flight = make(chan struct{})
		go m.refresh(context.WithoutCancel(ctx), c, m.flight)
	}
	done := m.flight
	m.mu.Unlock()

	wait := time.NewTimer(callerWait)
	defer wait.Stop()
	select {
	case <-done:
	case <-wait.C:
	case <-ctx.Done():
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.index
}

// Report registers report as entry name's view of the map's condition and
// calls it at once with the current one. The Map calls it again when each
// refresh attempt ends, from the refresh's goroutine, until a later Report
// for name replaces it. report must not call back into the Map.
func (m *Map) Report(name string, report func(text string, active bool)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reports[name] = report
	report(m.cond.text(name))
}

// text is the condition as entry name's alert says it.
func (c condition) text(name string) (string, bool) {
	switch {
	case !c.active:
		return "", false
	case c.since.IsZero():
		return name + "'s anime map could not be downloaded: " + c.cause.Error(), true
	}
	return name + "'s anime map has not refreshed since " + c.since.UTC().Format(time.RFC3339) + ": " + c.cause.Error(), true
}
