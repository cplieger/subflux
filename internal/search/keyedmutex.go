package search

import "sync"

// mediaGate serializes engine work per media item. SearchTargets is reached
// concurrently by the scheduled scan, the history poller, and manual scans;
// two of them processing the SAME item at once means duplicate provider
// queries and competing subtitle-state writes. Keyed by (media_type,
// media_id) rather than a global lock, so unrelated items never contend.
type mediaGate struct {
	locks map[string]*gateEntry
	mu    sync.Mutex
}

type gateEntry struct {
	mu   sync.Mutex
	refs int
}

func newMediaGate() *mediaGate {
	return &mediaGate{locks: make(map[string]*gateEntry)}
}

// lock acquires the per-key mutex, blocking while another holder runs, and
// returns the release func. Entries are reference-counted and removed at
// zero, so the map stays bounded by in-flight work, not library size.
func (g *mediaGate) lock(key string) (unlock func()) {
	g.mu.Lock()
	e, ok := g.locks[key]
	if !ok {
		e = &gateEntry{}
		g.locks[key] = e
	}
	e.refs++
	g.mu.Unlock()

	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		g.mu.Lock()
		e.refs--
		if e.refs == 0 {
			delete(g.locks, key)
		}
		g.mu.Unlock()
	}
}
