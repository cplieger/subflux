package api

import "sync"

// logOnce is a bounded set for "log at most once per key" deduplication.
// Once the set reaches its capacity, new keys are silently ignored (the
// dedup guarantee degrades gracefully rather than growing without bound).
type logOnce struct {
	seen     map[string]struct{}
	mu       sync.Mutex
	capacity int
}

// newLogOnce creates a logOnce with the given capacity.
func newLogOnce(capacity int) *logOnce {
	return &logOnce{
		seen:     make(map[string]struct{}, capacity),
		capacity: capacity,
	}
}

// first returns true the first time key is seen, false thereafter.
// Once the set is full, all new keys return false (no memory growth).
func (l *logOnce) first(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.seen[key]; ok {
		return false
	}
	if len(l.seen) >= l.capacity {
		return false
	}
	l.seen[key] = struct{}{}
	return true
}
