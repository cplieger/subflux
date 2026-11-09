package status

import (
	"cmp"
	"maps"
	"slices"
)

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
