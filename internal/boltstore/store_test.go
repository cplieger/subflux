package boltstore

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"
)

// allExpectedBuckets is every primary and index bucket Open must bootstrap:
// the core buckets plus the auth buckets the core store owns as the single
// bucket-schema owner.
func allExpectedBuckets() [][]byte {
	out := make([][]byte, 0, len(coreBuckets)+len(authBuckets))
	out = append(out, coreBuckets...)
	out = append(out, authBuckets...)
	return out
}

// openTemp opens a fresh store under a per-test temp dir and registers Close.
// StrictMode makes bbolt run a full consistency check after every commit —
// far too slow for production but exactly right for tests, turning any page
// or freelist corruption into an immediate panic at the offending commit.
func openTemp(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q) error: %v", path, err)
	}
	db.db.StrictMode = true
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db, path
}

// TestOpen_fileMode asserts Open creates the file with owner-only 0600
// permissions. POSIX file-mode bits are meaningless on Windows (which is the
// dev workstation), so the permission assertion is skipped there; the test
// still proves the file is created cross-platform. The mode assertion is the
// one that runs on Linux/CI.
func TestOpen_fileMode(t *testing.T) {
	_, path := openTemp(t)

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %q: %v", path, err)
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file-mode bits not meaningful on Windows; mode check runs on Linux/CI")
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("file mode = %04o, want 0600", got)
	}
}

// TestOpen_bootstrapsAllBuckets asserts every primary and index bucket exists
// after Open, and that the schema-version meta keys are written to the current
// values.
func TestOpen_bootstrapsAllBuckets(t *testing.T) {
	db, _ := openTemp(t)

	err := db.db.View(func(tx *bolt.Tx) error {
		for _, name := range allExpectedBuckets() {
			if tx.Bucket(name) == nil {
				t.Errorf("bucket %q missing after Open", name)
			}
		}
		core, corePresent := readSchemaVersion(tx, metaKeyCoreSchemaVersion)
		if !corePresent || core != coreSchemaVersion {
			t.Errorf("core schema version = (%d, present=%v), want (%d, true)", core, corePresent, coreSchemaVersion)
		}
		authV, authPresent := readSchemaVersion(tx, metaKeyAuthSchemaVersion)
		if !authPresent || authV != authSchemaVersion {
			t.Errorf("auth schema version = (%d, present=%v), want (%d, true)", authV, authPresent, authSchemaVersion)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View error: %v", err)
	}
}

// TestOpen_reopenIdempotent asserts a close-then-reopen of the same file
// succeeds: CreateBucketIfNotExists is idempotent and the unconditional schema
// write stays at the current version, so the buckets all still exist.
func TestOpen_reopenIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")

	db1, err := Open(path)
	if err != nil {
		t.Fatalf("first Open error: %v", err)
	}
	if err := db1.Close(context.Background()); err != nil {
		t.Fatalf("first Close error: %v", err)
	}

	db2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen error: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(context.Background()) })

	err = db2.db.View(func(tx *bolt.Tx) error {
		for _, name := range allExpectedBuckets() {
			if tx.Bucket(name) == nil {
				t.Errorf("bucket %q missing after reopen", name)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("View error: %v", err)
	}
}

// TestOpen_heldLockFailsFast asserts a second concurrent Open of the same file
// fails fast (within roughly openTimeout) rather than blocking indefinitely,
// because bbolt takes an exclusive OS lock (Requirement 13.2). The assertion is
// robust: it requires an error AND that the call returned in well under twice
// the timeout, without depending on the exact error text.
func TestOpen_heldLockFailsFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open error: %v", err)
	}
	t.Cleanup(func() { _ = first.Close(context.Background()) })

	start := time.Now()
	second, err := Open(path)
	elapsed := time.Since(start)

	if err == nil {
		_ = second.Close(context.Background())
		t.Fatal("second Open of a held file: error = nil, want a fail-fast lock error")
	}
	// Generous upper bound: the timeout is openTimeout; allow slack for slow CI
	// but still well below an "indefinite hang".
	if elapsed > openTimeout+5*time.Second {
		t.Errorf("second Open took %v, want fail-fast within ~%v", elapsed, openTimeout)
	}
}

// TestOpen_emptyPath asserts the empty-path guard rejects before touching the
// filesystem.
func TestOpen_emptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Error("Open(\"\"): error = nil, want a path-required error")
	}
}

// TestClose_safeOnZeroValue asserts Close tolerates a nil/zero DB so a failed
// construction path can defer Close unconditionally.
func TestClose_safeOnZeroValue(t *testing.T) {
	var d *DB
	if err := d.Close(context.Background()); err != nil {
		t.Errorf("Close on nil *DB: %v, want nil", err)
	}
	if err := (&DB{}).Close(context.Background()); err != nil {
		t.Errorf("Close on zero DB: %v, want nil", err)
	}
}
