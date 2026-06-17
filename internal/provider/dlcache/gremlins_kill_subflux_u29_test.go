package dlcache

import "testing"

// TestGk_subflux_u29_Put_itemSizeBoundary kills dlcache.go:51
// CONDITIONALS_BOUNDARY (">" vs ">=") and CONDITIONALS_NEGATION (">" vs "<=")
// on "int64(len(data)) > dc.maxItemSize".
//
//   - At the boundary len == maxItemSize the original stores ("10 > 10" is
//     false); both mutants reject ("10 >= 10" and "10 <= 10" are true).
//   - Over the limit the original rejects ("11 > 10" true); the negation
//     mutant stores ("11 <= 10" false → not rejected).
func TestGk_subflux_u29_Put_itemSizeBoundary(t *testing.T) {
	dc := New(8, 10) // maxEntries 8, maxItemSize 10 bytes

	atLimit := []byte("0123456789") // exactly 10 bytes
	if ok := dc.Put("at", atLimit, nil); !ok {
		t.Fatalf("Put(len=%d, maxItemSize=10) = false, want true (boundary stores)", len(atLimit))
	}
	if got, found := dc.Get("at"); !found || len(got) != 10 {
		t.Fatalf("Get(\"at\") = %q, %v; want 10 bytes, true", got, found)
	}

	over := []byte("0123456789X") // 11 bytes
	if ok := dc.Put("over", over, nil); ok {
		t.Fatalf("Put(len=%d, maxItemSize=10) = true, want false (over-limit rejected)", len(over))
	}
	if _, found := dc.Get("over"); found {
		t.Fatalf("Get(\"over\") found = true, want false (was rejected)")
	}
}

// TestGk_subflux_u29_Put_evictsWhenFull kills dlcache.go:62
// CONDITIONALS_BOUNDARY (">=" vs ">") and CONDITIONALS_NEGATION (">=" vs "<")
// on the eviction trigger "len(dc.cache) >= dc.maxEntries". With a full cache
// (2/2) the original evicts the oldest then stores the new key (Put true);
// both mutants skip the evict, so the post-evict ">=" check rejects (Put
// false).
func TestGk_subflux_u29_Put_evictsWhenFull(t *testing.T) {
	dc := New(2, 1024)
	if ok := dc.Put("a", []byte("A"), nil); !ok {
		t.Fatalf("Put(\"a\") = false, want true")
	}
	if ok := dc.Put("b", []byte("B"), nil); !ok {
		t.Fatalf("Put(\"b\") = false, want true")
	}
	// Cache is full (2/2). A third Put must evict the oldest and store "c".
	if ok := dc.Put("c", []byte("C"), nil); !ok {
		t.Fatalf("Put(\"c\") on full cache = false, want true (evict then store)")
	}
	if got, found := dc.Get("c"); !found || string(got) != "C" {
		t.Fatalf("Get(\"c\") = %q, %v; want \"C\", true", got, found)
	}
}

// TestGk_subflux_u29_Put_zeroMaxEntriesRejects kills dlcache.go:65
// CONDITIONALS_BOUNDARY (">=" vs ">") and CONDITIONALS_NEGATION (">=" vs "<")
// on the post-evict "len(dc.cache) >= dc.maxEntries". With maxEntries == 0
// the heap is empty so evictOldest is a no-op and len stays 0; the original
// "0 >= 0" rejects (Put false). The boundary mutant "0 > 0" and the negation
// mutant "0 < 0" are both false, so the entry is wrongly stored (Put true).
func TestGk_subflux_u29_Put_zeroMaxEntriesRejects(t *testing.T) {
	dc := New(0, 1024)
	if ok := dc.Put("k", []byte("v"), nil); ok {
		t.Fatalf("Put on maxEntries=0 cache = true, want false (cannot store)")
	}
	if _, found := dc.Get("k"); found {
		t.Fatalf("Get(\"k\") found = true, want false (maxEntries=0 stores nothing)")
	}
}
