package arrsvc

import (
	"context"
	"sync"
	"time"
)

// ReadGate is the shared half of the arr-read wrapper: the global FIFO
// wave-admission queue — both arr sides share it, so maxConcurrentWaves is an
// aggregate ceiling — plus the server runtime wave passes run under. One per
// server, surviving config reloads; the per-instance halves (CachedSonarr,
// CachedRadarr) are rebuilt by every activation.
type ReadGate struct {
	serverCtx func() context.Context
	bg        *sync.WaitGroup
	queue     *waveQueue
}

// NewReadGate builds the gate. serverCtx yields the server lifetime context
// (shutdown cancels admitted passes; upstream calls are never derived from a
// waiter's request context); bg is the WaitGroup wave runners and their
// timers register with. Either may be nil in tests.
func NewReadGate(serverCtx func() context.Context, bg *sync.WaitGroup) *ReadGate {
	return &ReadGate{serverCtx: serverCtx, bg: bg, queue: newWaveQueue(maxConcurrentWaves)}
}

// lifetime returns the server lifetime context, or Background when the gate
// was built without one.
func (g *ReadGate) lifetime() context.Context {
	if g.serverCtx != nil {
		if ctx := g.serverCtx(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

// goRun starts a wave runner under the server's WaitGroup.
func (g *ReadGate) goRun(fn func()) {
	if g.bg != nil {
		g.bg.Go(fn)
		return
	}
	go fn()
}

// admitVerdict is a waveQueue.acquire outcome.
type admitVerdict int

const (
	admitGranted admitVerdict = iota
	admitTimedOut
	admitDiscarded
	admitShutdown
)

// waveQueue is the explicit FIFO wave-admission queue. Explicit because
// x/sync's semaphore documents no FIFO order, and the admission budget needs
// a per-waiter deadline.
type waveQueue struct {
	waiters []*queueWaiter
	mu      sync.Mutex
	free    int
}

// queueWaiter is one queued acquire. Its permit arrives on grant (buffered,
// so a release never blocks); ownership transfers at the send, whether or not
// the waiter ever reads it.
type queueWaiter struct {
	grant chan struct{}
}

func newWaveQueue(permits int) *waveQueue {
	return &waveQueue{free: permits}
}

// acquire takes a permit, waiting in FIFO order until deadline. discard
// (the wave lost its last pre-start waiter) and shutdown abandon the wait; a
// permit granted concurrently with an abandon is passed on, except on a
// timeout, where the caller keeps it (it WAS admitted).
func (q *waveQueue) acquire(deadline time.Time, discard, shutdown <-chan struct{}) admitVerdict {
	q.mu.Lock()
	if q.free > 0 {
		q.free--
		q.mu.Unlock()
		return admitGranted
	}
	w := &queueWaiter{grant: make(chan struct{}, 1)}
	q.waiters = append(q.waiters, w)
	q.mu.Unlock()

	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case <-w.grant:
		return admitGranted
	case <-timer.C:
		if q.abandon(w) {
			return admitTimedOut
		}
		return admitGranted
	case <-discard:
		if !q.abandon(w) {
			q.release()
		}
		return admitDiscarded
	case <-shutdown:
		if !q.abandon(w) {
			q.release()
		}
		return admitShutdown
	}
}

// abandon removes w from the queue, reporting true when w was still queued.
// False means a concurrent release already granted w the permit, which the
// caller now owns.
func (q *waveQueue) abandon(w *queueWaiter) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i, x := range q.waiters {
		if x == w {
			q.waiters = append(q.waiters[:i], q.waiters[i+1:]...)
			return true
		}
	}
	return false
}

// release returns a permit: the queue head gets it, else the free count
// grows. free > 0 implies an empty queue, so FIFO order holds.
func (q *waveQueue) release() {
	q.mu.Lock()
	if len(q.waiters) > 0 {
		w := q.waiters[0]
		q.waiters = q.waiters[1:]
		q.mu.Unlock()
		w.grant <- struct{}{}
		return
	}
	q.free++
	q.mu.Unlock()
}
