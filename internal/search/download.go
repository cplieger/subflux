package search

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/providercalls"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
)

// ErrVariantUnserved is a variant the handle's target does not list or its
// candidate's flags do not serve.
var ErrVariantUnserved = errors.New("search: variant not served by this candidate")

// The Download job causes beyond the attempt refusals.
const (
	causeProviderFailed       = "provider_failed"
	causeSettingsChanged      = "settings_changed"
	causeMediaRootUnavailable = "media_root_unavailable"
	causeOutsideRoot          = "outside_root"
	causeWriteFailed          = RefusalWriteFailed
)

// DownloadCauses is every cause a Download job ends with, in its wire
// spelling.
var DownloadCauses = []string{
	RefusalFormatUnsupported, RefusalDifferentCut, RefusalDifferentEpisode, causeProviderFailed,
	causeSettingsChanged, causeMediaRootUnavailable, causeOutsideRoot, causeWriteFailed,
}

// Download starts a Download job fetching h's candidate for variant. With
// idem set, under Starts' lock on (owner, idem), the job started under that
// key is returned and h left alone; otherwise h is taken, and given back
// when the variant is not served or the job cannot be recorded. The job
// runs under the engine, never ctx, in the Item group under the instance's
// gate, and on a drained engine it ends Interrupted at once.
func (e *Engine) Download(ctx context.Context, owner int64, h Handle, v language.Variant, idem *status.JobKey) (status.Job, error) {
	unlock, err := e.deps.Starts.lock(ctx, owner, idem)
	if err != nil {
		return status.Job{}, err
	}
	defer unlock()
	if idem != nil {
		if j, ok, kerr := e.deps.Status.Keyed(ctx, owner, *idem); kerr != nil || ok {
			return j, kerr
		}
	}
	c, ok := e.handles.take(owner, h, time.Now())
	if !ok {
		return status.Job{}, ErrCandidateUnknown
	}
	if !slices.Contains(c.variants, v) || !c.flags.Serves(v) {
		e.handles.giveBack(c)
		return status.Job{}, ErrVariantUnserved
	}
	job := status.Job{Kind: status.Download, Owner: owner, Key: idem, Scope: strconv.FormatUint(uint64(c.inst), 10)}
	j, replayed, err := e.begin(ctx, job, nil)
	if err != nil || replayed {
		e.handles.giveBack(c)
		return j, err
	}
	e.handles.consume(h)
	return e.queue(ctx, j, c.inst, "downloading", func(ctx context.Context) (status.Outcome, string) {
		return e.downloadOnce(ctx, c, v)
	})
}

// queue runs job j as a drainer-owned goroutine of the Item group, under
// the engine's context and never ctx's cancellation, and returns it queued:
// once a slot and inst's gate are free it is Running in phase and settled
// with what run answers. On a drained engine j ends Interrupted at once.
func (e *Engine) queue(ctx context.Context, j status.Job, inst statedb.InstanceID, phase string, run func(context.Context) (status.Outcome, string)) (status.Job, error) { //nolint:gocritic // hugeParam: a Job is returned by value
	jctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	e.mu.Lock()
	if e.draining {
		e.mu.Unlock()
		cancel()
		e.end(j.ID, status.Interrupted, causeSettingsChanged)
		return e.job(ctx, j)
	}
	e.jobs[j.ID] = cancel
	e.deps.Status.Update(j.ID, func(j *status.Job) { j.Phase = "queued" })
	j.Phase = "queued"
	e.items.Go(func() error {
		defer e.forget(j.ID)
		e.runJob(jctx, j.ID, inst, phase, run)
		return nil
	})
	e.mu.Unlock()
	return j, nil
}

// job is j as the status store now holds it, j itself when it cannot say.
func (e *Engine) job(ctx context.Context, j status.Job) (status.Job, error) { //nolint:gocritic // hugeParam: a Job is returned by value
	if got, ok, err := e.deps.Status.Get(ctx, j.ID); err == nil && ok {
		return got, nil
	}
	return j, nil
}

