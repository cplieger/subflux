// Package effectqueue runs the side effects a mutation decided under a state
// lock after that lock is released, in the order the mutations happened.
package effectqueue

import "sync"

// Queue orders effect batches. Add batches in mutation order, either under
// the lock that orders the mutations or from one goroutine at a time that
// takes them in that order, and call Drain after releasing that lock. One
// goroutine drains at a time: a Drain that finds another in progress returns
// at once and leaves its batch to that goroutine, so an effect can run after
// its own caller returned, but never before an effect added ahead of it. The
// zero Queue is ready to use.
type Queue struct {
	pending  []func()
	mu       sync.Mutex
	draining bool
}

// Add appends fns to the queue.
func (q *Queue) Add(fns ...func()) {
	if len(fns) == 0 {
		return
	}
	q.mu.Lock()
	q.pending = append(q.pending, fns...)
	q.mu.Unlock()
}

// Drain runs every queued effect, including those added while it runs,
// unless another goroutine is already draining. An effect that panics stops
// the drain and leaves the effects after it queued for the next one.
func (q *Queue) Drain() {
	q.mu.Lock()
	if q.draining {
		q.mu.Unlock()
		return
	}
	q.draining = true
	defer func() {
		q.draining = false
		q.mu.Unlock()
	}()
	for len(q.pending) > 0 {
		batch := q.pending
		q.pending = nil
		q.runUnlocked(batch)
	}
}

// runUnlocked runs batch with q.mu released and holds it again on return,
// panic included, which is what Drain's deferred reset releases. A panic puts
// the effects after the panicking one back at the front of the queue.
func (q *Queue) runUnlocked(batch []func()) {
	q.mu.Unlock()
	next := 0
	defer func() {
		q.mu.Lock()
		if next < len(batch) {
			q.pending = append(batch[next:len(batch):len(batch)], q.pending...)
		}
	}()
	for next < len(batch) {
		f := batch[next]
		next++
		f()
	}
}
