// Package activity provides concurrent-safe activity and alert tracking
// for the subflux UI status indicator.
package activity

import (
	"strconv"
	"sync"
	"time"

	"github.com/cplieger/auth/v5"
	"github.com/cplieger/subflux/internal/subflux"
)

// Source is a typed string for activity entry source values.
type Source string

// Activity source constants.
const (
	SourceScheduled Source = "scheduled"
	SourceManual    Source = "manual"
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
	MediaType subflux.MediaType
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
	onUpsert func(Entry)
	onRemove func(Entry)
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
	StartedAt    time.Time         `json:"started_at"`
	EndedAt      *time.Time        `json:"ended_at,omitempty"`
	ID           string            `json:"id"`
	Action       string            `json:"action"`
	Detail       string            `json:"detail"`
	Source       Source            `json:"source"` // "scheduled" or "manual"
	Kind         ScanKind          `json:"kind,omitempty"`
	MediaType    subflux.MediaType `json:"media_type,omitempty"`
	RequiredRole auth.Role         `json:"required_role,omitempty"`
	MediaID      int               `json:"media_id,omitempty"`
	Season       int               `json:"season,omitempty"`
	Episode      int               `json:"episode,omitempty"`
	Current      int               `json:"current,omitempty"`
	Total        int               `json:"total,omitempty"`
	Done         bool              `json:"done"`
	Queued       bool              `json:"queued,omitempty"`
	Cancelled    bool              `json:"cancelled,omitempty"`
	Failed       bool              `json:"failed,omitempty"`
	Cancellable  bool              `json:"cancellable,omitempty"`
}

// New creates a Log with the given max capacity.
func New(maxItems int) *Log {
	return &Log{maxItems: maxItems, index: make(map[string]int, maxItems)}
}

// SetOnUpsert installs the observer called with the post-mutation entry
// snapshot after every entry mutation (start, queue flip, progress, terminal
// transition, queued-cancel). The hook is fired AFTER the log's lock is
// released, so it may safely re-enter the log; the Log cannot import the
// events bus, which is why the server injects the publisher here.
func (a *Log) SetOnUpsert(fn func(Entry)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onUpsert = fn
}

// SetOnRemove installs the observer called once per entry removed from the
// log — dismiss, prune, and cap eviction alike — with the under-lock entry
// snapshot, fired AFTER the lock is released (removals are collected under
// the lock). Like SetOnUpsert, the hook may re-enter the log.
func (a *Log) SetOnRemove(fn func(Entry)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onRemove = fn
}

// notify collects the hook calls a mutation decides under the Log's lock and
// fires them once the lock is released: callers defer fire BEFORE taking the
// lock, so the LIFO defer order runs it after the deferred unlock. The hook
// funcs are captured under the same lock, so a concurrent SetOn* cannot race
// the read.
type notify struct {
	onUpsert func(Entry)
	onRemove func(Entry)
	upserts  []Entry
	removes  []Entry
}

// fire invokes the collected hook calls, upserts before removes (a start's
// own upsert precedes the eviction removes it caused; per-entry ordering is
// unaffected — an evicted entry's upserts all predate its eviction).
func (n *notify) fire() {
	for i := range n.upserts {
		n.onUpsert(n.upserts[i])
	}
	for i := range n.removes {
		n.onRemove(n.removes[i])
	}
}

// noteUpsertLocked queues the post-mutation snapshot of entries[i] for the
// upsert hook. Caller must hold the write lock.
func (a *Log) noteUpsertLocked(n *notify, i int) {
	if a.onUpsert == nil {
		return
	}
	n.onUpsert = a.onUpsert
	n.upserts = append(n.upserts, snapshotEntry(&a.entries[i]))
}

// noteRemoveLocked queues e's snapshot for the remove hook, taken under the
// lock before the entry leaves the slice. Caller must hold the write lock.
func (a *Log) noteRemoveLocked(n *notify, e *Entry) {
	if a.onRemove == nil {
		return
	}
	n.onRemove = a.onRemove
	n.removes = append(n.removes, snapshotEntry(e))
}

// Start records a new activity and returns its ID.
func (a *Log) Start(action, detail string, source Source) string {
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.startLocked(&n, Entry{Action: action, Detail: detail, Source: source})
}

