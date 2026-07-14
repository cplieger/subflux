package boltstore

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/storetest"
	bolt "go.etcd.io/bbolt"
)

// TestBoltStoreContract runs the engine-agnostic api.Store behavioral contract
// suite (internal/store/storetest) against the bbolt *DB, proving the new
// engine satisfies every promoted finding — the three ReconcileState branches,
// non-destructive ClearManualLock, backoff cleared on save, DeleteStateByPaths
// orphan cleanup, GetState filter/search/limit/offset, and both CleanupDrift
// branches (Requirements 14.1, 14.2). The same suite runs against the legacy
// SQLite store.DB (internal/store) for parity.
func TestBoltStoreContract(t *testing.T) {
	t.Parallel()
	storetest.Suite(t, func(t *testing.T) api.Store {
		path := filepath.Join(t.TempDir(), "subflux.bolt")
		db, err := Open(path)
		if err != nil {
			t.Fatalf("Open(%q): %v", path, err)
		}
		db.db.StrictMode = true // consistency check every commit (test-only)
		t.Cleanup(func() { _ = db.Close(context.Background()) })
		return db
	})
}

// destructiveClearStore is a deliberately broken api.Store: it embeds a real,
// fully-functional *DB but overrides ClearManualLock to DELETE the quad's
// manual rows instead of flipping them to auto. That violates the promoted
// non-destructive-ClearManualLock invariant (the rows must be preserved and
// stay visible to GetState/DownloadedRefs, Requirement 4.3). It exists only to
// prove the contract suite's assertions actually catch a regression.
type destructiveClearStore struct {
	*DB
}

// ClearManualLock destructively deletes the manual rows for the quad. A
// conforming implementation flips manual=false and preserves the rows; this
// removes them, so AssertClearManualLockNonDestructive must report a failure.
func (s destructiveClearStore) ClearManualLock(_ context.Context, mt api.MediaType, mid, lang string, variant api.Variant) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		rows, err := collectStateRows(tx, mt, mid, lang, variant)
		if err != nil {
			return err
		}
		for i := range rows {
			r := &rows[i]
			if !r.Manual {
				continue
			}
			if _, derr := deleteState(tx, r.ID); derr != nil {
				return derr
			}
		}
		return nil
	})
}

// recorderTB captures whether a storetest assertion reported a failure, so a
// broken store can be driven through a promoted-invariant check WITHOUT failing
// the parent test. It satisfies storetest.TB. Fatalf panics with a sentinel so
// it halts the check like *testing.T's FailNow; runRecorded recovers it.
type recorderTB struct {
	failed bool
}

type recorderFatal struct{}

func (r *recorderTB) Helper() {}

func (r *recorderTB) Errorf(string, ...any) { r.failed = true }

func (r *recorderTB) Fatalf(string, ...any) {
	r.failed = true
	panic(recorderFatal{})
}

// runRecorded runs fn against the recorder, recovering the Fatalf sentinel so a
// fatal assertion is captured as a failure rather than crashing the test.
func runRecorded(rec *recorderTB, fn func(t storetest.TB)) {
	defer func() {
		if r := recover(); r != nil {
			if _, ok := r.(recorderFatal); !ok {
				panic(r) // a real panic (e.g. nil deref): surface it
			}
		}
	}()
	fn(rec)
}

// TestStoretestSuite_catchesDestructiveClearManualLock proves the strengthened
// contract suite is not vacuous: a store that violates the non-destructive
// ClearManualLock invariant is caught by the suite's assertion. It runs the
// promoted invariant (AssertClearManualLockNonDestructive) against the broken
// stub via a failure recorder and asserts a failure WAS reported. This is the
// "the suite fails against a deliberately broken stub" gate (Requirement 14.2),
// kept self-contained so it does not break this package's own go test.
func TestStoretestSuite_catchesDestructiveClearManualLock(t *testing.T) {
	t.Parallel()

	// Sanity: the real *DB passes the invariant.
	good, _ := openTemp(t)
	okRec := &recorderTB{}
	runRecorded(okRec, func(tb storetest.TB) {
		storetest.AssertClearManualLockNonDestructive(tb, good)
	})
	if okRec.failed {
		t.Fatal("AssertClearManualLockNonDestructive reported a failure against the conforming *DB; the check is over-strict")
	}

	// The broken (destructive) store must be caught.
	broken, _ := openTemp(t)
	badRec := &recorderTB{}
	runRecorded(badRec, func(tb storetest.TB) {
		storetest.AssertClearManualLockNonDestructive(tb, destructiveClearStore{broken})
	})
	if !badRec.failed {
		t.Fatal("contract suite did NOT catch a destructive ClearManualLock; the suite's assertion is vacuous")
	}
}
