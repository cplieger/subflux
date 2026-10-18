// Package status holds what subflux is doing and what is wrong with it and
// its providers: jobs, alerts as levels and provider states. Every mutation
// bumps one sequence number and wakes every observer.
package status

import (
	"crypto/rand"
	"sync"
)

// Store is the process's one status store.
type Store struct {
	p         Persister
	live      map[JobID]*liveJob
	unsaved   map[JobID]bool
	retryWake chan struct{}
	alerts    map[AlertSource]Alert
	retained  map[string]map[AlertSource]bool
	providers map[string]ProviderState
	// gen is closed and replaced by each mutation, so every observer that
	// read it before the mutation wakes.
	gen  chan struct{}
	mu   sync.Mutex
	seq  uint64
	boot [16]byte
}

func newStore() *Store {
	s := &Store{
		live:      map[JobID]*liveJob{},
		unsaved:   map[JobID]bool{},
		retryWake: make(chan struct{}, 1),
		alerts:    map[AlertSource]Alert{},
		retained:  map[string]map[AlertSource]bool{},
		providers: map[string]ProviderState{},
		gen:       make(chan struct{}),
	}
	_, _ = rand.Read(s.boot[:])
	return s
}

// Changed returns the current generation's channel, which the next mutation
// closes after bumping the sequence number. An observer re-reads Changed
// before each Snapshot, so every observer sees every change.
func (s *Store) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gen
}

// bump records a mutation; s.mu is held.
func (s *Store) bump() {
	s.seq++
	close(s.gen)
	s.gen = make(chan struct{})
}
