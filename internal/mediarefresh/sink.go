package mediarefresh

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/statedb"
)

// The tick (design section 6).
const (
	// debounce follows a feed wake, so a season pack arrives as one batch.
	debounce      = 5 * time.Second
	rescanTimeout = 30 * time.Second
	pingTimeout   = 10 * time.Second
	// maxAttempts counts a root's transient failures while its source
	// answers Ping; the last one skips that root alone.
	maxAttempts = 6
)

// sink rescans one source's roots as the feed reports them written.
type sink struct {
	store feed
	log   *slog.Logger
	src   sources
	stop  context.CancelFunc
	id    media.SourceID
	// cur is the feed position through which every written root is handled.
	cur      cursor
	retry    backoff
	attempts int
	mu       sync.Mutex
}

// cursor is a sink's position in the feed.
type cursor struct {
	epoch [16]byte
	rev   uint64
}

// batch is one Since answer's written roots not yet handled: keys still to
// resolve through RootTargets, then refs, the sink's own roots resolved and
// not rescanned yet, refs[0] in progress. Since lists each key once and a
// source root's items all key under one root, so each ref comes once.
type batch struct {
	keys []media.RootKey
	refs []mediasource.NativeRef
}

func (b *batch) done() bool { return len(b.keys) == 0 && len(b.refs) == 0 }

// resolve replaces keys[0] with the sink's own roots among its targets.
func (b *batch) resolve(id media.SourceID, targets []statedb.RootTarget) {
	b.keys = b.keys[1:]
	for _, t := range targets {
		if t.Source == id {
			b.refs = append(b.refs, t.Root)
		}
	}
}

func (s *sink) use(src sources) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.src = src
}

func (s *sink) source() sources {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src
}

// run refreshes until ctx ends. Its first Since, from the zero cursor, is
// Full, so the sink starts at the head. Changed is read before each Since,
// so a commit landing between the two still wakes it.
func (s *sink) run(ctx context.Context) {
	for {
		changed := s.store.Changed()
		d, ok := s.since(ctx)
		if !ok || !s.refresh(ctx, &d) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-changed:
		}
		if !sleep(ctx, debounce) {
			return
		}
	}
}

// since reads the feed after the cursor, retrying a failed read on the
// backoff and counting nothing; false once ctx ends.
func (s *sink) since(ctx context.Context) (statedb.Delta, bool) {
	for {
		d, err := s.store.Since(ctx, s.cur.epoch, s.cur.rev)
		if err == nil {
			s.retry.reset()
			return d, true
		}
		if !s.backOff(ctx, "reading the change feed failed", err) {
			return statedb.Delta{}, false
		}
	}
}

// refresh handles d's written roots, then moves the cursor to d's revision;
// a Full d moves it to the head and refreshes nothing, the source's own
// scan covering the gap. False once ctx ends, the cursor kept.
func (s *sink) refresh(ctx context.Context, d *statedb.Delta) bool {
	if !d.Full {
		b := batch{keys: d.Written}
		for !b.done() {
			if !s.step(ctx, &b) {
				return false
			}
		}
	}
	s.cur = cursor{epoch: d.Epoch, rev: d.Rev}
	return true
}

// step resolves b's next key, retrying a failed read on the backoff and
// counting nothing, or rescans its next ref; false once ctx ends.
func (s *sink) step(ctx context.Context, b *batch) bool {
	if ctx.Err() != nil {
		return false
	}
	if len(b.refs) > 0 {
		return s.rescan(ctx, b)
	}
	targets, err := s.store.RootTargets(ctx, b.keys[0])
	if err != nil {
		return s.backOff(ctx, "reading the targets of root "+b.keys[0].String()+" failed", err)
	}
	s.retry.reset()
	b.resolve(s.id, targets)
	return true
}

// rescan makes one attempt at b.refs[0], moving past it unless a transient
// failure keeps it for a retry; false once ctx ends.
func (s *sink) rescan(ctx context.Context, b *batch) bool {
	ref, src := b.refs[0], s.source()
	err := within(ctx, rescanTimeout, func(c context.Context) error { return src.Rescan(c, s.id, ref) })
	switch {
	case ctx.Err() != nil:
		return false
	case err == nil:
	case errors.Is(err, mediasource.ErrRescanOutcomeUnknown):
		s.log.Warn("mediarefresh: the source may have run the rescan, so it is not sent again", "root", ref.ID, "error", err)
	case !transient(err):
		s.log.Warn("mediarefresh: the rescan failed", "root", ref.ID, "error", err)
	default:
		skip, ok := s.unanswered(ctx, src, ref, err)
		if !ok || !skip {
			return ok
		}
	}
	b.refs = b.refs[1:]
	s.attempts = 0
	s.retry.reset()
	return true
}

// unanswered pings the source after ref's transient failure. A failed ping
// is an outage, counting nothing; an answered one counts one of ref's
// attempts, the last skipping it. Unless skipping, it waits the backoff. ok
// is false once ctx ends.
func (s *sink) unanswered(ctx context.Context, src sources, ref mediasource.NativeRef, err error) (skip, ok bool) {
	perr := within(ctx, pingTimeout, func(c context.Context) error { return src.Ping(c, s.id) })
	switch {
	case ctx.Err() != nil:
		return false, false
	case perr != nil:
		return false, s.backOff(ctx, "the rescan of root "+string(ref.ID)+" failed and the source does not answer", err)
	}
	s.attempts++
	if s.attempts == maxAttempts {
		s.log.Warn("mediarefresh: the rescan failed on every attempt while the source answered; the root is skipped", "root", ref.ID, "attempts", s.attempts, "error", err)
		return true, true
	}
	return false, s.backOff(ctx, "the rescan of root "+string(ref.ID)+" failed while the source answered", err)
}

// backOff logs what failed and waits the next retry delay; false once ctx
// ends, logging nothing.
func (s *sink) backOff(ctx context.Context, what string, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	d := s.retry.next()
	s.log.Warn("mediarefresh: "+what+"; retrying", "in", d, "error", err)
	return sleep(ctx, d)
}

// transient is a source's Unreachable failure, the only one a retry can
// change.
func transient(err error) bool {
	var se *mediasource.SourceError
	return errors.As(err, &se) && se.Cause == mediasource.Unreachable
}

// within runs f under ctx bounded to d.
func within(ctx context.Context, d time.Duration, f func(context.Context) error) error {
	c, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	return f(c)
}
