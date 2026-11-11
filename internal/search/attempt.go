package search

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/timing"
)

// The attempt-row spellings (CR-SE-10). Each holds its candidate in the
// attempt memory but RefusalFetchFailed and RefusalWriteFailed.
const (
	// refusalNone: delivered, or staged and not chosen.
	refusalNone              = ""
	RefusalDifferentCut      = "different_cut"
	RefusalFormatUnsupported = "format_unsupported"
	// RefusalDifferentEpisode: Align refused a pack member chosen by its
	// number, so the guard measured another episode (8.11).
	RefusalDifferentEpisode = "different_episode"
	// RefusalEpisodeCount and RefusalOrderUndetermined: an automatic run
	// refused a pack by 8.11's count rule, or for an order no title or
	// reference settles.
	RefusalEpisodeCount      = "episode_count"
	RefusalOrderUndetermined = "order_undetermined"
	// RefusalFetchGone: a NotFound or Permanent fetch failure.
	RefusalFetchGone = "fetch_gone"
	// RefusalFetchFailed: a Transient, RateLimited or Auth fetch failure,
	// or an admitted attempt that ended before its payload was judged.
	RefusalFetchFailed = "fetch_failed"
	// RefusalWriteFailed: the writer refused the attempt it was to deliver
	// (mediawrite and statedb write it).
	RefusalWriteFailed = "write_failed"
)

// remembered reports whether an attempt row of refusal keeps its candidate
// from being fetched again within attemptMemory.
func remembered(refusal string) bool {
	return refusal != RefusalFetchFailed && refusal != RefusalWriteFailed
}

const (
	// maxAttempts is how many candidates one pair's run fetches.
	maxAttempts = 3
	// endedRecordTimeout bounds the write of an attempt its context ended.
	endedRecordTimeout = 5 * time.Second
)

// runReport is what a row's run tells the drainer: whether its instance
// was gone, and why each missing pair it delivered nothing for was skipped.
type runReport struct {
	skips []string
	gone  bool
}

// work runs w up to the pick, then each wanted pair's attempts in the
// wants' order (8.6). Its error is a store error, ctx's, or a writer's
// mediawrite.ErrPathPending, ErrExpectation or ErrRootUnavailable, which
// the drainer defers on.
func (e *Engine) work(ctx context.Context, w statedb.Work) (runReport, error) {
	r, picks, err := e.toPick(ctx, w)
	switch {
	case errors.Is(err, errInstanceGone):
		return runReport{gone: true}, nil
	case err != nil || r == nil:
		return runReport{}, err
	}
	var rep runReport
	for i := range picks {
		t := &trial{r: r, want: picks[i].want, class: providercalls.Automatic, id: newRunID()}
		skip, err := e.attempt(ctx, t, &picks[i])
		if err != nil {
			return runReport{}, err
		}
		if skip != "" {
			rep.skips = append(rep.skips, skip)
		}
	}
	return rep, nil
}

// trial is one pair's attempts in one run: its RunID and the attempts
// fetched so far, in order, none delivered. A manual trial is a person's
// download: Interactive, numbered, locked and superseding nothing.
type trial struct {
	r    *run
	want *want
	// refusedBy is the funnel's refusal of the last call it did not admit.
	refusedBy error
	rows      []statedb.Download
	// names are the gated member names of the archive being chosen from,
	// as 8.11 reads them.
	names []string
	// item indexes r.items: the item an archive's member is chosen for.
	item   int
	class  providercalls.Class
	id     statedb.RunID
	manual bool
}

// outcome is how one attempt ended, short of a run error.
type outcome uint8

const (
	// notAdmitted: the funnel refused the call; no row, no trial used.
	notAdmitted outcome = iota
	// refused: its row carries a refusal.
	refused
	// staged: fetched and aligned, and not deliverable over the held file.
	staged
	delivered
)

func newRunID() statedb.RunID {
	var id statedb.RunID
	_, _ = rand.Read(id[:]) // crypto/rand.Read never fails (Go 1.24, crypto/rand docs)
	return id
}

