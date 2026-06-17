package dlcache

// Round-2 mutant-killing test for internal/provider/dlcache.
//
// Kills dlcache.go:62:19 CONDITIONALS_NEGATION (`if len(dc.cache) >= dc.maxEntries`
// -> `if len(dc.cache) < dc.maxEntries`). The guard evicts the oldest entry when
// the cache is full so a new entry can be stored; the negated form evicts only
// when NOT full, so storing into a full cache fails instead of evicting.

import "testing"

func TestGkSubfluxR2_PutEvictsWhenFull(t *testing.T) {
	dc := New(1, 1<<20) // capacity 1

	if !dc.Put("a", []byte("A"), nil) {
		t.Fatal("Put(a) into empty cache should succeed")
	}
	if !dc.Put("b", []byte("B"), nil) {
		t.Error("Put(b) into full cache should evict the oldest and succeed")
	}
	if _, ok := dc.Get("a"); ok {
		t.Error("entry a should have been evicted")
	}
	if _, ok := dc.Get("b"); !ok {
		t.Error("entry b should be cached after eviction")
	}
}
