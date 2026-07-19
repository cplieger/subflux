// Package activity provides concurrent-safe activity and alert tracking
// for the subflux UI status indicator.
package activity

import (
	"strconv"
	"sync"
	"time"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
)

// ActivitySource is a typed string for activity entry source values.
type ActivitySource string //nolint:revive // stutters but renaming breaks callers

// Activity source constants.
const (
	SourceScheduled ActivitySource = "scheduled"
	SourceManual    ActivitySource = "manual"
)

// ScanKind identifies which scan endpoint family a scan activity belongs to.
// Together with the media fields it forms the structured scan scope carried
// on activity entries (background-scans S12): the UI reconstructs running
// scans per scope from these fields instead of parsing human Action/Detail
// strings.
type ScanKind string

// Scan kind constants, one per scan start.
const (
	ScanKindSeries ScanKind = "series"
	ScanKindSeason ScanKind = "season"
	ScanKindMovie  ScanKind = "movie"
	ScanKindItem   ScanKind = "item"
	ScanKindFull   ScanKind = "full"
)

// Outcome is the four-valued terminal outcome of a background scan runner.
// It replaces the former activityOK bool: a user-requested stop (cancelled)
// and process shutdown must never collapse into one state, and a cancelled
// scan must not end as success.
type Outcome string

// Scan outcome constants.
const (
	OutcomeCompleted Outcome = "completed"
	OutcomeFailed    Outcome = "failed"
	OutcomeCancelled Outcome = "cancelled"
	OutcomeShutdown  Outcome = "shutdown"
)

// ScanScope is the structured identity of a scan: which endpoint family
// started it and which media it covers. Zero fields mean "not applicable"
// (e.g. a full scan has only Kind). It is a parameter/matching struct; the
// fields are stored flat on the Entry for serialization.
type ScanScope struct {
	Kind      ScanKind
	MediaType api.MediaType
	MediaID   int
	Season    int
	Episode   int
}

// matches reports whether the entry carries exactly this scan scope.
func (sc ScanScope) matches(e *Entry) bool {
	return e.Kind == sc.Kind && e.MediaType == sc.MediaType &&
		e.MediaID == sc.MediaID && e.Season == sc.Season && e.Episode == sc.Episode
}

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
//
// Scan entries additionally carry their structured scope (Kind, MediaType,
// MediaID, Season, Episode) and the role required to cancel them
// (RequiredRole: user for per-item scans, admin for full scans). Cancellable
// is a serialization-time flag merged from the StopRegistry by the activity
// GET handler; it is never persisted on the stored entry.
type Entry struct {
	StartedAt    time.Time      `json:"started_at"`
	EndedAt      *time.Time     `json:"ended_at,omitempty"`
	ID           string         `json:"id"`
	Action       string         `json:"action"`
	Detail       string         `json:"detail"`
	Source       ActivitySource `json:"source"` // "scheduled" or "manual"
	Kind         ScanKind       `json:"kind,omitempty"`
	MediaType    api.MediaType  `json:"media_type,omitempty"`
	RequiredRole auth.Role      `json:"required_role,omitempty"`
	MediaID      int            `json:"media_id,omitempty"`
	Season       int            `json:"season,omitempty"`
	Episode      int            `json:"episode,omitempty"`
	Current      int            `json:"current,omitempty"`
	Total        int            `json:"total,omitempty"`
	Done         bool           `json:"done"`
	Queued       bool           `json:"queued,omitempty"`
	Cancelled    bool           `json:"cancelled,omitempty"`
	Failed       bool           `json:"failed,omitempty"`
	Cancellable  bool           `json:"cancellable,omitempty"`
}

// New creates an ActivityLog with the given max capacity.
func New(maxItems int) *Log {
	return &Log{maxItems: maxItems, index: make(map[string]int, maxItems)}
}

// Start records a new activity and returns its ID.
func (a *Log) Start(action, detail string, source ActivitySource) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.startLocked(Entry{Action: action, Detail: detail, Source: source})
}

// StartScan records a new scan activity carrying its structured scope and
// the role required to cancel it. Idempotent same-scope start: when an
// active (not done, not cancelled) entry with the same scope already exists,
// its ID is returned with existing=true and no new entry is created — the
// find-and-create pair runs under one lock so two concurrent same-scope
// starts cannot both create an entry.
func (a *Log) StartScan(action, detail string, source ActivitySource,
	scope ScanScope, role auth.Role,
) (id string, existing bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if activeID, ok := a.activeScanLocked(scope); ok {
		return activeID, true
	}
	id = a.startLocked(Entry{
		Action: action, Detail: detail, Source: source,
		Kind: scope.Kind, MediaType: scope.MediaType, MediaID: scope.MediaID,
		Season: scope.Season, Episode: scope.Episode,
		RequiredRole: role,
	})
	return id, false
}