// forget drops a finished job's cancel.
func (e *Engine) forget(id status.JobID) {
	e.mu.Lock()
	cancel := e.jobs[id]
	delete(e.jobs, id)
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// end settles job id Done with outcome and cause.
func (e *Engine) end(id status.JobID, o status.Outcome, cause string) {
	e.deps.Status.Update(id, func(j *status.Job) { j.State, j.Outcome, j.Cause = status.Done, o, cause })
}

// runJob is a queued job's run: an Item slot, inst's gate, then run.
func (e *Engine) runJob(ctx context.Context, id status.JobID, inst statedb.InstanceID, phase string, run func(context.Context) (status.Outcome, string)) {
	select {
	case e.slots <- struct{}{}:
		defer func() { <-e.slots }()
	case <-ctx.Done():
		e.end(id, status.Interrupted, causeSettingsChanged)
		return
	}
	unlock, err := e.gate.Lock(ctx, inst)
	if err != nil {
		e.end(id, status.Interrupted, causeSettingsChanged)
		return
	}
	defer unlock()
	e.deps.Status.Update(id, func(j *status.Job) { j.State, j.Phase = status.Running, phase })
	o, cause := run(ctx)
	e.end(id, o, cause)
}

// downloadOnce runs c's one attempt and answers the job's end.
func (e *Engine) downloadOnce(ctx context.Context, c *held, v language.Variant) (o status.Outcome, cause string) {
	r, ok := e.downloadRun(ctx, c.inst)
	switch {
	case ctx.Err() != nil:
		return status.Interrupted, causeSettingsChanged
	case !ok:
		return status.Failed, causeWriteFailed
	}
	t := &trial{r: r, want: &want{target: c.target, variant: v}, class: providercalls.Interactive, id: newRunID(), manual: true}
	cand := provider.Candidate{Ref: c.ref, Lang: c.lang, Release: c.release, Flags: c.flags, MatchedBy: c.matched, MachineTranslated: c.machine}
	j := &judged{offer: &offer{from: c.from, cand: &cand, facts: c.facts, fit: c.fit, key: c.key}}
	end, err := e.try(ctx, t, j)
	switch {
	case err == nil && end == delivered:
		return status.Succeeded, ""
	case ctx.Err() != nil:
		if err != nil && !errors.Is(err, ctx.Err()) {
			e.log.Error("search: a drained download's attempt was not recorded", "instance", c.inst, "err", err)
		}
		return status.Interrupted, causeSettingsChanged
	case err != nil:
		return status.Failed, e.writeCause(c.inst, err)
	case end == notAdmitted && errors.Is(t.refusedBy, providercalls.ErrReleased):
		return status.Interrupted, causeSettingsChanged
	case end == notAdmitted:
		return status.Failed, causeProviderFailed
	}
	return status.Failed, e.refusedCause(ctx, t)
}

// downloadRun reads the instance a download delivers for, and for an
// episode its series as 8.11 reads it; false when it is gone or cannot be
// read.
func (e *Engine) downloadRun(ctx context.Context, inst statedb.InstanceID) (*run, bool) {
	in, found, err := e.deps.Store.Instance(ctx, inst)
	if err == nil && found && len(in.Items) > 0 {
		r := &run{now: time.Now(), inst: in, cause: statedb.Item, fetched: map[[32]byte]bool{}}
		if err = r.readItems(ctx, e); err == nil && len(r.items) > 0 {
			if err = r.readSeries(ctx, e); err == nil {
				return r, true
			}
		}
	}
	if err != nil && ctx.Err() == nil {
		e.log.Error("search: a download could not read its instance", "instance", inst, "err", err)
	}
	return nil, false
}

// refusedCause records a refused download's attempt and answers its cause:
// a fetch failure is the provider's, any other refusal its own spelling.
func (e *Engine) refusedCause(ctx context.Context, t *trial) string {
	if err := e.deps.Store.RecordAttempts(ctx, t.rows); err != nil {
		e.log.Error("search: a download's attempt was not recorded", "instance", t.r.inst.ID, "err", err)
	}
	switch refusal := t.rows[len(t.rows)-1].Refusal; refusal {
	case RefusalFetchGone, RefusalFetchFailed:
		return causeProviderFailed
	default:
		return refusal
	}
}

// writeCause is the cause of a download the writer refused.
func (e *Engine) writeCause(inst statedb.InstanceID, err error) string {
	switch {
	case errors.Is(err, mediawrite.ErrRootUnavailable):
		return causeMediaRootUnavailable
	case errors.Is(err, mediawrite.ErrOutsideRoot):
		return causeOutsideRoot
	}
	e.log.Warn("search: a download was not written", "instance", inst, "err", err)
	return causeWriteFailed
}
