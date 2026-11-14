package status

import (
	"cmp"
	"maps"
	"slices"
	"time"
)

// windowJobs bounds Snapshot.Jobs, so a fan-out of thousands of jobs never
// rides every status frame; Unfinished pages the unfinished ones past it.
const windowJobs = 50

// Snapshot is one consistent reading of the store.
type Snapshot struct {
	// Jobs are the newest 50 live jobs, newest started first, ties by ID;
	// Alerts are ordered by source, Providers by id.
	Jobs      []Job
	Alerts    []Alert
	Providers []ProviderState
	// Unfinished counts every Queued or Running job, however few of them
	// Jobs holds.
	Unfinished int
	// Boot names this process, so an observer tells a restart from a
	// sequence number that went back.
	Boot [16]byte
}

// Snapshot answers the store's state and the sequence number it was read at.
func (s *Store) Snapshot() (snap Snapshot, seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	alerts := slices.SortedFunc(maps.Values(s.alerts), func(a, b Alert) int { return cmp.Compare(a.Source, b.Source) })
	providers := slices.SortedFunc(maps.Values(s.providers), func(a, b ProviderState) int { return cmp.Compare(a.ID, b.ID) })
	jobs := s.liveJobs(func(*Job) bool { return true })
	unfinished := 0
	for i := range jobs {
		if jobs[i].State != Done {
			unfinished++
		}
	}
	return Snapshot{Jobs: jobs[:min(len(jobs), windowJobs)], Alerts: alerts, Providers: providers, Unfinished: unfinished, Boot: s.boot}, s.seq
}

// UnfinishedCursor is the position after one listed job: its start time,
// then its ID. The zero value is the position before the newest.
type UnfinishedCursor struct {
	Started time.Time
	ID      JobID
}

// Unfinished returns up to limit Queued or Running jobs after c, newest
// started first, ties by ID, from one view under the store's lock, and the
// cursor after the last, the zero cursor once none is left after it. A job
// ending between calls drops out without shifting the rest. A limit below 1
// lists none and answers the zero cursor.
func (s *Store) Unfinished(c UnfinishedCursor, limit int) ([]Job, UnfinishedCursor) {
	if limit < 1 {
		return nil, UnfinishedCursor{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	start := c.Started.IsZero() && c.ID == ""
	jobs := s.liveJobs(func(j *Job) bool { return j.State != Done && (start || jobOrder(j, c.Started, c.ID) > 0) })
	if len(jobs) <= limit {
		return jobs, UnfinishedCursor{}
	}
	last := jobs[limit-1]
	return jobs[:limit], UnfinishedCursor{Started: last.Started, ID: last.ID}
}

// jobOrder places j against the position (started, id): negative before it,
// newest started first, ties by ID.
func jobOrder(j *Job, started time.Time, id JobID) int {
	if c := started.Compare(j.Started); c != 0 {
		return c
	}
	return cmp.Compare(j.ID, id)
}