// ActiveScan returns the ID of the live (not done, not cancelled) scan entry
// matching scope, if any. It is the read-only half of StartScan's same-scope
// idempotency: endpoints guarding a start by other means (the full-scan
// CompareAndSwap flag) use it to answer a duplicate start with the RUNNING
// scan's id instead of a conflict.
func (a *Log) ActiveScan(scope ScanScope) (string, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.activeScanLocked(scope)
}

// activeScanLocked is the shared scope lookup behind StartScan's idempotency
// and ActiveScan. Caller must hold the lock (read or write).
func (a *Log) activeScanLocked(scope ScanScope) (string, bool) {
	for i := range a.entries {
		e := &a.entries[i]
		if !e.Done && !e.Cancelled && scope.matches(e) {
			return e.ID, true
		}
	}
	return "", false
}

// startLocked assigns the next ID, stamps StartedAt, and appends the entry.
// Capacity pressure evicts the OLDEST COMPLETED entry; running (not-done)
// entries are never evicted — a busy system must not hide a live cancellable
// scan (the log may temporarily exceed maxItems when every entry is live).
// Caller must hold the write lock.
func (a *Log) startLocked(e Entry) string { //nolint:gocritic // hugeParam: single construction site
	a.nextID++
	id := strconv.Itoa(a.nextID)
	e.ID = id
	e.StartedAt = time.Now()
	a.entries = append(a.entries, e)
	if len(a.entries) > a.maxItems {
		a.evictCompletedLocked()
		a.rebuildIndex()
	} else {
		if a.index == nil {
			a.index = make(map[string]int, a.maxItems)
		}
		a.index[id] = len(a.entries) - 1
	}
	return id
}

// evictCompletedLocked removes oldest-first completed entries until the log
// fits maxItems or only running entries remain. Caller must hold the write
// lock and rebuild the index afterwards.
func (a *Log) evictCompletedLocked() {
	for len(a.entries) > a.maxItems {
		evicted := false
		for i := range a.entries {
			if a.entries[i].Done {
				a.entries = append(a.entries[:i], a.entries[i+1:]...)
				evicted = true
				break
			}
		}
		if !evicted {
			return
		}
	}
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
		a.finishLocked(i)
	}
}

// Fail marks an activity as done with failure.
func (a *Log) Fail(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.finishLocked(i)
		a.entries[i].Failed = true
	}
}

// FinishCancelled marks an activity as TERMINALLY cancelled:
// Done=true, Cancelled=true, EndedAt set. This is the terminal state a
// user-stopped scan reaches — unlike the queued-dismiss Cancel flag alone,
// the entry stops rendering as running and becomes prunable.
func (a *Log) FinishCancelled(id string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.finishLocked(i)
		a.entries[i].Cancelled = true
	}
}

// finishLocked stamps the terminal transition shared by End, Fail, and
// FinishCancelled: Done plus an independently allocated EndedAt. The
// timestamp is never a pointer into the entries backing array — compaction
// (Dismiss, PruneCompleted, ring eviction) copies rows between slots and
// append reuses freed capacity, so an interior pointer would read another
// row's storage after the slice shifts. Caller must hold the write lock.
func (a *Log) finishLocked(i int) {
	now := time.Now()
	a.entries[i].Done = true
	a.entries[i].EndedAt = &now
}

// Get returns a snapshot copy of the entry with the given ID. The copy is
// deep where it matters: mutating it — including through its EndedAt
// pointer — never touches the log's internal state (see snapshotEntry).
func (a *Log) Get(id string) (Entry, bool) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if i, ok := a.findEntry(id); ok {
		return snapshotEntry(&a.entries[i]), true
	}
	return Entry{}, false
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

// Entries returns a snapshot of all entries (for serialization). Each entry
// is deep-copied (see snapshotEntry): callers may mutate the result freely.
func (a *Log) Entries() []Entry {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]Entry, len(a.entries))
	for i := range a.entries {
		out[i] = snapshotEntry(&a.entries[i])
	}
	return out
}

// snapshotEntry copies an entry for handing outside the lock, re-allocating
// the EndedAt pointer so no snapshot ever aliases internal storage: a caller
// writing through EndedAt must never corrupt the log, and internal
// compaction must never change what an already-returned snapshot reads.
func snapshotEntry(e *Entry) Entry {
	out := *e
	if e.EndedAt != nil {
		t := *e.EndedAt
		out.EndedAt = &t
	}
	return out
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
