package scan

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// The batch's bounds (design 5.2).
const (
	rootTimeout = 60 * time.Second
	// batchFailBudget is the consecutive root failures, under a source that
	// still answers its probe, that end a batch, its unread roots to retry.
	batchFailBudget = 3
)

// batch reads the changes since the loop's cursor and runs the changed
// roots, less those waiting in the retry set, with the queued refetches and
// the due retry roots, one pass per root, under one Import job. A check
// finding nothing moves the in-memory cursor and writes nothing; a batch
// that did not fail moves it to the check's next, whatever each root did.
// If Begin fails, the batch waits for the next tick. It answers what failed
// the tick.
func (l *loop) batch(ctx context.Context, set sources) error {
	now := time.Now()
	roots, c, next, err := l.changed(ctx, set, now)
	if err != nil {
		return err
	}
	for _, ref := range l.dueRetries(now) {
		roots = appendNew(roots, ref)
	}
	if len(roots) == 0 && !l.q.pendingRoots() {
		if l.haveCur {
			l.cur = next
		}
		return nil
	}
	bctx, cancel := context.WithCancel(ctx)
	defer cancel()
	j, _, err := l.s.status.Begin(ctx, status.Job{Kind: status.Import, Owner: status.SystemOwner, Scope: string(l.src), Source: l.src}, cancel)
	if err != nil {
		l.s.log.Warn("scan: cannot record a batch's job; it waits for the next tick", "source", l.src, "error", err)
		return errNoVerdict
	}
	b := &batchRun{l: l, job: j.ID, reqs: l.q.takeRoots()}
	for _, ref := range sortedRefs(b.reqs) {
		roots = appendNew(roots, ref)
	}
	l.s.status.Update(b.job, func(j *status.Job) { j.State, j.Total = status.Running, len(roots) })
	o := b.run(bctx, set, roots, c)
	verdict := b.err
	switch {
	case o == status.Succeeded:
		if l.haveCur {
			l.cur = next
		}
	case ctx.Err() != nil:
		o, verdict = status.Interrupted, errNoVerdict
	case bctx.Err() != nil:
		o, verdict = status.Cancelled, errNoVerdict
	}
	b.end(o, roots)
	return verdict
}

// changed reads the roots changed since the loop's cursor, less those
// waiting in the retry set, with the cursor read from and the one after
// them; none without a cursor. Expired drops the cursor and makes a whole
// pass due.
func (l *loop) changed(ctx context.Context, set sources, now time.Time) (roots []mediasource.NativeRef, c, next mediasource.Cursor, err error) {
	if !l.haveCur {
		return nil, c, next, nil
	}
	cctx, cancel := context.WithTimeout(ctx, changesTimeout)
	changes, next, err := set.Changes(cctx, l.src, l.cur)
	cancel()
	if err != nil {
		if se, ok := errors.AsType[*mediasource.SourceError](err); ok && se.Cause == mediasource.Expired && ctx.Err() == nil {
			l.expired, l.haveCur, l.cur = true, false, mediasource.Cursor{}
			l.s.log.Info("scan: the source no longer serves its cursor; a whole pass replaces it", "source", l.src)
		}
		return nil, c, next, err
	}
	for _, ch := range changes {
		if !l.waiting(ch.Root, now) {
			roots = appendNew(roots, ch.Root)
		}
	}
	return roots, l.cur, next, nil
}

// batchRun is one batch in flight: its Import job and the refetch jobs it
// carries by root.
type batchRun struct {
	l    *loop
	reqs map[mediasource.NativeRef][]status.JobID
	err  error
	job  status.JobID
	done int
}

