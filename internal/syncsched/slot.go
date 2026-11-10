package syncsched

import (
	"context"
	"slices"
	"sync"
	"time"
)

// class is the slot's queue: interactive first; automatic and batch then
// alternate one alignment each while both wait.
type class uint8

const (
	classInteractive class = iota + 1
	classAutomatic
	classBatch
)

var classNames = [...]string{classInteractive: "interactive", classAutomatic: "automatic", classBatch: "batch"}

// slot runs one alignment at a time and never preempts it. A batch job
// takes it per file, so queued work waits behind at most one batch file.
type slot struct {
	m      *metrics
	queues [classBatch + 1][]chan struct{} // FIFO per class; guarded by mu
	mu     sync.Mutex
	busy   bool  // guarded by mu
	shared class // the automatic or batch class served last; guarded by mu
}

func newSlot(m *metrics) *slot {
	for c := classInteractive; c <= classBatch; c++ {
		m.depth.Set(0, classNames[c])
	}
	return &slot{m: m}
}

// acquire waits for the slot in class c and returns its release, which is
// safe to call more than once; a context that ends first returns its error.
func (s *slot) acquire(ctx context.Context, c class) (func(), error) {
	start := time.Now()
	s.mu.Lock()
	if !s.busy {
		s.busy = true
		s.servedLocked(c)
		s.mu.Unlock()
		return s.granted(start), nil
	}
	ready := make(chan struct{})
	s.queues[c] = append(s.queues[c], ready)
	s.depthLocked(c)
	s.mu.Unlock()
	select {
	case <-ready:
		return s.granted(start), nil
	case <-ctx.Done():
	}
	s.mu.Lock()
	i := slices.Index(s.queues[c], ready)
	if i >= 0 {
		s.queues[c] = slices.Delete(s.queues[c], i, i+1)
		s.depthLocked(c)
	}
	s.mu.Unlock()
	if i < 0 {
		// Granted while the context ended: pass the slot on.
		s.release()
	}
	return nil, ctx.Err()
}

func (s *slot) granted(start time.Time) func() {
	s.m.wait.Observe(time.Since(start).Seconds())
	return sync.OnceFunc(s.release)
}

// release hands the slot to the next waiter, or frees it.
func (s *slot) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.nextLocked()
	if !ok {
		s.busy = false
		return
	}
	ready := s.queues[c][0]
	s.queues[c] = s.queues[c][1:]
	s.depthLocked(c)
	s.servedLocked(c)
	close(ready)
}

func (s *slot) nextLocked() (class, bool) {
	if len(s.queues[classInteractive]) > 0 {
		return classInteractive, true
	}
	auto, batch := len(s.queues[classAutomatic]) > 0, len(s.queues[classBatch]) > 0
	switch {
	case auto && batch && s.shared == classAutomatic:
		return classBatch, true
	case auto:
		return classAutomatic, true
	case batch:
		return classBatch, true
	}
	return 0, false
}

func (s *slot) servedLocked(c class) {
	if c != classInteractive {
		s.shared = c
	}
}

func (s *slot) depthLocked(c class) { s.m.depth.Set(float64(len(s.queues[c])), classNames[c]) }
