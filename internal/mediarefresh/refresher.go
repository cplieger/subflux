// Package mediarefresh tells the sources that can rescan that a root's
// subtitle files changed: one sink per source reads statedb's change feed
// from its own cursor and rescans the roots it owns, so a write never waits
// on a source and an outage delays a rescan without losing it.
package mediarefresh

import (
	"context"
	"log/slog"
	"sync"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/statedb"
)

// feed is mediarefresh's subset of *statedb.Store.
type feed interface {
	Changed() <-chan struct{}
	Since(ctx context.Context, epoch [16]byte, after uint64) (statedb.Delta, error)
	RootTargets(ctx context.Context, root media.RootKey) ([]statedb.RootTarget, error)
}

// sources is mediarefresh's subset of *mediasource.Set; Ping tells an
// outage from a root's own failure after a transient Rescan error.
type sources interface {
	Sources() []media.SourceID
	Capabilities(media.SourceID) (mediasource.Capabilities, bool)
	Rescan(context.Context, media.SourceID, mediasource.NativeRef) error
	Ping(context.Context, media.SourceID) error
}

// Deps are what a Refresher reads; a nil Logger is slog.Default().
type Deps struct {
	Store  feed
	Logger *slog.Logger
}

// Refresher runs one sink per source that can rescan, from the first
// ReloadSources on. Run it once; ReloadSources may come before or during it.
type Refresher struct {
	store feed
	log   *slog.Logger
	// wake is signalled by each ReloadSources; wakes coalesce, since Run
	// reads the latest Set.
	wake  chan struct{}
	src   sources
	sinks map[media.SourceID]*sink
	mu    sync.Mutex
}

// New is a Refresher over d with no sources.
func New(d Deps) *Refresher {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Refresher{store: d.Store, log: log, wake: make(chan struct{}, 1), sinks: map[media.SourceID]*sink{}}
}

// Run owns the sinks until ctx ends, then waits for each to return; it
// returns nil. A sink starts at the feed's head.
func (r *Refresher) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-r.wake:
			r.startSinks(ctx, &wg)
		}
	}
}

// ReloadSources hands the sinks src, the latest call winning. A sink whose
// source src still lists as able to rescan calls src from its next call,
// keeping its cursor, pending roots and retry state; every other sink is
// cancelled, starting no source call afterwards; a source new to src gets a sink
// once Run sees the call.
func (r *Refresher) ReloadSources(src sources) {
	r.mu.Lock()
	r.src = src
	for id, s := range r.sinks {
		if canRescan(src, id) {
			s.use(src)
			continue
		}
		s.stop()
		delete(r.sinks, id)
	}
	r.mu.Unlock()
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// startSinks starts a sink for each source of the latest Set that can
// rescan and has none.
func (r *Refresher) startSinks(ctx context.Context, wg *sync.WaitGroup) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.src == nil {
		return
	}
	for _, id := range r.src.Sources() {
		if _, running := r.sinks[id]; running || !canRescan(r.src, id) {
			continue
		}
		sctx, cancel := context.WithCancel(ctx)
		s := &sink{store: r.store, log: r.log.With("source", id), id: id, src: r.src, stop: cancel}
		r.sinks[id] = s
		wg.Go(func() { s.run(sctx) })
	}
}

func canRescan(src sources, id media.SourceID) bool {
	c, ok := src.Capabilities(id)
	return ok && c.Rescan
}
