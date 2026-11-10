package syncsched

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// Target is one file of a Resync, as its caller listed it.
type Target struct {
	File   mediawrite.FileRef
	Expect mediawrite.Expectation
}

// ResyncRequest asks for its Files' alignment by Method, MethodReference or
// Audio, for the owner status.Job.Owner names.
type ResyncRequest struct {
	Key    *status.JobKey
	Files  []Target
	Owner  int64
	Method timing.Method
}

// Resync starts one Sync job over 1 to MaxBatch files (more is
// ErrBatchTooLarge), or returns a keyed resend's job. Each file is read with
// Current at its turn, aligned in the batch class as Propose's job aligns
// it, and applied with Retime only when Verified; a file that changed, is
// refused or does not verify is skipped, and the job's cause is the first
// skip's. It ends Succeeded when every file applied, Partial when some did,
// else Failed; a stop takes effect between files and inside one.
func (a *Aligner) Resync(ctx context.Context, req ResyncRequest) (status.Job, error) {
	switch {
	case len(req.Files) == 0:
		return status.Job{}, errors.New("syncsched: Resync over no file")
	case len(req.Files) > MaxBatch:
		return status.Job{}, fmt.Errorf("%w: %d files, at most %d", ErrBatchTooLarge, len(req.Files), MaxBatch)
	}
	if err := validMethod(req.Method); err != nil {
		return status.Job{}, err
	}
	if job, ok, err := a.keyed(ctx, req.Owner, req.Key); err != nil || ok {
		return job, err
	}
	job, st, replayed, err := a.begin(ctx, status.Job{Kind: status.Sync, Owner: req.Owner, Key: req.Key, Total: len(req.Files)}, nil)
	if err != nil || replayed {
		return job, err
	}
	files := slices.Clone(req.Files)
	a.start(job, st, func(ctx context.Context, running func()) ending {
		return a.resync(ctx, job.ID, files, req.Method, running)
	})
	return job, nil
}

func (a *Aligner) resync(ctx context.Context, id status.JobID, files []Target, method timing.Method, running func()) ending {
	applied, cause := 0, ""
	for i := range files {
		if ctx.Err() != nil {
			return ending{}
		}
		skipped := a.resyncFile(ctx, &files[i], method, running)
		if ctx.Err() != nil {
			return ending{}
		}
		if skipped == "" {
			applied++
		} else if cause == "" {
			cause = skipped
		}
		a.progress(id, i+1)
	}
	switch applied {
	case len(files):
		return ending{outcome: status.Succeeded}
	case 0:
		return ending{outcome: status.Failed, cause: cause}
	}
	return ending{outcome: status.Partial, cause: cause}
}

// resyncFile aligns and applies one file, answering the cause it was
// skipped for, "" when it applied.
func (a *Aligner) resyncFile(ctx context.Context, t *Target, method timing.Method, running func()) string {
	// A file skipped before its pass still counts as the job's work.
	defer running()
	timed, err := a.writer.Current(ctx, t.File, t.Expect)
	if err != nil {
		return causeOf(err)
	}
	if method == timing.Audio && timed.Rigid {
		return causeDifferentCut
	}
	p := passOf(t.File, &timed, method, originBatch, running)
	r, err := a.runPass(ctx, classBatch, &p)
	switch {
	case err != nil:
		return causeOf(err)
	case r.fit.Method == timing.MethodNone:
		return emptyCause(&r, method)
	case r.verdict != timing.Verified:
		return causeUnverified
	}
	o := r.outcome(decisionOf(&timed))
	if _, err := a.writer.Retime(ctx, t.File, t.Expect, r.timing, &o); err != nil {
		return writeCause(err)
	}
	return ""
}
