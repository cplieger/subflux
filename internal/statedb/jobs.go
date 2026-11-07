package statedb

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cplieger/bolttable"
	"github.com/cplieger/subflux/internal/status"
)

// InsertJob implements status.Persister in one write. A refusal or a
// replay writes nothing.
func (s *Store) InsertJob(ctx context.Context, j status.Job, lim status.JobLimits) (status.Job, bool, error) { //nolint:gocritic // hugeParam: status.Persister passes a Job by value
	if lim.PerOwner < 1 || lim.Total < 1 {
		return status.Job{}, false, fmt.Errorf("statedb: insert job: limits %+v bound nothing", lim)
	}
	var stored status.Job
	var replayed bool
	err := s.write(ctx, "insert job", s.db.Update, func(tx *bolttable.Tx) (err error) {
		stored, replayed, err = s.insertJob(tx, &j, lim)
		return err
	})
	if err != nil {
		return status.Job{}, false, fmt.Errorf("statedb: insert job: %w", err)
	}
	return stored, replayed, nil
}

func (s *Store) insertJob(tx *bolttable.Tx, j *status.Job, lim status.JobLimits) (status.Job, bool, error) {
	r := tx.Read()
	prev, replay, err := s.replayOf(r, j)
	if err != nil || replay {
		return prev, replay, err
	}
	if _, exists, err := s.t.jobs.Get(r, j.ID); err != nil || exists {
		if exists {
			err = fmt.Errorf("statedb: job %s exists", j.ID)
		}
		return status.Job{}, false, err
	}
	if err := s.makeRoom(tx, j.Owner, lim); err != nil {
		return status.Job{}, false, err
	}
	row := jobRowOf(j)
	if err := s.t.jobs.Put(tx, j.ID, &row); err != nil {
		return status.Job{}, false, err
	}
	return jobOf(j.ID, &row), false, nil
}

// replayOf is the stored record of j's key when its fingerprint is j's,
// and status.ErrKeyReused when it is another's.
func (s *Store) replayOf(r *bolttable.ReadTx, j *status.Job) (status.Job, bool, error) {
	if j.Key == nil {
		return status.Job{}, false, nil
	}
	prev, found, err := s.jobByKey(r, j.Owner, j.Key.Key)
	switch {
	case err != nil || !found:
		return status.Job{}, false, err
	case prev.Key.Fingerprint != j.Key.Fingerprint:
		return status.Job{}, false, status.ErrKeyReused
	}
	return prev, true, nil
}

// makeRoom deletes the oldest ended record inside each full bound a new
// record of owner would pass: the owner's for PerOwner, then any user's for
// Total, which SystemOwner is outside. A full bound with no ended record is
// status.ErrJobCapacity.
func (s *Store) makeRoom(tx *bolttable.Tx, owner int64, lim status.JobLimits) error {
	r := tx.Read()
	prefix := ownerBytes(owner)
	n := 0
	err := s.t.jobsByOwner.Scan(r, bolttable.Range{Prefix: prefix}, func([]byte, status.JobID, []byte) (bool, error) {
		n++
		return n < lim.PerOwner, nil
	})
	if err == nil && n >= lim.PerOwner {
		err = s.evictOldest(tx, s.t.ownerEnded, prefix)
	}
	if err != nil || !userOwned(owner) {
		return err
	}
	total, err := s.t.owned.Value(r)
	if err != nil || total < int64(lim.Total) {
		return err
	}
	return s.evictOldest(tx, s.t.userEnded, nil)
}

// evictOldest deletes the record of ix's first entry under prefix, which
// Table.Delete takes out of every index, the key index included.
func (s *Store) evictOldest(tx *bolttable.Tx, ix *bolttable.Index[status.JobID, jobRow], prefix []byte) error {
	var victim status.JobID
	found := false
	err := ix.Scan(tx.Read(), bolttable.Range{Prefix: prefix}, func(_ []byte, id status.JobID, _ []byte) (bool, error) {
		victim, found = id, true
		return false, nil
	})
	if err != nil {
		return err
	}
	if !found {
		return status.ErrJobCapacity
	}
	_, err = s.t.jobs.Delete(tx, victim)
	return err
}

// PutJob implements status.Persister.
func (s *Store) PutJob(ctx context.Context, j status.Job) error { //nolint:gocritic // hugeParam: status.Persister passes a Job by value
	row := jobRowOf(&j)
	err := s.write(ctx, "put job", s.db.Update, func(tx *bolttable.Tx) error {
		prev, found, err := s.t.jobs.Get(tx.Read(), j.ID)
		switch {
		case err != nil:
			return err
		case !found:
			return fmt.Errorf("statedb: job %s is not stored", j.ID)
		case j.Rev <= prev.Rev:
			return fmt.Errorf("statedb: job %s rev %d is not above the stored %d", j.ID, j.Rev, prev.Rev)
		}
		return s.t.jobs.Put(tx, j.ID, &row)
	})
	if err != nil {
		return fmt.Errorf("statedb: put job: %w", err)
	}
	return nil
}

