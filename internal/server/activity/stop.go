// stop.go: the stop registry for running background scans.
//
// StopRegistry is a SIBLING of Log, not part of it: ScanGuard was extracted
// from activity.Log precisely to separate coordination concerns from data
// concerns, and storing live stop callbacks inside the Log would re-import
// that coupling. The server composes both — the registry answers "can this
// activity be stopped right now?", the Log owns the entry data.

package activity

import "sync"

// StopResult is the outcome of a stop request against the registry.
type StopResult int

// Stop request outcomes. StopNotCancellable is never returned by the
// registry itself (every registration is stoppable); it exists as shared
// vocabulary for the composing endpoint, which maps StopNotFound plus an
// existing terminal entry onto "not cancellable" (409).
const (
	StopRequested StopResult = iota
	StopAlreadyStopping
	StopNotFound
	StopNotCancellable
)

// stopEntry pairs a registered stop callback with its requested flag, which
// makes a second RequestStop idempotent (no double callback invocation).
type stopEntry struct {
	stop      func()
	requested bool
}

// StopRegistry tracks the live stop callbacks of running background scans,
// keyed by activity ID. The zero value is ready to use.
type StopRegistry struct {
	stops map[string]*stopEntry
	mu    sync.Mutex
}

// RegisterStop stores the stop callback for a running scan and returns the
// matching unregister func. The registration MUST be released on every
// terminal transition (end, fail, cancelled) or failed scans leak
// registrations and keep reporting cancellable; callers release via defer so
// every outcome path (including panics) unregisters. Unregister is
// idempotent.
func (r *StopRegistry) RegisterStop(id string, stop func()) (unregister func()) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stops == nil {
		r.stops = make(map[string]*stopEntry)
	}
	r.stops[id] = &stopEntry{stop: stop}
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.stops, id)
	}
}

// RequestStop asks the scan registered under id to stop gracefully.
// The first request invokes the stop callback AFTER releasing the registry
// lock (a callback must never run under any mutex) and returns
// StopRequested; repeated requests are idempotent (StopAlreadyStopping,
// callback not re-invoked). An id without a live registration — never
// registered, or already released by a terminal transition (the
// cancel-vs-end race resolves here as a no-op) — returns StopNotFound.
func (r *StopRegistry) RequestStop(id string) StopResult {
	r.mu.Lock()
	e, ok := r.stops[id]
	if !ok {
		r.mu.Unlock()
		return StopNotFound
	}
	if e.requested {
		r.mu.Unlock()
		return StopAlreadyStopping
	}
	e.requested = true
	stop := e.stop
	r.mu.Unlock()
	stop()
	return StopRequested
}

// Cancellable reports whether a live stop registration exists for id — the
// serialization-time source of the activity DTO's cancellable flag.
func (r *StopRegistry) Cancellable(id string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.stops[id]
	return ok
}