// attempt fetches p's candidates in rank order until one is delivered or
// maxAttempts were admitted, skipping one this run fetched for another
// pair. Delivering nothing, it records the attempts and notes why, and for
// a missing pair answers why it was skipped.
func (e *Engine) attempt(ctx context.Context, t *trial, p *pick) (string, error) {
	for _, j := range p.ranked {
		if len(t.rows) == maxAttempts {
			break
		}
		if t.r.fetched[j.key] {
			continue
		}
		o, err := e.try(ctx, t, j)
		switch {
		case errors.Is(err, mediawrite.ErrNameContended), errors.Is(err, mediawrite.ErrOutsideRoot):
			e.log.Warn("search: delivery refused, pair skipped", "instance", t.r.inst.ID, "target", t.want.target, "err", err)
			return t.skipped(writeSkip(err)), nil
		case err != nil:
			return "", err
		case o == delivered:
			return "", nil
		}
	}
	return e.settle(ctx, t, p)
}

// writeSkip spells a writer refusal that skips the pair.
func writeSkip(err error) string {
	if errors.Is(err, mediawrite.ErrOutsideRoot) {
		return causeOutsideRoot
	}
	return RefusalWriteFailed
}

// skipped is reason for a missing pair, "" for an upgrade, whose pair a
// file still serves.
func (t *trial) skipped(reason string) string {
	if t.want.cur != nil {
		return ""
	}
	return reason
}

// try is one candidate's attempt. A call the funnel refuses uses no trial
// and writes no row; an admitted one adds its row to t, or records t's rows
// itself when ctx ended or Align failed outright, and Deliver records them
// when it is called. Its error is ctx's, a store's or Deliver's.
func (e *Engine) try(ctx context.Context, t *trial, j *judged) (outcome, error) {
	payload, admitted, err := j.from.fetch(ctx, t.class, j.cand.Ref)
	if !admitted {
		t.refusedBy = err
		return notAdmitted, ctx.Err()
	}
	t.r.fetched[j.key] = true
	row := t.row(j)
	switch {
	case ctx.Err() != nil:
		return refused, e.ended(ctx, t, &row, ctx.Err())
	case err != nil:
		row.Refusal = fetchRefusal(err)
		e.log.Info("search: fetch failed", "instance", row.Inst, "provider", row.Provenance.Provider, "release", row.Provenance.Release, "refusal", row.Refusal, "err", err)
		t.rows = append(t.rows, row)
		return refused, nil
	}
	var a aligned
	err = e.alignPayload(ctx, t, j, payload, &a)
	switch {
	case ctx.Err() != nil:
		return refused, e.ended(ctx, t, &row, ctx.Err())
	case err != nil:
		return refused, e.ended(ctx, t, &row, fmt.Errorf("search: align instance %d's attempt: %w", row.Inst, err))
	}
	row.Provenance.Facts = a.facts
	if a.refusal != refusalNone {
		row.Refusal = a.refusal
		e.log.Info("search: attempt refused", "instance", row.Inst, "provider", row.Provenance.Provider, "release", row.Provenance.Release, "refusal", row.Refusal, "err", a.cause)
		t.rows = append(t.rows, row)
		return refused, nil
	}
	row.Provenance.Timing = a.Outcome
	if !t.want.deliverable(j.standing, a.Outcome.Verdict) {
		t.rows = append(t.rows, row)
		return staged, nil
	}
	if err := e.deliver(ctx, t, j, &row, &a); err != nil {
		return refused, err
	}
	return delivered, nil
}

// fetch fetches ref's payload through b's binding as class c; admitted
// reports whether the funnel started the call.
func (b *bound) fetch(ctx context.Context, c providercalls.Class, ref provider.Ref) (payload []byte, admitted bool, err error) {
	err = b.binding.Call(ctx, providercalls.Fetch, c, func(ctx context.Context) error {
		admitted = true
		var ferr error
		payload, ferr = b.adapter.Fetch(ctx, ref)
		return ferr
	})
	return payload, admitted, err
}

