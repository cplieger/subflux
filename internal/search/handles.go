package search

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/cplieger/subflux/internal/language"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
	"github.com/cplieger/subflux/internal/statedb"
)

// The bounds of the server-held search results (8.7).
const (
	// maxCandidates bounds one manual search's answer.
	maxCandidates = 50
	// handleTTL is how long a handle downloads.
	handleTTL = 15 * time.Minute
	// maxHandles bounds the live handles of every owner together.
	maxHandles = 5_000
	// maxHandlesPerOwner bounds one owner's; a new search past it evicts
	// the owner's oldest searches.
	maxHandlesPerOwner = 250
	// handleBytes is a handle's randomness: 128 bits, 22 base64url
	// characters.
	handleBytes = 16
)

// Handle names one candidate of one manual search, for its owner only.
type Handle string

var (
	// ErrSearchCapacity is a search whose handles would pass maxHandles.
	ErrSearchCapacity = errors.New("search: too many search results held")
	// ErrCandidateUnknown is a handle unknown, expired, another owner's or
	// already taken.
	ErrCandidateUnknown = errors.New("search: candidate unknown")
)

// held is one handle's candidate: what Download needs and nothing else, so
// a handle's size is bounded by the wrapper's answer bounds.
type held struct {
	expires  time.Time
	from     *bound
	search   *heldSearch
	release  string
	ref      provider.Ref
	lang     language.Tag
	target   language.Tag
	variants []language.Variant
	owner    int64
	inst     statedb.InstanceID
	key      [32]byte
	facts    rank.Facts
	flags    language.Flags
	matched  rank.MatchedBy
	fit      rank.Fit
	machine  bool
	taken    bool
}

// heldSearch is one search's handles, evicted together.
type heldSearch struct{ handles []Handle }

// handleStore is an engine's live handles; an engine swap drops them.
type handleStore struct {
	byHandle map[Handle]*held
	// searches holds each owner's searches, oldest first.
	searches map[int64][]*heldSearch
	owned    map[int64]int
	mu       sync.Mutex
}

func newHandleStore() handleStore {
	return handleStore{byHandle: map[Handle]*held{}, searches: map[int64][]*heldSearch{}, owned: map[int64]int{}}
}

// room reports whether n more handles for owner would fit once expired
// handles are swept and the owner's oldest searches evicted.
func (s *handleStore) room(owner int64, n int, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	_, ok := s.planLocked(owner, n)
	return ok
}

// insert holds hs as one search of owner, each under a fresh handle, in
// order: the evictions it plans and the insertion commit together, or,
// past maxHandles, nothing changes and the answer is ErrSearchCapacity.
func (s *handleStore) insert(owner int64, hs []*held, now time.Time) ([]Handle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked(now)
	evict, ok := s.planLocked(owner, len(hs))
	if !ok {
		return nil, ErrSearchCapacity
	}
	for _, old := range s.searches[owner][:evict] {
		s.dropSearchLocked(owner, old)
	}
	s.searches[owner] = slices.Delete(s.searches[owner], 0, evict)
	search := &heldSearch{handles: make([]Handle, 0, len(hs))}
	for _, h := range hs {
		id := s.freshLocked()
		h.owner, h.search, h.expires = owner, search, now.Add(handleTTL)
		s.byHandle[id] = h
		search.handles = append(search.handles, id)
	}
	s.owned[owner] += len(hs)
	s.searches[owner] = append(s.searches[owner], search)
	return slices.Clone(search.handles), nil
}

// planLocked is how many of owner's oldest searches n more handles evict,
// and whether the store then holds them under maxHandles.
func (s *handleStore) planLocked(owner int64, n int) (int, bool) {
	evict, freed := 0, 0
	searches := s.searches[owner]
	for evict < len(searches) && s.owned[owner]-freed+n > maxHandlesPerOwner {
		freed += len(searches[evict].handles)
		evict++
	}
	return evict, len(s.byHandle)-freed+n <= maxHandles
}

// take marks h taken for owner and returns its candidate; false for a
// handle unknown, expired, another owner's or already taken.
func (s *handleStore) take(owner int64, h Handle, now time.Time) (*held, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.byHandle[h]
	if !ok || c.owner != owner || c.taken || !now.Before(c.expires) {
		return nil, false
	}
	c.taken = true
	return c, true
}

// giveBack makes a taken handle downloadable again.
func (s *handleStore) giveBack(c *held) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c.taken = false
}

// consume drops a taken handle for good.
func (s *handleStore) consume(h Handle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.byHandle[h]; ok {
		delete(s.byHandle, h)
		s.owned[c.owner]--
		c.search.handles = slices.DeleteFunc(c.search.handles, func(x Handle) bool { return x == h })
	}
}

// clear drops every handle, as an engine's drain does.
func (s *handleStore) clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	clear(s.byHandle)
	clear(s.searches)
	clear(s.owned)
}

func (s *handleStore) sweepLocked(now time.Time) {
	for owner, searches := range s.searches {
		live := searches[:0]
		for _, search := range searches {
			search.handles = slices.DeleteFunc(search.handles, func(h Handle) bool {
				if c := s.byHandle[h]; now.Before(c.expires) {
					return false
				}
				delete(s.byHandle, h)
				s.owned[owner]--
				return true
			})
			if len(search.handles) > 0 {
				live = append(live, search)
			}
		}
		if len(live) == 0 {
			delete(s.searches, owner)
			delete(s.owned, owner)
			continue
		}
		s.searches[owner] = live
	}
}

func (s *handleStore) dropSearchLocked(owner int64, search *heldSearch) {
	for _, h := range search.handles {
		delete(s.byHandle, h)
	}
	s.owned[owner] -= len(search.handles)
	search.handles = nil
}

// freshLocked is a handle no live one holds.
func (s *handleStore) freshLocked() Handle {
	for {
		var b [handleBytes]byte
		_, _ = rand.Read(b[:]) // crypto/rand.Read never fails (Go 1.24, crypto/rand docs)
		if h := Handle(base64.RawURLEncoding.EncodeToString(b[:])); s.byHandle[h] == nil {
			return h
		}
	}
}
