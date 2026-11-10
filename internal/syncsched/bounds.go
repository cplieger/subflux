package syncsched

import (
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/mediawrite"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/timing"
)

// The bounds on what the jobs and previews hold.
const (
	MaxQueuedPerOwner    = 8
	MaxQueued            = 64
	MaxProposalsPerOwner = 16
	MaxProposals         = 256
	ProposalTTL          = 15 * time.Minute
	MaxPreviews          = 2
	MaxBatch             = 500
)

// referenceCacheSize is how many extracted references' spans the cache holds.
const referenceCacheSize = 32

// maxPreviewFrom is the latest start a preview takes.
const maxPreviewFrom = 24 * time.Hour

// ledger counts what the bounds limit: queued and running jobs per owner,
// proposals from admit until they expire, and streaming previews.
type ledger struct {
	jobs      map[int64]int
	held      map[status.JobID]*reservation
	heldBy    map[int64]int
	jobsTotal int
	// heldTotal is heldBy's sum: held lacks the reservations admitted and
	// not yet begun, so the global bound cannot read len(held).
	heldTotal int
	previews  int
}

// reservation is one proposal: running while proposal is nil, then holding
// it until its Ended plus ProposalTTL.
type reservation struct {
	proposal *Proposal
	key      proposalKey
}

// proposalKey is what makes two proposals the same: the expectation too,
// since one made for a file's earlier state cannot apply to it.
type proposalKey struct {
	file   mediawrite.FileRef
	expect mediawrite.Expectation
	owner  int64
	method timing.Method
}

func newLedger() ledger {
	return ledger{jobs: map[int64]int{}, held: map[status.JobID]*reservation{}, heldBy: map[int64]int{}}
}

// admit counts a job for owner, and a proposal with it when proposal is set,
// or answers ErrSyncCapacity and counts nothing.
func (l *ledger) admit(owner int64, proposal bool, now time.Time) error {
	switch {
	case l.jobs[owner] >= MaxQueuedPerOwner:
		return fmt.Errorf("%w: %d sync jobs queued or running for this user", ErrSyncCapacity, MaxQueuedPerOwner)
	case l.jobsTotal >= MaxQueued:
		return fmt.Errorf("%w: %d sync jobs queued or running", ErrSyncCapacity, MaxQueued)
	}
	if proposal {
		l.expire(now)
		switch {
		case l.heldBy[owner] >= MaxProposalsPerOwner:
			return fmt.Errorf("%w: %d proposals held for this user", ErrSyncCapacity, MaxProposalsPerOwner)
		case l.heldTotal >= MaxProposals:
			return fmt.Errorf("%w: %d proposals held", ErrSyncCapacity, MaxProposals)
		}
		l.heldBy[owner]++
		l.heldTotal++
	}
	l.jobs[owner]++
	l.jobsTotal++
	return nil
}

// withdraw undoes an admit whose job never began.
func (l *ledger) withdraw(owner int64, proposal bool) {
	l.jobDone(owner)
	if proposal {
		l.unheld(owner)
	}
}

// hold records the reservation of a begun proposal job.
func (l *ledger) hold(id status.JobID, k *proposalKey) { l.held[id] = &reservation{key: *k} }

// keep holds p as its job's proposal.
func (l *ledger) keep(p *Proposal) {
	if r := l.held[p.Job]; r != nil {
		r.proposal = p
	}
}

// ended counts owner's job id as ended; a proposal job that holds no
// proposal frees its reservation.
func (l *ledger) ended(owner int64, id status.JobID) {
	l.jobDone(owner)
	if r := l.held[id]; r != nil && r.proposal == nil {
		delete(l.held, id)
		l.unheld(owner)
	}
}

// live is the job of the unexpired proposal under k.
func (l *ledger) live(k *proposalKey, now time.Time) (status.JobID, bool) {
	l.expire(now)
	for id, r := range l.held {
		if r.key == *k {
			return id, true
		}
	}
	return "", false
}

// proposal is owner's unexpired proposal of job id.
func (l *ledger) proposal(owner int64, id status.JobID, now time.Time) (Proposal, bool) {
	l.expire(now)
	r := l.held[id]
	if r == nil || r.proposal == nil || r.key.owner != owner {
		return Proposal{}, false
	}
	return *r.proposal, true
}

func (l *ledger) expire(now time.Time) {
	for id, r := range l.held {
		if r.proposal != nil && !now.Before(r.proposal.Ended.Add(ProposalTTL)) {
			delete(l.held, id)
			l.unheld(r.key.owner)
		}
	}
}

// openPreview counts a preview, false at MaxPreviews.
func (l *ledger) openPreview() bool {
	if l.previews >= MaxPreviews {
		return false
	}
	l.previews++
	return true
}

func (l *ledger) closePreview() { l.previews-- }

func (l *ledger) jobDone(owner int64) {
	l.jobsTotal--
	l.jobs[owner]--
	if l.jobs[owner] == 0 {
		delete(l.jobs, owner)
	}
}

func (l *ledger) unheld(owner int64) {
	l.heldTotal--
	l.heldBy[owner]--
	if l.heldBy[owner] == 0 {
		delete(l.heldBy, owner)
	}
}
