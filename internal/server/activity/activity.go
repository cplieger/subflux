// Package activity provides concurrent-safe activity and alert tracking
// for the subflux UI status indicator.
package activity

import (
	"strconv"
	"sync"
	"time"
)

// ActivitySource is a typed string for activity entry source values.
type ActivitySource string //nolint:revive // stutters but renaming breaks callers

// Activity source constants.
const (
	SourceScheduled ActivitySource = "scheduled"
	SourceManual    ActivitySource = "manual"
)

// DefaultPruneAge is the duration after which completed activities are pruned.
const DefaultPruneAge = 15 * time.Minute

// Log tracks recent actions for the UI status indicator.
type Log struct {
	index    map[string]int
	entries  []Entry
	maxItems int
	nextID   int
	mu       sync.RWMutex
}

// Entry represents an ongoing or recent action.
type Entry struct {
	StartedAt  time.Time      `json:"started_at"`
	EndedAt    *time.Time     `json:"ended_at,omitempty"`
	EndedAtVal time.Time      `json:"-"` // backing store for EndedAt to avoid heap alloc
	ID         string         `json:"id"`
	Action     string         `json:"action"`
	Detail     string         `json:"detail"`
	Source     ActivitySource `json:"source"` // "scheduled" or "manual"
	Done       bool           `json:"done"`
	Queued     bool           `json:"queued,omitempty"`
	Cancelled  bool           `json:"cancelled,omitempty"`
	Failed     bool           `json:"failed,omitempty"`
	Current    int            `json:"current,omitempty"`
	Total      int            `json:"total,omitempty"`
}

// New creates an ActivityLog with the given max capacity.
func New(maxItems int) *Log {
	return &Log{maxItems: maxItems, index: make(map[string]int, maxItems)}
}

// Start records a new activity and returns its ID.
func (a *Log) Start(action, detail string, source ActivitySource) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	id := strconv.Itoa(a.nextID)
	a.entries = append(a.entries, Entry{
		ID: id, Action: action, Detail: detail,
		Source:    source,
		StartedAt: time.Now(),
	})
	if len(a.entries) > a.maxItems {
		a.entries = a.entries[len(a.entries)-a.maxItems:]
		a.rebuildIndex()
	} else {
		if a.index == nil {
			a.index = make(map[string]int, a.maxItems)
		}
		a.index[id] = len(a.entries) - 1
	}
	return id
}

// SetQueued marks an activity as queued (waiting to run).
func (a *Log) SetQueued(id string, queued bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.entries[i].Queued = queued
		if !queued {
			a.entries[i].StartedAt = time.Now()
		}
	}
}

// End marks an activity as done.
func (a *Log) End(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.entries[i].Done = true
		a.entries[i].EndedAtVal = time.Now()
		a.entries[i].EndedAt = &a.entries[i].EndedAtVal
	}
}

// Fail marks an activity as done with failure.
func (a *Log) Fail(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.entries[i].Done = true
		a.entries[i].Failed = true
		a.entries[i].EndedAtVal = time.Now()
		a.entries[i].EndedAt = &a.entries[i].EndedAtVal
	}
}

// Progress updates the current/total counters and detail for an activity.
func (a *Log) Progress(id string, current, total int, detail string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.entries[i].Current = current
		a.entries[i].Total = total
		if detail != "" {
			a.entries[i].Detail = detail
		}
	}
}

// Dismiss removes a completed activity by ID.
func (a *Log) Dismiss(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	i, ok := a.findEntry(id)
	if !ok || !a.entries[i].Done {
		return
	}
	a.entries = append(a.entries[:i], a.entries[i+1:]...)
	delete(a.index, id)
	for k, idx := range a.index {
		if idx > i {
			a.index[k] = idx - 1
		}
	}
}

// Cancel marks a queued activity as cancelled. Returns true if found and cancelled.
func (a *Log) Cancel(id string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	i, ok := a.findEntry(id)
	if !ok {
		return false
	}
	if a.entries[i].Queued && !a.entries[i].Done {
		a.entries[i].Cancelled = true
		return true
	}
	return false
}

// IsCancelled checks if an activity has been cancelled.
func (a *Log) IsCancelled(id string) bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if i, ok := a.findEntry(id); ok {
		return a.entries[i].Cancelled
	}
	return false
}

// PruneCompleted removes completed activities older than maxAge.
func (a *Log) PruneCompleted(maxAge time.Duration) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	n := 0
	for i := range a.entries {
		if a.entries[i].Done && a.entries[i].EndedAt != nil && a.entries[i].EndedAt.Before(cutoff) {
			continue
		}
		a.entries[n] = a.entries[i]
		n++
	}
	a.entries = a.entries[:n]
	a.rebuildIndex()
}

// Entries returns a snapshot of all entries (for serialization).
func (a *Log) Entries() []Entry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Entry, len(a.entries))
	copy(out, a.entries)
	return out
}

// RLock acquires a read lock (for test inspection).
func (a *Log) RLock() { a.mu.RLock() }

// RUnlock releases a read lock.
func (a *Log) RUnlock() { a.mu.RUnlock() }

// Lock acquires a write lock (for test manipulation).
func (a *Log) Lock() { a.mu.Lock() }

// Unlock releases a write lock.
func (a *Log) Unlock() { a.mu.Unlock() }

// EntriesUnsafe returns the internal entries slice without copying.
// Caller must hold the lock.
func (a *Log) EntriesUnsafe() []Entry { return a.entries }

// AppendEntry appends an entry directly (for test setup). Caller must hold the lock.
func (a *Log) AppendEntry(e Entry) { //nolint:gocritic // hugeParam: exported test helper
	a.entries = append(a.entries, e)
	if a.index == nil {
		a.index = make(map[string]int, a.maxItems)
	}
	a.index[e.ID] = len(a.entries) - 1
}

// rebuildIndex rebuilds the ID→index map from the entries slice.
func (a *Log) rebuildIndex() {
	if a.index == nil {
		a.index = make(map[string]int, a.maxItems)
	}
	clear(a.index)
	for i := range a.entries {
		a.index[a.entries[i].ID] = i
	}
}

// findEntry returns the index of the entry with the given ID.
func (a *Log) findEntry(id string) (int, bool) {
	if a.index != nil {
		if i, ok := a.index[id]; ok {
			return i, true
		}
	}
	for i := range a.entries {
		if a.entries[i].ID == id {
			return i, true
		}
	}
	return 0, false
}