// fetchRefusal spells an admitted fetch's failure.
func fetchRefusal(err error) string {
	if f := (*provider.Failure)(nil); errors.As(err, &f) && (f.Kind == provider.NotFound || f.Kind == provider.Permanent) {
		return RefusalFetchGone
	}
	return RefusalFetchFailed
}

// row is j's attempt row as fetched, its refusal and timing still unset.
func (t *trial) row(j *judged) statedb.Download {
	c := j.cand
	return statedb.Download{
		At: time.Now(), Target: t.want.target, Variant: t.want.variant, Items: t.r.inst.Items,
		Inst: t.r.inst.ID, Run: t.id, Attempt: len(t.rows) + 1, Candidate: j.key,
		Provenance: statedb.Provenance{
			Provider: string(j.from.entry.ID), Release: c.Release, Facts: j.facts, MatchedBy: c.MatchedBy,
			Fit: j.fit, MachineTranslated: c.MachineTranslated, Manual: t.manual,
		},
	}
}

// ended records t's rows with row as RefusalFetchFailed, so a replay may
// fetch it again, under a context cause cannot end, and returns cause, or
// the store's error when the write failed.
func (e *Engine) ended(ctx context.Context, t *trial, row *statedb.Download, cause error) error {
	row.Refusal = RefusalFetchFailed
	t.rows = append(t.rows, *row)
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), endedRecordTimeout)
	defer cancel()
	if err := e.deps.Store.RecordAttempts(wctx, t.rows); err != nil {
		return fmt.Errorf("search: record instance %d's ended attempts: %w", row.Inst, err)
	}
	return cause
}

// settle closes a pair that delivered nothing: it records t's attempts,
// then notes why on the pair, 8.5's skip when nothing ranked, else the
// last refusal when every attempt was refused for its payload. A fetch
// failure, a staged attempt or a pair whose best candidate was already
// tried notes nothing, keeping the note an earlier run left. Its answer
// is the missing pair's skip: the note, else the last attempt's spelling,
// else the earlier note, else no_candidate.
func (e *Engine) settle(ctx context.Context, t *trial, p *pick) (string, error) {
	inst := t.r.inst.ID
	if len(t.rows) > 0 {
		if err := e.deps.Store.RecordAttempts(ctx, t.rows); err != nil {
			return "", fmt.Errorf("search: record instance %d's attempts: %w", inst, err)
		}
	}
	refusal := t.refusal()
	if len(t.rows) == 0 && p.skip != None {
		b, err := p.skip.MarshalText()
		if err != nil {
			return "", err
		}
		refusal = string(b)
	}
	if refusal != "" {
		if err := e.deps.Store.NoteRefusal(ctx, inst, t.want.target, t.want.variant, refusal); err != nil {
			return "", fmt.Errorf("search: note instance %d's refusal: %w", inst, err)
		}
	}
	reason := cmp.Or(refusal, t.lastRow(), t.want.lastRefusal, whyNotText[NoCandidate])
	return t.skipped(reason), nil
}

// lastRow is the last attempt's refusal, "" for none.
func (t *trial) lastRow() string {
	if len(t.rows) == 0 {
		return ""
	}
	return t.rows[len(t.rows)-1].Refusal
}

// refusal is the last row's spelling when every row is a payload refusal,
// else "".
func (t *trial) refusal() string {
	for i := range t.rows {
		switch t.rows[i].Refusal {
		case RefusalDifferentCut, RefusalFormatUnsupported, RefusalDifferentEpisode, RefusalEpisodeCount, RefusalOrderUndetermined:
		default:
			return ""
		}
	}
	if len(t.rows) == 0 {
		return ""
	}
	return t.rows[len(t.rows)-1].Refusal
}

// deliverable reports whether an attempt of standing st aligned to v is
// delivered: one delivery per pair, never a confidence retry, but over an
// Unverified file only a verified or trusted result, and over one no
// reference can time only a strict gain or a verified or trusted result.
func (w *want) deliverable(st standing, v timing.Verdict) bool {
	verified := v == timing.Trusted || v == timing.Verified
	switch {
	case w.anyUntried:
		return verified
	case w.gainOrClose:
		return verified || compareStanding(st, *w.cur) < 0
	}
	return true
}
