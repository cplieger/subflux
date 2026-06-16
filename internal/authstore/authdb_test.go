package authstore

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/boltstore"
	bolt "go.etcd.io/bbolt"
)

// bootstrappedFile returns the path to a bbolt file that has been opened (and
// thus fully bootstrapped — every core AND auth bucket created) by the core
// store, then closed so the OS file lock is released. The auth store shares
// this exact on-disk schema; reopening it as a raw handle gives a *bbolt.DB
// whose auth buckets already exist, which is the production wiring (boltstore
// owns bucket creation, authstore.New only shares the handle).
func bootstrappedFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	core, err := boltstore.Open(path)
	if err != nil {
		t.Fatalf("boltstore.Open(%q): %v", path, err)
	}
	if err := core.Close(context.Background()); err != nil {
		t.Fatalf("boltstore.Close: %v", err)
	}
	return path
}

// openShared opens path as a raw *bbolt.DB — the handle the core store would
// own and the auth store would share — and registers its close. The auth store
// must NEVER close this handle (the core store owns its lifecycle), so the test
// owns the close here.
func openShared(t *testing.T, path string) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("bolt.Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// TestNew_overBoltstoreDB_exposesEmptyAuthBuckets asserts that a Store
// constructed over a freshly boltstore-opened file sees every auth bucket the
// foundation declares, and that they are empty on first boot. This also
// cross-checks that authstore's bucket-name constants match the names the core
// store (the schema owner) actually created.
func TestNew_overBoltstoreDB_exposesEmptyAuthBuckets(t *testing.T) {
	db := openShared(t, bootstrappedFile(t))
	s := New(db)
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	err := s.view(func(tx *bolt.Tx) error {
		for _, name := range authBuckets {
			b, ok := authBucket(tx, name)
			if !ok {
				t.Errorf("auth bucket %q missing in a boltstore-opened file", name)
				continue
			}
			if k, _ := b.Cursor().First(); k != nil {
				t.Errorf("auth bucket %q is not empty on first boot (first key %x)", name, k)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("view: %v", err)
	}
}

// TestClose_leavesSharedHandleUsable asserts the auth store's Close stops at the
// sweeper and never closes the shared *bbolt.DB handle: after Close the handle
// is still usable for both reads and writes (the core store keeps operating).
func TestClose_leavesSharedHandleUsable(t *testing.T) {
	db := openShared(t, bootstrappedFile(t))
	s := New(db)
	if err := s.Open(); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// A write transaction on the shared handle must still succeed after the auth
	// store closed — i.e. the handle was not closed underneath the core store.
	if err := db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketAuthUsers))
		if b == nil {
			t.Fatal("auth_users bucket vanished after authstore Close")
		}
		return b.Put([]byte("probe"), []byte("ok"))
	}); err != nil {
		t.Fatalf("shared handle unusable after authstore Close: %v", err)
	}
	if err := db.View(func(tx *bolt.Tx) error {
		if got := tx.Bucket([]byte(bucketAuthUsers)).Get([]byte("probe")); string(got) != "ok" {
			t.Errorf("probe read = %q, want %q", got, "ok")
		}
		return nil
	}); err != nil {
		t.Fatalf("view after authstore Close: %v", err)
	}
}

// TestDecodeAuthRecord_corruptFailsClosed asserts that a deliberately corrupt
// auth record surfaces a descriptive fail-closed error through the foundation
// decode helper, rather than being silently skipped the way a derived core
// bucket would be (Requirement 13.4).
func TestDecodeAuthRecord_corruptFailsClosed(t *testing.T) {
	db := openShared(t, bootstrappedFile(t))

	key := []byte("corrupt")
	if err := db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketAuthUsers)).Put(key, []byte("{not valid json"))
	}); err != nil {
		t.Fatalf("seed corrupt record: %v", err)
	}

	// A minimal record type stands in for the real userRec (task 8.2); the
	// helper is generic over the value type.
	type rec struct {
		ID int64 `json:"id"`
	}
	err := db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte(bucketAuthUsers)).Get(key)
		var v rec
		return decodeAuthRecord(bucketAuthUsers, key, data, &v)
	})
	if err == nil {
		t.Fatal("decodeAuthRecord on corrupt data: err = nil, want a fail-closed error")
	}
	if !strings.Contains(err.Error(), bucketAuthUsers) {
		t.Errorf("error %q does not name the source bucket %q", err, bucketAuthUsers)
	}
	if !strings.Contains(err.Error(), "decode") {
		t.Errorf("error %q is not a descriptive decode error", err)
	}
}

// TestUniqueCheck covers the uniqueness chokepoint: a present index key
// conflicts, an absent one is allowed, and an absent index bucket is treated as
// empty (no conflict) per the empty-first-boot rule.
func TestUniqueCheck(t *testing.T) {
	db := openShared(t, bootstrappedFile(t))
	s := New(db)

	if err := s.update(func(tx *bolt.Tx) error {
		// Seed an existing username index entry.
		if err := tx.Bucket([]byte(bucketIxUserName)).Put([]byte("alice"), []byte("1")); err != nil {
			return err
		}
		if err := uniqueCheck(tx, bucketIxUserName, []byte("alice")); !errors.Is(err, errConflict) {
			t.Errorf("uniqueCheck(existing) = %v, want errConflict", err)
		}
		if err := uniqueCheck(tx, bucketIxUserName, []byte("bob")); err != nil {
			t.Errorf("uniqueCheck(absent key) = %v, want nil", err)
		}
		if err := uniqueCheck(tx, "ix_does_not_exist", []byte("alice")); err != nil {
			t.Errorf("uniqueCheck(absent bucket) = %v, want nil (empty first boot)", err)
		}
		return nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// TestNextAuthID_monotonic asserts the surrogate-id allocator returns positive,
// strictly increasing ids inside a write transaction.
func TestNextAuthID_monotonic(t *testing.T) {
	db := openShared(t, bootstrappedFile(t))
	s := New(db)

	if err := s.update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketAuthUsers))
		id1, err := nextAuthID(b)
		if err != nil {
			return err
		}
		id2, err := nextAuthID(b)
		if err != nil {
			return err
		}
		if id1 < 1 {
			t.Errorf("first id = %d, want >= 1", id1)
		}
		if id2 != id1+1 {
			t.Errorf("second id = %d, want %d (monotonic)", id2, id1+1)
		}
		return nil
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
}

// TestAuthBucket_absentTreatedAsEmpty asserts the defensive accessor reports a
// missing bucket as (nil, false) rather than panicking, so reads can treat an
// absent bucket as an empty first boot.
func TestAuthBucket_absentTreatedAsEmpty(t *testing.T) {
	db := openShared(t, bootstrappedFile(t))

	if err := db.View(func(tx *bolt.Tx) error {
		if b, ok := authBucket(tx, "ix_does_not_exist"); ok || b != nil {
			t.Errorf("authBucket(absent) = (%v, %v), want (nil, false)", b, ok)
		}
		if _, ok := authBucket(tx, bucketAuthUsers); !ok {
			t.Errorf("authBucket(%q) ok = false, want true", bucketAuthUsers)
		}
		return nil
	}); err != nil {
		t.Fatalf("view: %v", err)
	}
}
