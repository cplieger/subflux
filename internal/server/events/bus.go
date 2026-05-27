// Package events provides an in-memory pub/sub event bus for server-sent events.
package events

import (
	"sync"
	"sync/atomic"
)

// RingBufferSize is the capacity of the shared ring buffer.
// Must be a power of 2 for efficient modulo via bitmask.
const RingBufferSize = 256

// EventBus is an in-memory pub/sub for server-sent events using a shared
// ring buffer. Publishers write to the ring; subscribers track their own
// read position and skip events they've fallen behind on.
type EventBus struct {
	notify   chan struct{}
	ring     [RingBufferSize]Event
	ringMu   sync.RWMutex
	writeAt  atomic.Uint64
	notifyMu sync.Mutex
	clients  atomic.Int32
}

// New creates a new event bus.
func New() *EventBus {
	eb := &EventBus{
		notify: make(chan struct{}),
	}
	return eb
}

// Subscription tracks a single subscriber's read position.
type Subscription struct {
	eb     *EventBus
	readAt uint64
	Done   atomic.Bool
}

// Subscribe registers a new client and returns a subscription + unsubscribe func.
func (eb *EventBus) Subscribe() (sub *Subscription, unsub func()) {
	eb.clients.Add(1)
	s := &Subscription{
		eb:     eb,
		readAt: eb.writeAt.Load(),
	}
	var once sync.Once
	return s, func() {
		once.Do(func() {
			s.Done.Store(true)
			eb.clients.Add(-1)
		})
	}
}

// WaitCh returns a channel that is closed when new events are published.
// The returned channel is safe to use in a select statement and does not
// spawn any goroutines (unlike the previous Wait() method).
func (eb *EventBus) WaitCh() <-chan struct{} {
	eb.notifyMu.Lock()
	ch := eb.notify
	eb.notifyMu.Unlock()
	return ch
}

// Next returns the next event for this subscriber, or false if caught up.
func (s *Subscription) Next() (Event, bool) {
	w := s.eb.writeAt.Load()
	if s.readAt >= w {
		return Event{}, false
	}
	// If we've fallen behind more than the buffer, skip to latest.
	if w-s.readAt > RingBufferSize {
		s.readAt = w - RingBufferSize
	}
	s.eb.ringMu.RLock()
	e := s.eb.ring[s.readAt%RingBufferSize]
	s.eb.ringMu.RUnlock()
	s.readAt++
	return e, true
}

// Publish sends an event to the ring buffer and wakes all subscribers.
// No-op when the bus is nil.
func (eb *EventBus) Publish(e Event) {
	if eb == nil {
		return
	}
	pos := eb.writeAt.Load()
	eb.ringMu.Lock()
	eb.ring[pos%RingBufferSize] = e
	eb.ringMu.Unlock()
	eb.writeAt.Add(1)

	// Wake all waiters by closing the current notify channel and swapping
	// in a fresh one. This is lock-free on the read path (WaitCh).
	eb.notifyMu.Lock()
	old := eb.notify
	eb.notify = make(chan struct{})
	eb.notifyMu.Unlock()
	close(old)
}

// ClientCount returns the number of connected SSE clients.
func (eb *EventBus) ClientCount() int {
	return int(eb.clients.Load())
}
