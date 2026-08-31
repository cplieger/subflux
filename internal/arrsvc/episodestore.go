package arrsvc

import (
	"maps"
	"sync"
	"time"
)

// maxResidentEpisodeEntries bounds the episode lists one read table keeps
// resident. The other key families are O(1) per arr instance; this one carries
// an entry per SERIES, written both by plain reads and by the scan's
// write-through, so its resident set grows with the library — and the shared
// TTL cache reclaims nothing, reporting an expired entry as a miss while
// keeping its payload until that key is written again, a day later for a series
// the scan visits once a cycle. 64 exceeds any reader set that can share an
// entry inside one arrCacheTTL and caps the family in single-digit MB.
const maxResidentEpisodeEntries = 64

// episodeStore is the episodes half of a read table's storage: the same
// insert-time TTL the shared cache applies, plus a bound on retention.
// Reaching the bound reclaims expired entries first and drops the map whole
// only when none were expired; a discarded live entry costs one arr fetch on
// the next read of that series and was at most one TTL from being a miss.
type episodeStore struct {
	entries map[string]episodeEntry
	mu      sync.Mutex
}

// episodeEntry is one stored episode list and the instant it stops being
// served, computed at insert exactly as the shared cache computes it.
type episodeEntry struct {
	entry   readEntry
	expires time.Time
}

func newEpisodeStore() *episodeStore {
	return &episodeStore{entries: make(map[string]episodeEntry)}
}

// get returns the entry for key, reporting an expired one as a miss.
func (s *episodeStore) get(key string) (readEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[key]
	if !ok || time.Now().After(e.expires) {
		return readEntry{}, false
	}
	return e.entry, true
}

// put stores an entry under the bound. Overwriting a held key never grows the
// map, so only a new key can force a reclaim.
func (s *episodeStore) put(key string, e readEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, held := s.entries[key]; !held && len(s.entries) >= maxResidentEpisodeEntries {
		now := time.Now()
		maps.DeleteFunc(s.entries, func(_ string, held episodeEntry) bool {
			return now.After(held.expires)
		})
		if len(s.entries) >= maxResidentEpisodeEntries {
			s.entries = make(map[string]episodeEntry)
		}
	}
	s.entries[key] = episodeEntry{entry: e, expires: time.Now().Add(arrCacheTTL)}
}

// resident reports how many entries the store holds, expired ones included:
// the bound is about retained memory, not about hits.
func (s *episodeStore) resident() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}