// StartScan records a new scan activity carrying its structured scope and
// the role required to cancel it. Idempotent same-scope start: when an
// active (not done, not cancelled) entry with the same scope already exists,
// its ID is returned with existing=true and no new entry is created — the
// find-and-create pair runs under one lock so two concurrent same-scope
// starts cannot both create an entry.
func (a *Log) StartScan(action, detail string, source Source,
	scope ScanScope, role auth.Role,
) (id string, existing bool) {
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	if activeID, ok := a.activeScanLocked(scope); ok {
		return activeID, true
	}
	id = a.startLocked(&n, Entry{
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

// startLocked assigns the next ID, stamps StartedAt, appends the entry, and
// queues its upsert plus any eviction removes on n. Capacity pressure evicts
// the OLDEST COMPLETED entry; running (not-done)
// entries are never evicted — a busy system must not hide a live cancellable
// scan (the log may temporarily exceed maxItems when every entry is live).
// Caller must hold the write lock.
func (a *Log) startLocked(n *notify, e Entry) string { //nolint:gocritic // hugeParam: single construction site
	a.nextID++
	id := strconv.Itoa(a.nextID)
	e.ID = id
	e.StartedAt = time.Now()
	a.entries = append(a.entries, e)
	if len(a.entries) > a.maxItems {
		a.evictCompletedLocked(n)
		a.rebuildIndex()
	} else {
		if a.index == nil {
			a.index = make(map[string]int, a.maxItems)
		}
		a.index[id] = len(a.entries) - 1
	}
	if i, ok := a.findEntry(id); ok {
		a.noteUpsertLocked(n, i)
	}
	return id
}

// evictCompletedLocked removes oldest-first completed entries until the log
// fits maxItems or only running entries remain, queueing each eviction on n.
// Caller must hold the write lock and rebuild the index afterwards.
func (a *Log) evictCompletedLocked(n *notify) {
	for len(a.entries) > a.maxItems {
		evicted := false
		for i := range a.entries {
			if a.entries[i].Done {
				a.noteRemoveLocked(n, &a.entries[i])
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
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.entries[i].Queued = queued
		if !queued {
			a.entries[i].StartedAt = time.Now()
		}
		a.noteUpsertLocked(&n, i)
	}
}

// End marks an activity as done.
func (a *Log) End(id string) {
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.finishLocked(i)
		a.noteUpsertLocked(&n, i)
	}
}

// Fail marks an activity as done with failure.
func (a *Log) Fail(id string) {
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.finishLocked(i)
		a.entries[i].Failed = true
		a.noteUpsertLocked(&n, i)
	}
}

// FinishCancelled marks an activity as TERMINALLY cancelled:
// Done=true, Cancelled=true, EndedAt set. This is the terminal state a
// user-stopped scan reaches — unlike the queued-dismiss Cancel flag alone,
// the entry stops rendering as running and becomes prunable.
func (a *Log) FinishCancelled(id string) {
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.finishLocked(i)
		a.entries[i].Cancelled = true
		a.noteUpsertLocked(&n, i)
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
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	if i, ok := a.findEntry(id); ok {
		a.entries[i].Current = current
		a.entries[i].Total = total
		if detail != "" {
			a.entries[i].Detail = detail
		}
		a.noteUpsertLocked(&n, i)
	}
}

// Dismiss removes a completed activity by ID.
func (a *Log) Dismiss(id string) {
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	i, ok := a.findEntry(id)
	if !ok || !a.entries[i].Done {
		return
	}
	a.noteRemoveLocked(&n, &a.entries[i])
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
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	i, ok := a.findEntry(id)
	if !ok {
		return false
	}
	if a.entries[i].Queued && !a.entries[i].Done {
		a.entries[i].Cancelled = true
		a.noteUpsertLocked(&n, i)
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

// PruneCompleted removes completed activities older than maxAge. The prune
// POLICY lives here; the 60s ticker goroutine driving it is the server's
// (runActivityPrune), and it is the ONE owner of pruning — the activity GET
// no longer prunes on read.
func (a *Log) PruneCompleted(maxAge time.Duration) {
	var n notify
	defer n.fire()
	a.mu.Lock()
	defer a.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	kept := 0
	for i := range a.entries {
		if a.entries[i].Done && a.entries[i].EndedAt != nil && a.entries[i].EndedAt.Before(cutoff) {
			a.noteRemoveLocked(&n, &a.entries[i])
			continue
		}
		a.entries[kept] = a.entries[i]
		kept++
	}
	a.entries = a.entries[:kept]
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
