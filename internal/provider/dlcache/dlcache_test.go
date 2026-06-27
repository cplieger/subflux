package dlcache

import "testing"

// TestPut_itemSizeBoundary pins the maxItemSize gate: an item exactly at the
// limit is stored, one byte over is rejected.
func TestPut_itemSizeBoundary(t *testing.T) {
	t.Parallel()
	dc := New(8, 10) // maxItemSize 10 bytes

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
		t.Fatal("Get(\"over\") found = true, want false (was rejected)")
	}
}

// TestPut_evictsOldestWhenFull pins the eviction trigger: storing into a full
// cache evicts the least-recently-used entry and stores the new one.
func TestPut_evictsOldestWhenFull(t *testing.T) {
	t.Parallel()
	dc := New(2, 1024)
	if ok := dc.Put("a", []byte("A"), nil); !ok {
		t.Fatal("Put(\"a\") = false, want true")
	}
	if ok := dc.Put("b", []byte("B"), nil); !ok {
		t.Fatal("Put(\"b\") = false, want true")
	}
	// Cache is full (2/2). A third Put must evict the oldest ("a") and store "c".
	if ok := dc.Put("c", []byte("C"), nil); !ok {
		t.Fatal("Put(\"c\") on full cache = false, want true (evict then store)")
	}
	if _, found := dc.Get("a"); found {
		t.Error("entry \"a\" should have been evicted as the oldest")
	}
	if got, found := dc.Get("c"); !found || string(got) != "C" {
		t.Errorf("Get(\"c\") = %q, %v; want \"C\", true", got, found)
	}
}

// TestPut_zeroMaxEntriesRejects pins the post-evict capacity check: with
// maxEntries 0 the cache can store nothing (eviction cannot make room).
func TestPut_zeroMaxEntriesRejects(t *testing.T) {
	t.Parallel()
	dc := New(0, 1024)
	if ok := dc.Put("k", []byte("v"), nil); ok {
		t.Fatal("Put on maxEntries=0 cache = true, want false (cannot store)")
	}
	if _, found := dc.Get("k"); found {
		t.Fatal("Get(\"k\") found = true, want false (maxEntries=0 stores nothing)")
	}
}

// TestPut_existingKeyKeepsOriginalData: re-Putting an existing key reports
// success but does not overwrite the stored data.
func TestPut_existingKeyKeepsOriginalData(t *testing.T) {
	t.Parallel()
	dc := New(4, 1024)
	dc.Put("k", []byte("first"), nil)
	if ok := dc.Put("k", []byte("second"), nil); !ok {
		t.Fatal("re-Put of existing key = false, want true")
	}
	if got, _ := dc.Get("k"); string(got) != "first" {
		t.Errorf("Get(\"k\") = %q, want \"first\" (re-Put must not overwrite)", got)
	}
}

// TestGet_refreshesRecencyForLRU: a Get marks the entry most-recently-used, so
// a subsequent eviction drops a different, older entry. This is the only path
// that updates an entry's recency on read.
func TestGet_refreshesRecencyForLRU(t *testing.T) {
	t.Parallel()
	dc := New(2, 1024)
	dc.Put("a", []byte("A"), nil)
	dc.Put("b", []byte("B"), nil)

	// Touch "a" so it becomes newer than "b".
	if _, found := dc.Get("a"); !found {
		t.Fatal("Get(\"a\") not found before eviction")
	}

	// Inserting "c" must now evict "b" (the least-recently-used), not "a".
	dc.Put("c", []byte("C"), nil)
	if _, found := dc.Get("b"); found {
		t.Error("entry \"b\" should have been evicted as least-recently-used after Get(\"a\")")
	}
	if _, found := dc.Get("a"); !found {
		t.Error("entry \"a\" should survive eviction after being refreshed by Get")
	}
	if _, found := dc.Get("c"); !found {
		t.Error("entry \"c\" should be present after insertion")
	}
}

// TestPut_onSaturatedWhenItemTooBig: the callback fires when an item is
// rejected for exceeding maxItemSize.
func TestPut_onSaturatedWhenItemTooBig(t *testing.T) {
	t.Parallel()
	dc := New(4, 5) // maxItemSize 5 bytes
	calls := 0
	ok := dc.Put("big", []byte("123456"), func() { calls++ }) // 6 bytes
	if ok {
		t.Error("Put of oversized item = true, want false")
	}
	if calls != 1 {
		t.Errorf("onSaturated called %d times, want 1", calls)
	}
}

// TestPut_onSaturatedFiresOncePerClearCycle: onSaturated is invoked at most
// once between Clear calls, then re-armed by Clear.
func TestPut_onSaturatedFiresOncePerClearCycle(t *testing.T) {
	t.Parallel()
	dc := New(0, 1024) // maxEntries 0: every Put saturates (no room)
	calls := 0
	cb := func() { calls++ }

	dc.Put("a", []byte("A"), cb)
	dc.Put("b", []byte("B"), cb)
	if calls != 1 {
		t.Errorf("onSaturated fired %d times before Clear, want 1 (once per cycle)", calls)
	}

	dc.Clear() // re-arms the saturation guard
	dc.Put("c", []byte("C"), cb)
	if calls != 2 {
		t.Errorf("onSaturated fired %d times total, want 2 (Clear re-arms the guard)", calls)
	}
}
