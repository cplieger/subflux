// Package boltstore is the bbolt-backed core store for subflux's search,
// subtitle, scan, sync-offset, and poll domains. It fully implements the
// composite api.Store interface.
//
// # Naming
//
// The package lives at internal/boltstore rather than internal/store for a
// historical reason: during the SQLite-to-bbolt rewrite both engines existed
// side by side (the differential-parity oracle imported both), so the new
// engine could not take the internal/store path. The SQLite engine is gone;
// internal/store now holds only the engine-agnostic leaves this package
// builds on (store/kv: codec, key encoders, index helpers; store/storetest:
// the api.Store contract suite). boltstore is the permanent home of the
// engine.
//
// # Ownership
//
// The core store OWNS the shared *bbolt.DB handle: Open opens it and Close
// closes it. The auth store (internal/authstore) shares the same handle via
// authstore.New and never closes it.
package boltstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"go.etcd.io/bbolt"
)

// Compile-time assertion: *DB implements the composite api.Store interface.
var _ api.Store = (*DB)(nil)

// statFunc checks a path's existence for ReconcileState's filesystem oracle. It
// mirrors the legacy SQLite store's injectable stat function: production uses
// os.Stat, and tests substitute a fake so reconciliation can be exercised
// without touching the real filesystem. ReconcileState only inspects whether
// the returned error is os.ErrNotExist (file gone) versus any other error
// (treated as present/skip), matching the old reconcile classifier.
type statFunc func(path string) (os.FileInfo, error)

// openTimeout bounds how long Open waits for the bbolt file lock before failing
// fast. bbolt takes an exclusive OS lock on the file, so a second opener (for
// example a stale or duplicate process) must surface quickly at startup rather
// than block indefinitely.
const openTimeout = 5 * time.Second

// initialMmapSize is the initial mmap span (not allocated disk) requested at
// Open. bbolt grows the file by remapping, and on non-Linux platforms (no
// mremap) a grow while a hot-backup WriteTo read transaction is open must wait
// for it to finish. Pre-mapping 256 MiB — far above the expected tens-of-MB
// working set — makes grow-remaps a non-event for the deployment's lifetime.
// Virtual address space is free; resident memory is still driven by actual
// page access.
const initialMmapSize = 256 << 20

// DB is the bbolt-backed core store. It owns the *bbolt.DB handle that the auth
// store shares.
type DB struct {
	db *bbolt.DB

	// statFn is the filesystem-existence oracle ReconcileState uses to decide
	// whether each row's video and subtitle files still exist. It defaults to
	// os.Stat in Open; tests override it to drive reconciliation deterministically
	// without real files.
	statFn statFunc
}

// Open opens (creating if necessary) the bbolt database at path with owner-only
// (0o600) permissions and a bounded file-lock timeout. The returned *DB owns
// the handle; the caller must Close it to release the file lock.
//
// A held file lock (a second opener of the same file) fails fast within
// openTimeout rather than blocking indefinitely (Requirement 13.2).
//
// In one Update transaction Open then (a) verifies the on-disk schema versions
// are not from a future, breaking build — refusing to open if so, since no
// downgrade migration exists — and (b) bootstraps EVERY core and auth bucket.
// The core store is the single bucket-schema owner: it creates every primary
// and index bucket from coreBuckets+authBuckets (the auth-domain key builders
// live in internal/authstore, but the buckets are owned here), so the auth
// store only ever shares the already-bootstrapped handle. Finally it writes the
// current core and auth schema versions into meta.
//
// The schema versions are written UNCONDITIONALLY to the current value, not
// only-when-absent. verifySchemaVersions has already refused any stored version
// that mismatches this build (no migration exists in either direction), so the
// only values that reach the write are absent (fresh file) or equal (re-open).
// Setting them to the current value in every case is correct and avoids a
// read-modify-write branch: after a successful Open the file is, by
// definition, a current-build file.
//
// If the bootstrap Update fails, Open closes the handle before returning so a
// failed Open never leaks the file lock.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("boltstore: open: path must not be empty")
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{
		Timeout: openTimeout,
		// Freelist as hashmap instead of sorted array: O(1) allocation and no
		// O(n) sorted-insert on free. etcd runs this in production and bbolt's
		// maintainers list defaulting it as a TODO; the array type's only edge
		// (slightly better contiguous-page reuse) doesn't matter at this scale.
		FreelistType: bbolt.FreelistMapType,
		// Don't write the freelist on every commit; rebuild it on Open instead.
		// etcd's default. Commits get cheaper; the crash-recovery cost is a
		// full-file scan at open, which is instant on a tens-of-MB file.
		NoFreelistSync:  true,
		InitialMmapSize: initialMmapSize,
	})
	if err != nil {
		return nil, fmt.Errorf("boltstore: open %q: %w", path, err)
	}
	if err := bootstrap(db); err != nil {
		// Close the handle so a failed Open does not leak the OS file lock.
		_ = db.Close()
		return nil, fmt.Errorf("boltstore: open %q: %w", path, err)
	}
	return &DB{db: db, statFn: os.Stat}, nil
}

// bootstrap runs the one-time-per-open schema check and bucket creation inside a
// single Update. It is split out of Open so the transaction body stays
// readable. verifySchemaVersions runs FIRST (detect-and-refuse a future schema)
// before any bucket is created, so refusing a future file leaves it untouched.
func bootstrap(db *bbolt.DB) error {
	return db.Update(func(tx *bbolt.Tx) error {
		if err := verifySchemaVersions(tx); err != nil {
			return err
		}
		for _, name := range coreBuckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("bootstrap core bucket %q: %w", name, err)
			}
		}
		for _, name := range authBuckets {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return fmt.Errorf("bootstrap auth bucket %q: %w", name, err)
			}
		}
		if err := writeSchemaVersion(tx, metaKeyCoreSchemaVersion, coreSchemaVersion); err != nil {
			return err
		}
		return writeSchemaVersion(tx, metaKeyAuthSchemaVersion, authSchemaVersion)
	})
}

// BoltDB exposes the underlying *bbolt.DB handle so the auth store
// (internal/authstore) can share the same file. The auth store NEVER closes
// this handle — the core store owns it exclusively.
func (d *DB) BoltDB() *bbolt.DB {
	return d.db
}

// Close closes the underlying bbolt handle. The core store owns the handle, so
// this is the single place it is closed; the auth store's Close never touches
// it. The context satisfies the api.Store contract (it bounds caller shutdown
// time, e.g. a SIGTERM grace period); bbolt's own Close takes no context.
// Close is safe to call on a zero DB.
func (d *DB) Close(_ context.Context) error {
	if d == nil || d.db == nil {
		return nil
	}
	if err := d.db.Close(); err != nil {
		return fmt.Errorf("boltstore: close: %w", err)
	}
	return nil
}
