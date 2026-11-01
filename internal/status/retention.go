package status

import (
	"cmp"
	"slices"
	"time"
)

// Live retention of ended jobs, so a client polling a job it started sees
// its end before the job leaves the live view.
const (
	liveMaxDone = 50
	liveMaxAge  = 15 * time.Minute
)

func (s *Store) evictExpired() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.evictLocked(time.Now()) {
		s.bump()
	}
}

// evictLocked drops ended jobs older than liveMaxAge, then the oldest ended
// beyond liveMaxDone, and reports whether it dropped one. A job whose
// terminal record is not yet stored stays, so Get and Keyed never fall back
// to its Queued record. s.mu is held.
func (s *Store) evictLocked(now time.Time) bool {
	var ended []*liveJob
	for _, l := range s.live {
		if l.job.State == Done && l.durable {
			ended = append(ended, l)
		}
	}
	slices.SortFunc(ended, func(a, b *liveJob) int {
		if c := a.job.Ended.Compare(b.job.Ended); c != 0 {
			return c
		}
		return cmp.Compare(a.job.ID, b.job.ID)
	})
	dropped := false
	for i, l := range ended {
		if len(ended)-i <= liveMaxDone && now.Sub(l.job.Ended) < liveMaxAge {
			break
		}
		delete(s.live, l.job.ID)
		dropped = true
	}
	return dropped
}