// GetJob implements status.Persister.
func (s *Store) GetJob(ctx context.Context, id status.JobID) (status.Job, bool, error) {
	var j status.Job
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		row, ok, err := s.t.jobs.Get(r, id)
		if ok {
			j, found = jobOf(id, &row), true
		}
		return err
	})
	if err != nil {
		return status.Job{}, false, fmt.Errorf("statedb: get job: %w", err)
	}
	return j, found, nil
}

// JobByKey implements status.Persister.
func (s *Store) JobByKey(ctx context.Context, owner int64, key string) (status.Job, bool, error) {
	var j status.Job
	var found bool
	err := s.db.View(ctx, func(r *bolttable.ReadTx) (err error) {
		j, found, err = s.jobByKey(r, owner, key)
		return err
	})
	if err != nil {
		return status.Job{}, false, fmt.Errorf("statedb: job by key: %w", err)
	}
	return j, found, nil
}

func (s *Store) jobByKey(r *bolttable.ReadTx, owner int64, key string) (status.Job, bool, error) {
	id, _, found, err := s.t.jobByKey.Lookup(r, appendText(ownerBytes(owner), key))
	if err != nil || !found {
		return status.Job{}, false, err
	}
	row, found, err := s.t.jobs.Get(r, id)
	switch {
	case err != nil:
		return status.Job{}, false, err
	case !found:
		return status.Job{}, false, fmt.Errorf("%w: the key index names job %s, which is gone", bolttable.ErrCorrupt, id)
	}
	return jobOf(id, &row), true, nil
}

// errTooManyJobs is UnfinishedJobs past the most records the default
// bounds admit, which only a write outside InsertJob can store.
var errTooManyJobs = errors.New("statedb: the jobs table holds more records than its bounds admit")

// UnfinishedJobs implements status.Persister, scanning at most the
// DefaultLimits' Total plus one owner's PerOwner (SystemOwner's) records.
func (s *Store) UnfinishedJobs(ctx context.Context) ([]status.Job, error) {
	most := status.DefaultLimits.Total + status.DefaultLimits.PerOwner
	out := []status.Job{}
	err := s.db.View(ctx, func(r *bolttable.ReadTx) error {
		n := 0
		return s.t.jobs.Scan(r, bolttable.Range{}, func(id status.JobID, v *jobRow) (bool, error) {
			n++
			if n > most {
				return false, errTooManyJobs
			}
			if v.State != uint8(status.Done) {
				out = append(out, jobOf(id, v))
			}
			return true, nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("statedb: unfinished jobs: %w", err)
	}
	return out, nil
}

// DeleteJobsEndedBefore implements status.Persister: the ended index's
// range stops at the first record ended at or after t.
func (s *Store) DeleteJobsEndedBefore(ctx context.Context, t time.Time) (int, error) {
	var n int
	err := s.write(ctx, "delete ended jobs", s.db.Update, func(tx *bolttable.Tx) error {
		var ids []status.JobID
		err := s.t.jobsEnded.Scan(tx.Read(), bolttable.Range{To: appendTime(nil, t)}, func(_ []byte, id status.JobID, _ []byte) (bool, error) {
			ids = append(ids, id)
			return true, nil
		})
		if err != nil {
			return err
		}
		for _, id := range ids {
			if _, err := s.t.jobs.Delete(tx, id); err != nil {
				return err
			}
		}
		n = len(ids)
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("statedb: delete ended jobs: %w", err)
	}
	return n, nil
}

func jobRowOf(j *status.Job) jobRow {
	r := jobRow{
		Started: j.Started, Ended: j.Ended, Scope: j.Scope, Phase: j.Phase, Cause: j.Cause,
		Owner: j.Owner, Done: j.Done, Total: j.Total, Rev: j.Rev,
		Kind: uint8(j.Kind), State: uint8(j.State), Outcome: uint8(j.Outcome), Cancellable: j.Cancellable,
	}
	if j.Key != nil {
		r.Key = &jobKeyRow{Key: j.Key.Key, Fingerprint: j.Key.Fingerprint}
	}
	return r
}

func jobOf(id status.JobID, r *jobRow) status.Job {
	j := status.Job{
		ID: id, Started: r.Started, Ended: r.Ended, Scope: r.Scope, Phase: r.Phase, Cause: r.Cause,
		Owner: r.Owner, Done: r.Done, Total: r.Total, Rev: r.Rev,
		Kind: status.Kind(r.Kind), State: status.State(r.State), Outcome: status.Outcome(r.Outcome), Cancellable: r.Cancellable,
	}
	if r.Key != nil {
		j.Key = &status.JobKey{Key: r.Key.Key, Fingerprint: r.Key.Fingerprint}
	}
	return j
}
