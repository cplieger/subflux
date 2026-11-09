package scan

import (
	"context"
	"slices"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// reload is one Reload handed to Run; done is closed once it is applied.
type reload struct {
	set              sources
	done             chan struct{}
	changed, rekeyed []media.SourceID
}

// Reload hands Run a rebuilt Set and returns once it is applied; the latest
// call wins. changed names every added source and every source whose entry
// differs outside its kind's Secret fields; rekeyed every source whose entry
// differs only in them. Removed and changed sources' queued requests end
// Interrupted; apply owns what each loop does.
func (s *Scanner) Reload(set sources, changed, rekeyed []media.SourceID) {
	r := reload{set: set, changed: changed, rekeyed: rekeyed, done: make(chan struct{})}
	s.mu.Lock()
	if !s.running {
		s.applyLocked(r)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()
	select {
	case s.reloads <- r:
		<-r.done
	case <-s.exited:
	}
}

// apply stops the loops of removed and changed sources and waits for each,
// then starts one for each added or changed source with its whole pass due
// at once. Every other loop keeps its schedule, queue, cursor and retry set
// and takes the new Set at its next pass boundary; a rekeyed one also drops
// its whole-pass backoff, so a pass failed on the old key retries at the
// next wake. Run calls it, so no loop starts after Run stops.
func (s *Scanner) apply(ctx context.Context, r reload) {
	s.mu.Lock()
	s.set = r.set
	stop := s.stoppedLocked(r)
	s.mu.Unlock()
	for _, l := range stop {
		l.cancel()
		<-l.done
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range stop {
		delete(s.loops, l.src)
		s.metrics.forget(l.src)
	}
	for _, l := range s.addLocked(r) {
		s.start(ctx, l)
	}
	s.rekeyLocked(r)
}

// applyLocked applies r before Run starts: no loop runs yet.
func (s *Scanner) applyLocked(r reload) {
	s.set = r.set
	for _, l := range s.stoppedLocked(r) {
		delete(s.loops, l.src)
	}
	s.addLocked(r)
	s.rekeyLocked(r)
}

// stoppedLocked lists the loops of sources r's Set lacks or r changed.
func (s *Scanner) stoppedLocked(r reload) []*loop {
	ids := r.set.Sources()
	var out []*loop
	for id, l := range s.loops {
		if !slices.Contains(ids, id) || slices.Contains(r.changed, id) {
			out = append(out, l)
		}
	}
	return out
}

// addLocked makes a loop, its whole pass due now, for each source of r's
// Set with none, and answers them.
func (s *Scanner) addLocked(r reload) []*loop {
	var out []*loop
	for _, id := range r.set.Sources() {
		if _, ok := s.loops[id]; !ok {
			l := newLoop(s, id, 0)
			s.loops[id] = l
			out = append(out, l)
		}
	}
	return out
}

func (s *Scanner) rekeyLocked(r reload) {
	for _, id := range r.rekeyed {
		if l, ok := s.loops[id]; ok && !slices.Contains(r.changed, id) {
			signal(l.rekey)
		}
	}
}

// SetSchedule hands Run a new scan section; the latest call wins. Every loop
// keeps its cursor, queue, retry set and backoff, and re-arms its next
// change check and its scheduled whole pass from the new intervals at once.
// A schedule whose interval is not positive is refused with ERROR, the old
// one kept.
func (s *Scanner) SetSchedule(sched mediasource.Schedule) {
	if sched.Change <= 0 || sched.Whole <= 0 {
		s.log.Error("scan: refused a schedule whose intervals are not positive", "change", sched.Change, "whole", sched.Whole)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sched = sched
	s.metrics.interval(sched)
	for _, l := range s.loops {
		signal(l.rearm)
	}
}
