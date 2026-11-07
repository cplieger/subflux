package providercalls

import (
	"context"
	"slices"
	"sync"
	"time"
)

// maxInFlight is each provider's call slots, both classes together.
const maxInFlight = 2

// slots is a provider's in-flight bound, handing a freed slot to the oldest
// Interactive waiter before any Automatic one.
type slots struct {
	waiting [Interactive + 1][]chan struct{} // by class
	free    int
	mu      sync.Mutex
}

func (s *slots) acquire(ctx context.Context, c Class) error {
	s.mu.Lock()
	if s.free > 0 && len(s.waiting[Interactive]) == 0 && (c == Interactive || len(s.waiting[Automatic]) == 0) {
		s.free--
		s.mu.Unlock()
		return nil
	}
	ready := make(chan struct{})
	s.waiting[c] = append(s.waiting[c], ready)
	s.mu.Unlock()
	select {
	case <-ready:
		return nil
	case <-ctx.Done():
	}
	s.mu.Lock()
	i := slices.Index(s.waiting[c], ready)
	if i >= 0 {
		s.waiting[c] = slices.Delete(s.waiting[c], i, i+1)
	}
	s.mu.Unlock()
	if i < 0 {
		// The slot was handed over as ctx ended: pass it on.
		s.release()
	}
	return ctx.Err()
}

func (s *slots) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range [...]Class{Interactive, Automatic} {
		if len(s.waiting[c]) > 0 {
			close(s.waiting[c][0])
			s.waiting[c] = slices.Delete(s.waiting[c], 0, 1)
			return
		}
	}
	s.free++
}

// pace waits for the provider's next Automatic start, the limiter spacing
// them by the latest binding's interval.
func (p *providerState) pace(ctx context.Context) error {
	r := p.limiter.Reserve()
	d := r.Delay()
	if d == 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		r.Cancel()
		return ctx.Err()
	}
}
