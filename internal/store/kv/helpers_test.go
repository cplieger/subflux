package boltkv

import (
	"path/filepath"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// openTestDB opens a fresh bbolt database in a temporary directory and
// registers cleanup. It fails the test on error.
func openTestDB(t *testing.T) *bolt.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "test.bolt")
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: time.Second})
	if err != nil {
		t.Fatalf("open bbolt: %v", err)
	}
	t.Cleanup(func() {
		if cerr := db.Close(); cerr != nil {
			t.Errorf("close bbolt: %v", cerr)
		}
	})
	return db
}

// mustCreateBuckets creates the named buckets in one Update transaction.
func mustCreateBuckets(t *testing.T, db *bolt.DB, names ...string) {
	t.Helper()
	err := db.Update(func(tx *bolt.Tx) error {
		for _, n := range names {
			if _, berr := tx.CreateBucketIfNotExists([]byte(n)); berr != nil {
				return berr
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("create buckets: %v", err)
	}
}
