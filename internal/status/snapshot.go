package status

import (
	"cmp"
	"maps"
	"slices"
	"time"
)

// Health is a provider's state.
type Health uint8

// The provider states; the zero Health is none of them.
const (
	OK Health = iota + 1
	Degraded
	Disabled
)

// ProviderState is one provider's health, until when it holds, and why.
type ProviderState struct {
	Until  time.Time
	ID     string
	Reason string
	Health Health
}

// SetProvider records p, its Reason cut as an alert's text is.
func (s *Store) SetProvider(p ProviderState) {
	p.Reason = cutText(p.Reason)
	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, ok := s.providers[p.ID]; ok && cur.Health == p.Health && cur.Reason == p.Reason && cur.Until.Equal(p.Until) {
		return
	}
	s.providers[p.ID] = p
	s.bump()
}

// Snapshot is one consistent reading of the store.
type Snapshot struct {
	// Jobs are the live jobs, newest started first; Alerts are ordered by
	// source, Providers by id.
	Jobs      []Job
	Alerts    []Alert
	Providers []ProviderState
	// Boot names this process, so an observer tells a restart from a
	// sequence number that went back.
	Boot [16]byte
}

// Snapshot answers the store's state and the sequence number it was read at.
func (s *Store) Snapshot() (snap Snapshot, seq uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	alerts := slices.SortedFunc(maps.Values(s.alerts), func(a, b Alert) int { return cmp.Compare(a.Source, b.Source) })
	providers := slices.SortedFunc(maps.Values(s.providers), func(a, b ProviderState) int { return cmp.Compare(a.ID, b.ID) })
	return Snapshot{Jobs: s.liveJobs(), Alerts: alerts, Providers: providers, Boot: s.boot}, s.seq
}
