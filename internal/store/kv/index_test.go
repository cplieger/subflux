package kv

import (
	"testing"

	bolt "go.etcd.io/bbolt"
)

// idxRec is a minimal primary record for exercising index maintenance. It is
// indexed by Tag, with the score N projected into the index value.
type idxRec struct {
	Tag string `json:"tag"`
	N   int    `json:"n"`
}

const (
	bktPrimary = "items"
	bktIndex   = "ix_tag"
	bktMeta    = "meta"
)

var counterKey = []byte("items_count")

// tagIndex builds the IndexSpec under test: key = tag 0x00 primaryKey, value =
// be64(N) projection.
func tagIndex() []IndexSpec[idxRec] {
	return []IndexSpec[idxRec]{{
		Bucket: bktIndex,
		Key: func(pk []byte, v *idxRec) []byte {
			return TimeIndexKeyless(v.Tag, pk)
		},
		Value: func(_ []byte, v *idxRec) []byte {
			return Be64(uint64(v.N))
		},
	}}
}

// TimeIndexKeyless joins a text component and a binary primary with a NUL
// separator (a test-local helper mirroring how a real store derives a
// text-prefixed index key). Defined in the test to keep the production surface
// minimal.
func TimeIndexKeyless(text string, primary []byte) []byte {
	buf := make([]byte, 0, len(text)+1+len(primary))
	buf = append(buf, text...)
	buf = append(buf, Sep)
	buf = append(buf, primary...)
	return buf
}

func counters() []CounterSpec {
	return []CounterSpec{{Bucket: bktMeta, Key: counterKey}}
}

// indexEntries returns the index bucket as a map of hex(key) -> decoded N.
func indexEntries(t *testing.T, db *bolt.DB) map[string]uint64 {
	t.Helper()
	out := map[string]uint64{}
	err := db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bktIndex)).ForEach(func(k, v []byte) error {
			n, _ := DecodeBe64(v)
			out[string(k)] = n
			return nil
		})
	})
	if err != nil {
		t.Fatalf("scan index: %v", err)
	}
	return out
}

func readCount(t *testing.T, db *bolt.DB) int64 {
	t.Helper()
	var c int64
	err := db.View(func(tx *bolt.Tx) error {
		c = ReadCounter(tx.Bucket([]byte(bktMeta)), counterKey)
		return nil
	})
	if err != nil {
		t.Fatalf("read counter: %v", err)
	}
	return c
}

func put(t *testing.T, db *bolt.DB, key string, rec idxRec) {
	t.Helper()
	err := db.Update(func(tx *bolt.Tx) error {
		return PutIndexed(tx, bktPrimary, []byte(key), &rec, tagIndex(), counters())
	})
	if err != nil {
		t.Fatalf("PutIndexed(%s): %v", key, err)
	}
}

func del(t *testing.T, db *bolt.DB, key string) bool {
	t.Helper()
	var existed bool
	err := db.Update(func(tx *bolt.Tx) error {
		var derr error
		existed, derr = DeleteIndexed(tx, bktPrimary, []byte(key), tagIndex(), counters())
		return derr
	})
	if err != nil {
		t.Fatalf("DeleteIndexed(%s): %v", key, err)
	}
	return existed
}

// TestPutDeleteIndexed_maintainsIndexAndCounter drives a sequence of inserts,
// updates, and deletes and asserts the index bucket and the meta counter stay
// exactly consistent with the primary at every step.
//
// FLAT on purpose, and it was briefly not. The steps are a state machine over
// ONE database, so wrapping each in t.Run made them look independent while
// sharing state: `go test -run '.../deleting_k1_leaves_only_k2_indexed'` then
// failed because its predecessors never ran, and the final step PASSED
// VACUOUSLY in isolation because an empty store already reads counter 0 with 0
// entries. Named subtests are for scenarios that build their own fixture; the
// fix for a multi-assert cluster like this one is t.Errorf per fact, which is
// what it uses. Each step names itself in its own failure messages, so a
// failure still says which transition broke.
//
// The t.Fatalf sites are deliberate: each guards a map index or a length the
// next assertion reads, so continuing past one reports on a list that is
// already wrong.
func TestPutDeleteIndexed_maintainsIndexAndCounter(t *testing.T) {
	db := openTestDB(t)
	mustCreateBuckets(t, db, bktPrimary, bktIndex, bktMeta)

	// Step 1: insert k1 indexes it and counts it.
	put(t, db, "k1", idxRec{Tag: "a", N: 1})
	if c := readCount(t, db); c != 1 {
		t.Errorf("insert k1: counter = %d, want 1", c)
	}
	entries := indexEntries(t, db)
	if len(entries) != 1 {
		t.Fatalf("insert k1: %d index entries, want 1", len(entries))
	}
	if got := entries[string(TimeIndexKeyless("a", []byte("k1")))]; got != 1 {
		t.Errorf("insert k1: index projection = %d, want 1", got)
	}

	// Step 2: re-tagging k1 replaces its index entry and leaves the count.
	put(t, db, "k1", idxRec{Tag: "b", N: 2})
	if c := readCount(t, db); c != 1 {
		t.Errorf("re-tag k1: counter = %d, want 1 (an update is not an insert)", c)
	}
	entries = indexEntries(t, db)
	if len(entries) != 1 {
		t.Fatalf("re-tag k1: %d index entries, want 1", len(entries))
	}
	if _, stale := entries[string(TimeIndexKeyless("a", []byte("k1")))]; stale {
		t.Error("re-tag k1: stale 'a' index entry was not deleted on update")
	}
	if got := entries[string(TimeIndexKeyless("b", []byte("k1")))]; got != 2 {
		t.Errorf("re-tag k1: updated index projection = %d, want 2", got)
	}

	// Step 3: a second row sharing a tag gets its own entry.
	put(t, db, "k2", idxRec{Tag: "b", N: 3})
	if c := readCount(t, db); c != 2 {
		t.Errorf("insert k2: counter = %d, want 2", c)
	}
	if entries := indexEntries(t, db); len(entries) != 2 {
		t.Errorf("insert k2: %d index entries, want 2", len(entries))
	}

	// Step 4: deleting k1 leaves only k2 indexed.
	if !del(t, db, "k1") {
		t.Error("delete k1: DeleteIndexed reported existed = false, want true")
	}
	if c := readCount(t, db); c != 1 {
		t.Errorf("delete k1: counter = %d, want 1", c)
	}
	entries = indexEntries(t, db)
	if len(entries) != 1 {
		t.Fatalf("delete k1: %d index entries, want 1", len(entries))
	}
	if got := entries[string(TimeIndexKeyless("b", []byte("k2")))]; got != 3 {
		t.Errorf("delete k1: remaining index projection for k2 = %d, want 3", got)
	}

	// Step 5: deleting a missing key changes nothing.
	if del(t, db, "missing") {
		t.Error("delete missing: DeleteIndexed reported existed = true, want false")
	}
	if c := readCount(t, db); c != 1 {
		t.Errorf("delete missing: counter = %d, want 1 (unchanged)", c)
	}

	// Step 6: emptying the store clamps the counter at zero. The second delete
	// must not underflow it.
	del(t, db, "k2")
	del(t, db, "k2")
	if c := readCount(t, db); c != 0 {
		t.Errorf("empty store: counter = %d, want 0", c)
	}
	if entries := indexEntries(t, db); len(entries) != 0 {
		t.Errorf("empty store: %d index entries, want 0", len(entries))
	}
}

