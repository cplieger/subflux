// Package statustest holds the contract every status.Persister meets and an
// in-memory reference implementation of it.
package statustest

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/status"
)

// MemPersister is the in-memory reference status.Persister.
type MemPersister struct {
	jobs map[status.JobID]*status.Job
	keys map[memKey]status.JobID
	mu   sync.Mutex
}

type memKey struct {
	key   string
	owner int64
}

var _ status.Persister = (*MemPersister)(nil)

// NewMemPersister returns an empty MemPersister.
func NewMemPersister() *MemPersister {
	return &MemPersister{jobs: map[status.JobID]*status.Job{}, keys: map[memKey]status.JobID{}}
}

// InsertJob implements status.Persister.
func (m *MemPersister) InsertJob(ctx context.Context, j status.Job, lim status.JobLimits) (status.Job, bool, error) { //nolint:gocritic // hugeParam: status.Persister passes a Job by value
	if err := ctx.Err(); err != nil {
		return status.Job{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.Key != nil {
		if id, ok := m.keys[memKey{owner: j.Owner, key: j.Key.Key}]; ok {
			stored := clone(m.jobs[id])
			if stored.Key.Fingerprint != j.Key.Fingerprint {
				return status.Job{}, false, status.ErrKeyReused
			}
			return stored, true, nil
		}
	}
	if _, ok := m.jobs[j.ID]; ok {
		return status.Job{}, false, fmt.Errorf("statustest: job %s exists", j.ID)
	}
	victims, err := m.room(j.Owner, lim)
	if err != nil {
		return status.Job{}, false, err
	}
	for _, v := range victims {
		m.delete(v)
	}
	stored := clone(&j)
	m.jobs[j.ID] = &stored
	if j.Key != nil {
		m.keys[memKey{owner: j.Owner, key: j.Key.Key}] = j.ID
	}
	return clone(&stored), false, nil
}

// room names the records a new record of owner deletes to fit lim: the
// oldest ended inside each full bound, the owner's for PerOwner and any
// user's for Total, which SystemOwner is outside. It is ErrJobCapacity when
// a full bound holds no ended record.
func (m *MemPersister) room(owner int64, lim status.JobLimits) ([]status.JobID, error) {
	var victims []status.JobID
	owned := func(o int64) bool { return o == owner }
	if m.count(owned, nil) >= lim.PerOwner {
		v, ok := m.oldestEnded(owned, nil)
		if !ok {
			return nil, status.ErrJobCapacity
		}
		victims = append(victims, v)
	}
	if owner == status.SystemOwner {
		return victims, nil
	}
	user := func(o int64) bool { return o != status.SystemOwner }
	if m.count(user, victims) >= lim.Total {
		v, ok := m.oldestEnded(user, victims)
		if !ok {
			return nil, status.ErrJobCapacity
		}
		victims = append(victims, v)
	}
	return victims, nil
}

// PutJob implements status.Persister.
func (m *MemPersister) PutJob(ctx context.Context, j status.Job) error { //nolint:gocritic // hugeParam: status.Persister passes a Job by value
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	stored, ok := m.jobs[j.ID]
	if !ok {
		return fmt.Errorf("statustest: no job %s", j.ID)
	}
	if j.Rev <= stored.Rev {
		return fmt.Errorf("statustest: job %s rev %d is not above the stored %d", j.ID, j.Rev, stored.Rev)
	}
	next := clone(&j)
	m.jobs[j.ID] = &next
	return nil
}

// GetJob implements status.Persister.
func (m *MemPersister) GetJob(ctx context.Context, id status.JobID) (status.Job, bool, error) {
	if err := ctx.Err(); err != nil {
		return status.Job{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return status.Job{}, false, nil
	}
	return clone(j), true, nil
}

// JobByKey implements status.Persister.
func (m *MemPersister) JobByKey(ctx context.Context, owner int64, key string) (status.Job, bool, error) {
	if err := ctx.Err(); err != nil {
		return status.Job{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.keys[memKey{owner: owner, key: key}]
	if !ok {
		return status.Job{}, false, nil
	}
	return clone(m.jobs[id]), true, nil
}

// UnfinishedJobs implements status.Persister.
func (m *MemPersister) UnfinishedJobs(ctx context.Context) ([]status.Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []status.Job{}
	for _, j := range m.jobs {
		if j.State != status.Done {
			out = append(out, clone(j))
		}
	}
	return out, nil
}

// DeleteJobsEndedBefore implements status.Persister.
func (m *MemPersister) DeleteJobsEndedBefore(ctx context.Context, t time.Time) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, j := range m.jobs {
		if j.State == status.Done && j.Ended.Before(t) {
			m.delete(id)
			n++
		}
	}
	return n, nil
}

func (m *MemPersister) count(match func(int64) bool, skip []status.JobID) int {
	n := 0
	for id, j := range m.jobs {
		if match(j.Owner) && !slices.Contains(skip, id) {
			n++
		}
	}
	return n
}

func (m *MemPersister) oldestEnded(match func(int64) bool, skip []status.JobID) (status.JobID, bool) {
	var best *status.Job
	for id, j := range m.jobs {
		if j.State != status.Done || !match(j.Owner) || slices.Contains(skip, id) {
			continue
		}
		if best == nil || j.Ended.Before(best.Ended) || (j.Ended.Equal(best.Ended) && cmp.Less(j.ID, best.ID)) {
			best = j
		}
	}
	if best == nil {
		return "", false
	}
	return best.ID, true
}

func (m *MemPersister) delete(id status.JobID) {
	if j, ok := m.jobs[id]; ok && j.Key != nil {
		delete(m.keys, memKey{owner: j.Owner, key: j.Key.Key})
	}
	delete(m.jobs, id)
}

func clone(j *status.Job) status.Job {
	c := *j
	if c.Key != nil {
		k := *c.Key
		c.Key = &k
	}
	return c
}
