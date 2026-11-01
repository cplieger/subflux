package status

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
)

// JobLimits bounds stored job records. PerOwner bounds each owner, system
// included; Total bounds all user owners together.
type JobLimits struct{ PerOwner, Total int }

// DefaultLimits are the bounds Begin passes.
var DefaultLimits = JobLimits{PerOwner: 1_000, Total: 10_000}

// Persister stores job records; statedb implements it.
type Persister interface {
	// InsertJob stores a new record in one transaction. A record with the
	// same (Owner, Key.Key) is returned with true when its fingerprint is
	// equal and is ErrKeyReused otherwise, either winning over the bounds.
	// At a full bound it first deletes the oldest ended record inside that
	// bound, keyed or not (the owner's for PerOwner, any user's for Total),
	// and answers ErrJobCapacity, writing nothing, only when every record
	// there is unfinished. An existing ID is an error.
	InsertJob(context.Context, Job, JobLimits) (Job, bool, error)
	// PutJob replaces a stored record; an absent ID, or a Rev not above the
	// stored one, is an error.
	PutJob(context.Context, Job) error
	GetJob(context.Context, JobID) (Job, bool, error)
	JobByKey(ctx context.Context, owner int64, key string) (Job, bool, error)
	UnfinishedJobs(context.Context) ([]Job, error)
	DeleteJobsEndedBefore(context.Context, time.Time) (int, error)
}

// The terminal-record budget and retry backoff; tests shorten none of them,
// since they run under synctest.
const (
	terminalBudget = 5 * time.Second
	retryMin       = time.Second
	retryMax       = time.Minute
	sweepEvery     = time.Hour
	recordMaxAge   = 24 * time.Hour
)

// New opens the store over p, first rewriting every unfinished record as
// Done with Outcome Interrupted, not cancellable, so a job a crash cut short
// never reads back as running.
func New(ctx context.Context, p Persister) (*Store, error) {
	if p == nil {
		return nil, errors.New("status: nil persister")
	}
	recs, err := p.UnfinishedJobs(ctx)
	if err != nil {
		return nil, fmt.Errorf("status: read unfinished jobs: %w", err)
	}
	s := newStore()
	s.p = p
	now := time.Now()
	for i := range recs {
		j := &recs[i]
		j.State, j.Outcome, j.Cancellable, j.Ended = Done, Interrupted, false, now
		j.Rev++
		if err := p.PutJob(ctx, *j); err != nil {
			return nil, fmt.Errorf("status: mark job %s interrupted: %w", j.ID, err)
		}
		s.live[j.ID] = &liveJob{job: *j, durable: true}
	}
	s.evictLocked(now)
	return s, nil
}

// Run retries every terminal record not yet stored, with backoff from 1 s
// to 60 s, and deletes records ended over 24 h ago at start and hourly. It
// returns nil when ctx ends, after one last attempt at the unstored records.
func (s *Store) Run(ctx context.Context) error {
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()
	expire := time.NewTicker(liveMaxAge / 15)
	defer expire.Stop()
	s.sweepRecords()
	backoff := retryMin
	var retry *time.Timer
	var retryC <-chan time.Time
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	for {
		if retryC == nil && s.hasUnsaved() {
			retry = time.NewTimer(backoff)
			retryC = retry.C
		}
		select {
		case <-ctx.Done():
			s.saveUnsaved()
			return nil
		case <-s.retryWake:
		case <-retryC:
			retryC = nil
			if s.saveUnsaved() {
				backoff = retryMin
			} else {
				backoff = min(2*backoff, retryMax)
			}
		case <-sweep.C:
			s.sweepRecords()
		case <-expire.C:
			s.evictExpired()
		}
	}
}

func (s *Store) hasUnsaved() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.unsaved) > 0
}

// saveUnsaved attempts every unstored terminal record and reports whether
// none is left.
func (s *Store) saveUnsaved() bool {
	s.mu.Lock()
	ids := make([]JobID, 0, len(s.unsaved))
	for id := range s.unsaved {
		ids = append(ids, id)
	}
	s.mu.Unlock()
	for _, id := range ids {
		s.persist(id)
	}
	return !s.hasUnsaved()
}

// persist stores id's terminal record on a detached context. Each attempt
// after the first raises Rev, so an attempt whose write landed but reported
// failure cannot make every later one a stale-revision refusal.
func (s *Store) persist(id JobID) {
	s.mu.Lock()
	l, ok := s.live[id]
	if !ok || l.durable || l.saving {
		s.mu.Unlock()
		return
	}
	if l.attempts > 0 {
		l.job.Rev++
		s.bump()
	}
	l.attempts++
	l.saving = true
	j := cloneJob(l.job)
	s.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), terminalBudget)
	err := s.p.PutJob(ctx, j)
	cancel()

	s.mu.Lock()
	l.saving = false
	if err != nil {
		s.mu.Unlock()
		slog.Warn("status: could not store a finished job; retrying", "job", string(id), "error", err)
		s.Reconcile(AlertJobRecords, true, "finished jobs are not yet stored")
		select {
		case s.retryWake <- struct{}{}:
		default:
		}
		return
	}
	l.durable = true
	delete(s.unsaved, id)
	empty := len(s.unsaved) == 0
	if s.evictLocked(time.Now()) {
		s.bump()
	}
	s.mu.Unlock()
	if empty {
		s.Reconcile(AlertJobRecords, false, "")
	}
}

func (s *Store) sweepRecords() {
	slog.Info("status: job record sweep started")
	ctx, cancel := context.WithTimeout(context.Background(), terminalBudget)
	defer cancel()
	n, err := s.p.DeleteJobsEndedBefore(ctx, time.Now().Add(-recordMaxAge))
	if err != nil {
		slog.Warn("status: job record sweep failed", "error", err)
		return
	}
	slog.Info("status: job record sweep finished", "deleted", n)
}