// TestPutIndexed_failsClosedOnAMissingCounterBucket asserts a declared counter
// whose bucket does not exist is an error rather than a silently skipped count:
// every reader trusts the counter to match the primary, so a row that lands
// uncounted is drift that no later write repairs.
func TestPutIndexed_failsClosedOnAMissingCounterBucket(t *testing.T) {
	db := openTestDB(t)
	// The counter's meta bucket is deliberately absent.
	mustCreateBuckets(t, db, bktPrimary, bktIndex)

	rec := idxRec{Tag: "a", N: 1}
	err := db.Update(func(tx *bolt.Tx) error {
		return PutIndexed(tx, bktPrimary, []byte("k1"), &rec, tagIndex(), counters())
	})
	if err == nil {
		t.Fatal("PutIndexed with a missing counter bucket = nil, want an error")
	}

	// The refused transaction leaves no row behind.
	var stored []byte
	if verr := db.View(func(tx *bolt.Tx) error {
		stored = tx.Bucket([]byte(bktPrimary)).Get([]byte("k1"))
		return nil
	}); verr != nil {
		t.Fatalf("read primary: %v", verr)
	}
	if stored != nil {
		t.Errorf("primary row k1 = %q after a refused insert, want it absent", stored)
	}
}

// TestPutIndexed_indexEqualsPrimaryRescan asserts the index is a faithful
// derivation of the primary: every primary row has exactly one matching index
// entry and there are no orphans, after a mixed operation sequence.
func TestPutIndexed_indexEqualsPrimaryRescan(t *testing.T) {
	db := openTestDB(t)
	mustCreateBuckets(t, db, bktPrimary, bktIndex, bktMeta)

	put(t, db, "k1", idxRec{Tag: "x", N: 10})
	put(t, db, "k2", idxRec{Tag: "y", N: 20})
	put(t, db, "k3", idxRec{Tag: "x", N: 30})
	put(t, db, "k2", idxRec{Tag: "z", N: 21}) // re-tag
	del(t, db, "k1")

	err := db.View(func(tx *bolt.Tx) error {
		pb := tx.Bucket([]byte(bktPrimary))
		ib := tx.Bucket([]byte(bktIndex))

		// Derive the expected index from a full primary scan.
		want := map[string]uint64{}
		var rows int64
		if ferr := pb.ForEach(func(k, v []byte) error {
			var r idxRec
			if derr := Decode(v, &r); derr != nil {
				return derr
			}
			rows++
			want[string(TimeIndexKeyless(r.Tag, k))] = uint64(r.N)
			return nil
		}); ferr != nil {
			return ferr
		}

		// Compare against the actual index bucket.
		got := map[string]uint64{}
		if ferr := ib.ForEach(func(k, v []byte) error {
			n, _ := DecodeBe64(v)
			got[string(k)] = n
			return nil
		}); ferr != nil {
			return ferr
		}

		if len(got) != len(want) {
			t.Errorf("index entry count = %d, want %d (= primary rows)", len(got), len(want))
		}
		for k, wn := range want {
			gn, ok := got[k]
			if !ok {
				t.Errorf("missing index entry for primary-derived key %x", k)
				continue
			}
			if gn != wn {
				t.Errorf("index projection for %x = %d, want %d", k, gn, wn)
			}
		}
		for k := range got {
			if _, ok := want[k]; !ok {
				t.Errorf("orphan index entry %x has no matching primary row", k)
			}
		}

		// And the maintained counter equals the primary row count.
		if c := ReadCounter(tx.Bucket([]byte(bktMeta)), counterKey); c != rows {
			t.Errorf("counter = %d, want %d (= primary rows)", c, rows)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	// Sanity: confirm the "x"/k1 entry really is absent after the delete.
	if _, present := indexEntries(t, db)[string(TimeIndexKeyless("x", []byte("k1")))]; present {
		t.Error("deleted k1 still has an index entry")
	}
}
