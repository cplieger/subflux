package scan

import (
	"context"
	"errors"
	"slices"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/status"
)

// Request is a manual scan: Root nil asks a whole pass of Source, else a
// refetch of that one root. A refetch whose read fails while the source
// answers its probe ends its job Failed with the *SourceError; the root's
// retry continues with no job attached.
type Request struct {
	Root   *mediasource.NativeRef
	Key    *status.JobKey
	Source media.SourceID
	Owner  int64
}

// ErrScanCapacity answers a request needing a new queue slot while its
// source holds 16 or its owner 4 there.
var ErrScanCapacity = errors.New("scan: too many queued requests for this source or owner")

// Start records a Queued job and hands it to the source's loop, which runs
// it with the next pass of its scope. In order: a keyed request already
// recorded returns its job, whatever has happened to its source since; an
// unknown source is a *SourceError with UnknownSource, a root of a kind the
// source does not serve Unsupported; then, under the source's admission
// lock: ErrScanCapacity, writing nothing; Begin, whose replay of a racing
// duplicate returns that job unqueued and whose error queues nothing; and
// the insertion. status's errors pass through.
func (s *Scanner) Start(ctx context.Context, r Request) (status.Job, error) {
	if r.Key != nil {
		if j, ok, err := s.status.Keyed(ctx, r.Owner, *r.Key); err != nil || ok {
			return j, err
		}
	}
	l, caps, ok := s.source(r.Source)
	if !ok {
		return status.Job{}, refused(r.Source, mediasource.UnknownSource)
	}
	job := status.Job{Kind: status.Scan, Owner: r.Owner, Scope: string(r.Source), Key: r.Key, Source: r.Source}
	if r.Root != nil {
		if !slices.Contains(caps.Kinds, r.Root.Kind) {
			return status.Job{}, refused(r.Source, mediasource.Unsupported)
		}
		job.Kind, job.Scope = status.ItemScan, rootScope(r.Source, *r.Root)
	}
	ref := new(status.JobID)
	l.q.mu.Lock()
	defer l.q.mu.Unlock()
	if l.q.closed {
		return status.Job{}, refused(r.Source, mediasource.UnknownSource)
	}
	if !l.q.roomLocked(r.Owner, r.Root) {
		return status.Job{}, ErrScanCapacity
	}
	j, replayed, err := s.status.Begin(ctx, job, func() { l.stopRequest(ref) })
	if err != nil || replayed {
		return j, err
	}
	*ref = j.ID
	l.q.insertLocked(queued{job: j.ID, owner: r.Owner}, r.Root)
	if r.Root == nil {
		signal(l.poke)
	}
	return j, nil
}

// refused is Start's *SourceError for src.
func refused(src media.SourceID, c mediasource.Cause) *mediasource.SourceError {
	return &mediasource.SourceError{Source: src, Op: "Start", Cause: c}
}

// rootScope is a refetch job's Scope: the source id, the root's kind and
// its native id.
func rootScope(src media.SourceID, root mediasource.NativeRef) string {
	k, _ := root.Kind.MarshalText()
	return string(src) + "/" + string(k) + "/" + string(root.ID)
}

// stopRequest ends a requester's job Cancelled once the queue or the pass
// carrying it let it go.
func (l *loop) stopRequest(ref *status.JobID) {
	if id, ok := l.q.stop(ref); ok {
		l.s.status.Update(id, func(j *status.Job) { j.State, j.Outcome = status.Done, status.Cancelled })
	}
}
