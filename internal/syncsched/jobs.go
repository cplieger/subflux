package syncsched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/subsync"
	"github.com/cplieger/subflux/internal/subsync/ffmpeg"
	"github.com/cplieger/subflux/internal/timing"
)

// errStopped is a job context's cause when its owner stopped the job.
var errStopped = errors.New("syncsched: job stopped")

// stopper is a job's stop handle, made before status.Begin takes it.
type stopper struct {
	ctx  context.Context
	stop context.CancelFunc
}

func newStopper() stopper {
	ctx, stop := context.WithCancel(context.Background())
	return stopper{ctx: ctx, stop: stop}
}

// ending is how a job's work ended; outcome None when its context ended
// first, which reads Cancelled or Interrupted.
type ending struct {
	cause   string
	outcome status.Outcome
}

// keyed is owner's job under key, when it sent one and status holds it.
func (a *Aligner) keyed(ctx context.Context, owner int64, key *status.JobKey) (status.Job, bool, error) {
	if key == nil {
		return status.Job{}, false, nil
	}
	return a.status.Keyed(ctx, owner, *key)
}

// begin admits j under the bounds, with a proposal reservation when pk is
// set, and records it with status. replayed is an earlier job answering
// the same request: an unexpired proposal under pk, or a keyed resend.
func (a *Aligner) begin(ctx context.Context, j status.Job, pk *proposalKey) (_ status.Job, _ stopper, replayed bool, _ error) { //nolint:gocritic // hugeParam: j is Begin's template
	a.mu.Lock()
	now := time.Now()
	if pk != nil {
		if id, ok := a.ledger.live(pk, now); ok {
			a.mu.Unlock()
			job, found, err := a.status.Get(ctx, id)
			if err == nil && !found {
				err = fmt.Errorf("syncsched: status lost proposal job %s", id)
			}
			return job, stopper{}, true, err
		}
	}
	err := a.ledger.admit(j.Owner, pk != nil, now)
	a.mu.Unlock()
	if err != nil {
		return status.Job{}, stopper{}, false, err
	}
	st := newStopper()
	job, replayed, err := a.status.Begin(ctx, j, st.stop)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil || replayed {
		a.ledger.withdraw(j.Owner, pk != nil)
		st.stop()
		return job, stopper{}, replayed, err
	}
	if pk != nil {
		a.ledger.hold(job.ID, pk)
	}
	return job, st, false, nil
}

// start hands work to Run as job's goroutine, waiting for Run to take it
// whatever the request's context does, so a closed tab never cancels a
// job; a job Run will never take ends Interrupted at once.
func (a *Aligner) start(job status.Job, st stopper, work func(context.Context, func()) ending) { //nolint:gocritic // hugeParam: the job is Begin's copy
	run := func(base context.Context) { a.runJob(base, job, st, work) }
	select {
	case a.spawn <- run:
		return
	case <-a.stopped:
	}
	st.stop()
	a.finish(&job, ending{outcome: status.Interrupted})
}

func (a *Aligner) runJob(base context.Context, job status.Job, st stopper, work func(context.Context, func()) ending) { //nolint:gocritic // hugeParam: the job is Begin's copy
	ctx, cancel := context.WithCancelCause(base)
	unhook := context.AfterFunc(st.ctx, func() { cancel(errStopped) })
	e := work(ctx, sync.OnceFunc(func() {
		a.status.Update(job.ID, func(j *status.Job) { j.State = status.Running })
	}))
	if e.outcome == status.None {
		e = ending{outcome: status.Interrupted}
		if errors.Is(context.Cause(ctx), errStopped) {
			e.outcome = status.Cancelled
		}
	}
	unhook()
	cancel(nil)
	st.stop()
	a.finish(&job, e)
}

// finish frees job's place in the bounds, then ends it.
func (a *Aligner) finish(job *status.Job, e ending) {
	a.mu.Lock()
	a.ledger.ended(job.Owner, job.ID)
	a.mu.Unlock()
	a.status.Update(job.ID, func(j *status.Job) {
		j.State, j.Outcome, j.Cause = status.Done, e.outcome, e.cause
	})
	a.log.Info("syncsched: sync job ended", "job", job.ID, "outcome", e.outcome.String(), "cause", e.cause)
}

func (a *Aligner) progress(id status.JobID, done int) {
	a.status.Update(id, func(j *status.Job) { j.Done = done })
}

// passOf is the pass that aligns timed for a job by method, settled as
// Current reads the file: its forced flag and its rigidity, whatever its
// Decision. running, when set, marks the job Running once the pass holds
// the slot.
func passOf(file mediawrite.FileRef, timed *mediawrite.Timed, method timing.Method, o origin, running func()) pass {
	return pass{
		inst: file.Inst(), cues: timed.Cues, tag: timed.Tag, origin: o,
		reference: method == timing.MethodReference, audio: method == timing.Audio,
		rigid: timed.Rigid, forced: timed.Flags.Forced, started: running, below: subsync.AtFit,
	}
}

func decisionOf(timed *mediawrite.Timed) timing.Decision {
	if timed.Outcome == nil {
		return timing.Decision{}
	}
	return timed.Outcome.Decision
}

func validMethod(m timing.Method) error {
	if m != timing.MethodReference && m != timing.Audio {
		return fmt.Errorf("syncsched: a sync job with method %d", m)
	}
	return nil
}

// causeOf is the job cause of a failure.
func causeOf(err error) string {
	switch {
	case errors.Is(err, ErrDifferentCut):
		return causeDifferentCut
	case errors.Is(err, ffmpeg.ErrUnavailable):
		return causeFFmpegUnavailable
	}
	for _, c := range mediawrite.Codes {
		if errors.Is(err, c.Err) {
			return c.Code
		}
	}
	return causeSyncFailed
}

// writeCause is the job cause of a Retime failure.
func writeCause(err error) string {
	if c := causeOf(err); c != causeSyncFailed {
		return c
	}
	return causeWriteFailed
}

// emptyCause is the cause of a pass r that settled no fit for method.
func emptyCause(r *result, method timing.Method) string {
	switch {
	case r.failure != nil:
		return causeOf(r.failure)
	case method == timing.Audio:
		return causeNoAudio
	}
	return causeNoReference
}
