// Package scan runs one loop per configured source: the whole passes and
// change batches that bring a source's library into statedb through its pass
// protocol (design 5). It is the one owner of the source:<id> and
// source_overlap:<id> alerts, so no other reader can disagree about a
// source's health.
package scan

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// sources is scan's subset of *mediasource.Set.
type sources interface {
	Sources() []media.SourceID
	Capabilities(media.SourceID) (mediasource.Capabilities, bool)
	Items(context.Context, media.SourceID, func(mediasource.Root, []mediasource.Item) error, mediasource.Unread) error
	Changes(context.Context, media.SourceID, mediasource.Cursor) ([]mediasource.Change, mediasource.Cursor, error)
	Root(context.Context, media.SourceID, mediasource.NativeRef) (mediasource.Root, []mediasource.Item, bool, error)
	RootFolders(context.Context, media.SourceID) ([]string, error)
}

// passes is scan's subset of *statedb.Store.
type passes interface {
	SourceCursor(context.Context, media.SourceID) (mediasource.Cursor, bool, error)
	BeginPass(context.Context, media.SourceID, statedb.PassScope) (statedb.PassID, error)
	ObserveItems(context.Context, media.SourceID, statedb.PassID, mediasource.Root, []mediasource.Item) error
	FinishPass(ctx context.Context, src media.SourceID, pass statedb.PassID, complete bool, next mediasource.Cursor, keep []mediasource.NativeRef) ([]media.SourceID, error)
}

// jobs is scan's subset of *status.Store.
type jobs interface {
	Keyed(ctx context.Context, owner int64, k status.JobKey) (status.Job, bool, error)
	Begin(context.Context, status.Job, func()) (status.Job, bool, error)
	Update(status.JobID, func(*status.Job))
	Reconcile(status.AlertSource, bool, string)
}

var (
	_ sources = (*mediasource.Set)(nil)
	_ passes  = (*statedb.Store)(nil)
	_ jobs    = (*status.Store)(nil)
)

// Deps is what a Scanner runs over. Logger nil is slog.Default().
type Deps struct {
	Store    passes
	Status   jobs
	Logger   *slog.Logger
	Schedule mediasource.Schedule
}

// The fixed parts of the schedule (design 5); the change and whole
// intervals are the scan section's.
const (
	startupDelay   = 30 * time.Second
	retryMin       = time.Minute
	retryMax       = 30 * time.Minute
	finishTimeout  = 5 * time.Second
	changesTimeout = 30 * time.Second
	probeTimeout   = 10 * time.Second
)

// Scanner owns one loop per source of its Set.
type Scanner struct {
	store  passes
	status jobs
	log    *slog.Logger

	set     sources
	metrics metrics
	loops   map[media.SourceID]*loop
	// reloads hands Run a Reload while it runs; exited is closed once Run
	// stops taking them.
	reloads chan reload
	exited  chan struct{}
	wg      sync.WaitGroup
	mu      sync.Mutex
	sched   mediasource.Schedule
	running bool
}

// New builds a Scanner over set; Run starts its loops. A nil Store, Status
// or set, or a schedule interval that is not positive, is a wiring error and
// panics.
func New(d Deps, set sources) *Scanner {
	if d.Store == nil || d.Status == nil || set == nil {
		panic("scan: New needs a Store, a Status and a Set")
	}
	if d.Schedule.Change <= 0 || d.Schedule.Whole <= 0 {
		panic("scan: New needs positive schedule intervals")
	}
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	s := &Scanner{
		store: d.Store, status: d.Status, log: log, set: set, sched: d.Schedule,
		loops: map[media.SourceID]*loop{}, reloads: make(chan reload), exited: make(chan struct{}),
		metrics: newMetrics(),
	}
	s.metrics.interval(d.Schedule)
	for _, id := range set.Sources() {
		s.loops[id] = newLoop(s, id, startupDelay)
	}
	return s
}

// Run owns one loop per source until ctx ends, then waits for each loop's
// pass to finish and returns nil. A second call is an error.
func (s *Scanner) Run(ctx context.Context) error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return errors.New("scan: Run called twice")
	}
	s.running = true
	for _, l := range s.loops {
		s.start(ctx, l)
	}
	s.mu.Unlock()
	for {
		select {
		case <-ctx.Done():
			close(s.exited)
			s.wg.Wait()
			return nil
		case r := <-s.reloads:
			s.apply(ctx, r)
			close(r.done)
		}
	}
}

// start runs l under its own context, so Reload can stop it alone.
func (s *Scanner) start(ctx context.Context, l *loop) {
	lctx, cancel := context.WithCancel(ctx)
	l.cancel, l.done = cancel, make(chan struct{})
	s.wg.Go(func() {
		defer close(l.done)
		l.run(lctx)
	})
}

func (s *Scanner) current() (sources, mediasource.Schedule) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set, s.sched
}

// source answers id's loop and capabilities; false when the Set in force
// does not hold it.
func (s *Scanner) source(id media.SourceID) (*loop, mediasource.Capabilities, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l, ok := s.loops[id]
	if !ok {
		return nil, mediasource.Capabilities{}, false
	}
	caps, ok := s.set.Capabilities(id)
	return l, caps, ok
}
