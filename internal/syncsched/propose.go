package syncsched

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// ProposeRequest asks for one file's alignment by Method, MethodReference
// or Audio, for the owner status.Job.Owner names.
type ProposeRequest struct {
	Key    *status.JobKey
	File   mediawrite.FileRef
	Expect mediawrite.Expectation
	Owner  int64
	Method timing.Method
}

// Proposal is a Propose job's result: the fit's Timing, below the gate
// included, against the file's baseline, and its settled Outcome. api
// applies it with Writer.Retime and that Outcome unchanged, so a below-gate
// proposal stays Unverified.
type Proposal struct {
	Timing  timing.Timing
	Ended   time.Time
	Job     status.JobID
	File    mediawrite.FileRef
	Outcome timing.Outcome
	Expect  mediawrite.Expectation
}

// Propose starts a Sync job aligning req.File by req.Method once Current
// confirms req.Expect and the bounds admit it, so a stale file, a full
// bound (ErrSyncCapacity) or a status refusal answers before any job is
// visible. A keyed resend, or an unexpired proposal of the same owner, file,
// expectation and method, returns that job. The job settles with the file's
// forced flag; a Rigid file aligns up to Linear, and an audio request on it
// ends with cause different_cut before it runs. A job that settles a fit
// holds its Proposal; one ending on any other cause holds none.
func (a *Aligner) Propose(ctx context.Context, req ProposeRequest) (status.Job, error) { //nolint:gocritic // hugeParam: the request is the caller's value
	if err := validMethod(req.Method); err != nil {
		return status.Job{}, err
	}
	unlock, err := a.starts.Lock(ctx, startKey{file: req.File, owner: req.Owner, method: uint8(req.Method)})
	if err != nil {
		return status.Job{}, err
	}
	defer unlock()
	if job, ok, kerr := a.keyed(ctx, req.Owner, req.Key); kerr != nil || ok {
		return job, kerr
	}
	timed, err := a.writer.Current(ctx, req.File, req.Expect)
	if err != nil {
		return status.Job{}, err
	}
	pk := proposalKey{file: req.File, expect: req.Expect, owner: req.Owner, method: req.Method}
	job, st, replayed, err := a.begin(ctx, status.Job{Kind: status.Sync, Owner: req.Owner, Key: req.Key, Scope: req.File.Name(), Total: 1}, &pk)
	if err != nil || replayed {
		return job, err
	}
	a.start(job, st, func(ctx context.Context, running func()) ending { return a.propose(ctx, job.ID, &pk, &timed, running) })
	return job, nil
}

// Proposal is owner's result of Propose job id for ProposalTTL after the
// job ended; false for another owner's job, one still running, one that
// holds none, or one expired.
func (a *Aligner) Proposal(owner int64, id status.JobID) (Proposal, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ledger.proposal(owner, id, time.Now())
}

func (a *Aligner) propose(ctx context.Context, id status.JobID, pk *proposalKey, timed *mediawrite.Timed, running func()) ending {
	if pk.method == timing.Audio && timed.Rigid {
		a.progress(id, 1)
		return ending{outcome: status.Failed, cause: causeDifferentCut}
	}
	p := passOf(pk.file, timed, pk.method, originProposal, running)
	r, err := a.runPass(ctx, classInteractive, &p)
	if ctx.Err() != nil {
		return ending{}
	}
	a.progress(id, 1)
	switch {
	case err != nil:
		return ending{outcome: status.Failed, cause: causeOf(err)}
	case r.fit.Method == timing.MethodNone:
		return ending{outcome: status.Failed, cause: emptyCause(&r, pk.method)}
	}
	held := Proposal{Job: id, File: pk.file, Expect: pk.expect, Timing: r.timing, Outcome: r.outcome(decisionOf(timed)), Ended: time.Now()}
	a.mu.Lock()
	a.ledger.keep(&held)
	a.mu.Unlock()
	return ending{outcome: status.Succeeded}
}
