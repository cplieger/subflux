package kv

import (
	"bytes"
	"testing"

	bolt "go.etcd.io/bbolt"
)

func TestNextID_monotonicAndBe64Encoded(t *testing.T) {
	db := openTestDB(t)
	mustCreateBuckets(t, db, "items")

	const n = 50
	ids := make([]uint64, 0, n)
	keys := make([][]byte, 0, n)

	err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte("items"))
		for range n {
			id, key, ierr := NextID(b)
			if ierr != nil {
				return ierr
			}
			ids = append(ids, id)
			keys = append(keys, key)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Ids are strictly increasing and start at 1 (bbolt's first sequence).
	if ids[0] != 1 {
		t.Errorf("first id = %d, want 1", ids[0])
	}
	for i := range len(ids) - 1 {
		if ids[i+1] != ids[i]+1 {
			t.Errorf("ids not contiguous-monotonic at %d: %d then %d", i, ids[i], ids[i+1])
		}
		// The be64 key encoding sorts in the same (insertion) order.
		if bytes.Compare(keys[i], keys[i+1]) >= 0 {
			t.Errorf("be64 keys not in increasing order at %d: %x then %x", i, keys[i], keys[i+1])
		}
	}

	// The returned key encodes the returned id.
	for i, id := range ids {
		if got, ok := DecodeBe64(keys[i]); !ok || got != id {
			t.Errorf("key %x decodes to (%d,%v), want %d", keys[i], got, ok, id)
		}
	}

	// Sequence persists monotonically across a second transaction.
	var next uint64
	err = db.Update(func(tx *bolt.Tx) error {
		id, _, ierr := NextID(tx.Bucket([]byte("items")))
		next = id
		return ierr
	})
	if err != nil {
		t.Fatalf("second Update: %v", err)
	}
	if next != ids[len(ids)-1]+1 {
		t.Errorf("sequence did not continue across tx: got %d, want %d", next, ids[len(ids)-1]+1)
	}
}
