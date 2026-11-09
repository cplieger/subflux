package scan

import (
	"context"
	"errors"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// PassNow makes every loop's scheduled whole pass due at once, with its
// SystemOwner Scan job; a loop already walking runs one more whole pass
// after the current one, and calls while a pass is pending coalesce.
func (s *Scanner) PassNow() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range s.loops {
		l.q.markPassNow()
		signal(l.poke)
	}
}

// whole runs one whole pass made due by d, moves the loop's facts by how it
// ended (design 5.1's table), and answers what failed it.
func (l *loop) whole(ctx context.Context, set sources, caps mediasource.Capabilities, d due) error {
	passCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	pj, ok := l.beginJobs(ctx, d, cancel)
	if !ok {
		return errNoVerdict
	}
	l.s.log.Info("scan: whole pass started", "source", l.src)
	w := l.walk(passCtx, set, caps, pj)
	end := time.Now()
	switch {
	case w.err == nil:
		l.completed(&w, end)
		pj.end(status.Succeeded, "")
		l.s.log.Info("scan: whole pass complete", "source", l.src, "roots", w.roots)
		return nil
	case ctx.Err() != nil:
		pj.end(status.Interrupted, "")
		l.s.log.Info("scan: whole pass interrupted", "source", l.src)
		return errNoVerdict
	case passCtx.Err() != nil:
		l.cancelled(d, end)
		pj.end(status.Cancelled, "")
		l.s.log.Info("scan: whole pass cancelled", "source", l.src)
		return errNoVerdict
	default:
		l.failed(&w, end)
		pj.end(status.Failed, w.err.Error())
		l.s.log.Warn("scan: whole pass failed", "source", l.src, "error", w.err, "retry_in", l.backoff)
		return w.err
	}
}

// walked is what a whole pass's walk did.
type walked struct {
	err  error
	head mediasource.Cursor
	// read lists the retry set's roots the walk observed.
	read []mediasource.NativeRef
	// unread lists the roots the Set reported unread, which the pass keeps.
	unread   []unreadRoot
	roots    int
	tookHead bool
}

type unreadRoot struct {
	err *mediasource.SourceError
	ref mediasource.NativeRef
}

// walk takes the head before the pass opens, so changes made during the
// walk replay afterwards, then observes every root Items yields and
// collects every unread one. A walk with unread roots probes the source
// again: a failed probe fails the pass as an outage, else the pass completes
// with that head, keeping the unread roots. A failed walk closes the pass
// incomplete.
func (l *loop) walk(ctx context.Context, set sources, caps mediasource.Capabilities, pj *passJobs) walked {
	var w walked
	if caps.Changes {
		cctx, cancel := context.WithTimeout(ctx, changesTimeout)
		_, w.head, w.err = set.Changes(cctx, l.src, mediasource.Cursor{})
		cancel()
		if w.err != nil {
			return w
		}
		w.tookHead = true
	}
	pass, err := l.s.store.BeginPass(ctx, l.src, statedb.PassScope{})
	if err != nil {
		w.err = err
		return w
	}
	err = set.Items(ctx, l.src, func(r mediasource.Root, items []mediasource.Item) error {
		if oerr := l.s.store.ObserveItems(ctx, l.src, pass, r, items); oerr != nil {
			return oerr
		}
		if _, ok := l.retry[r.Native]; ok {
			w.read = append(w.read, r.Native)
		}
		w.roots++
		pj.progress(w.roots, 0)
		return nil
	}, func(ref mediasource.NativeRef, se *mediasource.SourceError) error {
		w.unread = append(w.unread, unreadRoot{err: se, ref: ref})
		return nil
	})
	if err == nil && len(w.unread) > 0 {
		err = l.probe(ctx, set)
	}
	if err == nil {
		var owners []media.SourceID
		if owners, err = l.s.store.FinishPass(ctx, l.src, pass, true, w.head, w.keep()); err == nil {
			l.reconcileOverlap(owners)
			return w
		}
	}
	w.err = err
	l.abandon(ctx, pass)
	return w
}

// keep is the pass's keep list, nil when every root was read.
func (w *walked) keep() []mediasource.NativeRef {
	if len(w.unread) == 0 {
		return nil
	}
	refs := make([]mediasource.NativeRef, len(w.unread))
	for i, u := range w.unread {
		refs[i] = u.ref
	}
	return refs
}

// abandon closes a failed pass incomplete on a detached context, so a
// cancelled pass still closes; storage retires nothing for it.
func (l *loop) abandon(ctx context.Context, pass statedb.PassID) {
	fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
	defer cancel()
	if _, err := l.s.store.FinishPass(fctx, l.src, pass, false, mediasource.Cursor{}, nil); err != nil {
		l.s.log.Warn("scan: cannot close a failed pass", "source", l.src, "pass", uint64(pass), "error", err)
	}
}

func (l *loop) completed(w *walked, end time.Time) {
	l.wholeFrom = end
	l.s.metrics.mark(l.src, end)
	l.backoff, l.backoffUntil = 0, time.Time{}
	l.expired, l.wholeRetry, l.wholeSourceErr = false, false, nil
	l.cur, l.haveCur = w.head, w.tookHead && !w.head.IsZero()
	for _, ref := range w.read {
		delete(l.retry, ref)
	}
	for _, u := range w.unread {
		l.retryLater(u.ref, u.err, end)
	}
}

// failed sets the retry whatever failed the pass; only a source's failure
// moves wholeSourceErr.
func (l *loop) failed(w *walked, end time.Time) {
	l.wholeRetry = true
	l.backoff = min(max(2*l.backoff, retryMin), retryMax)
	l.backoffUntil = end.Add(l.backoff)
	if se, ok := errors.AsType[*mediasource.SourceError](w.err); ok {
		l.wholeSourceErr = se
	}
	if !l.haveCur && w.tookHead && !w.head.IsZero() {
		l.cur, l.haveCur = w.head, true
	}
}

// cancelled moves the schedule only when it made the pass due; a cancelled
// retry waits out its current backoff again, undoubled, and expired stays
// set, since the source has no cursor without a whole pass.
func (l *loop) cancelled(d due, end time.Time) {
	if d.schedule {
		l.wholeFrom = end
	}
	if l.wholeRetry {
		l.backoffUntil = end.Add(l.backoff)
	}
}