// run runs one pass per root, each storing c when it completes, and answers
// Succeeded when the batch did not fail. A root whose read fails while the
// source answers a fresh probe joins the retry set; the batchFailBudget-th
// in a row ends the batch, the rest joining it unread. A root unread for its
// aired order joins it with no probe and counts toward none, since the
// source answered. A failed probe, a storage error or cancellation fails the
// batch, which replays from c.
func (b *batchRun) run(ctx context.Context, set sources, roots []mediasource.NativeRef, c mediasource.Cursor) status.Outcome {
	l, fails := b.l, 0
	for i, ref := range roots {
		err := l.rootPass(ctx, set, ref, c)
		se, isSource := errors.AsType[*mediasource.SourceError](err)
		switch {
		case err == nil:
			delete(l.retry, ref)
			fails = 0
			b.endRoot(ref, status.Succeeded, "")
		case ctx.Err() != nil || !isSource:
			b.err = err
			return status.Failed
		case errors.Is(err, mediasource.ErrAiredOrderUnavailable):
			l.retryLater(ref, se, time.Now())
			b.endRoot(ref, status.Failed, se.Error())
		default:
			if perr := l.probe(ctx, set); perr != nil {
				b.err = perr
				return status.Failed
			}
			now := time.Now()
			l.retryLater(ref, se, now)
			b.endRoot(ref, status.Failed, se.Error())
			fails++
			if fails == batchFailBudget {
				for _, rest := range roots[i+1:] {
					l.deferUnread(rest, se, now)
					b.endRoot(rest, status.Failed, se.Error())
				}
				return status.Succeeded
			}
		}
	}
	return status.Succeeded
}

// endRoot ends ref's refetch jobs the batch still carries and counts the
// root done.
func (b *batchRun) endRoot(ref mediasource.NativeRef, o status.Outcome, cause string) {
	b.done++
	for _, id := range b.l.q.release(b.reqs[ref]) {
		b.l.s.status.Update(id, func(j *status.Job) { j.State, j.Outcome, j.Cause = status.Done, o, cause })
	}
	done := b.done
	b.l.s.status.Update(b.job, func(j *status.Job) { j.Done = done })
}

// end ends the Import job and every refetch job still carried with o.
func (b *batchRun) end(o status.Outcome, roots []mediasource.NativeRef) {
	cause := ""
	if o == status.Failed && b.err != nil {
		cause = b.err.Error()
		b.l.s.log.Warn("scan: change batch failed; it replays at the next tick", "source", b.l.src, "error", b.err)
	}
	for _, ref := range roots {
		for _, id := range b.l.q.release(b.reqs[ref]) {
			b.l.s.status.Update(id, func(j *status.Job) { j.State, j.Outcome, j.Cause = status.Done, o, cause })
		}
	}
	b.l.s.status.Update(b.job, func(j *status.Job) { j.State, j.Outcome, j.Cause = status.Done, o, cause })
}

func appendNew(refs []mediasource.NativeRef, ref mediasource.NativeRef) []mediasource.NativeRef {
	if slices.Contains(refs, ref) {
		return refs
	}
	return append(refs, ref)
}

// rootPass reads ref fresh as a pass scoped to it: observed when found,
// retired by the completion when not.
func (l *loop) rootPass(ctx context.Context, set sources, ref mediasource.NativeRef, c mediasource.Cursor) error {
	pass, err := l.s.store.BeginPass(ctx, l.src, statedb.PassScope{Roots: []mediasource.NativeRef{ref}})
	if err != nil {
		return err
	}
	rctx, cancel := context.WithTimeout(ctx, rootTimeout)
	root, items, found, err := set.Root(rctx, l.src, ref)
	cancel()
	if err == nil && found {
		err = l.s.store.ObserveItems(ctx, l.src, pass, root, items)
	}
	if err == nil {
		var owners []media.SourceID
		if owners, err = l.s.store.FinishPass(ctx, l.src, pass, true, c, nil); err == nil {
			l.reconcileOverlap(owners)
			return nil
		}
	}
	l.abandon(ctx, pass)
	return err
}

// probe asks the source's root folders, refreshing what MediaRoots reports.
func (l *loop) probe(ctx context.Context, set sources) error {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	_, err := set.RootFolders(pctx, l.src)
	return err
}
