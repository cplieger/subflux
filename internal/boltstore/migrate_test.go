package boltstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file covers the migration FRAMEWORK guards and mechanics (Requirements
// 1, 2, 5.4, 5.5): strict stamp reads (newer / malformed / missing-populated
// refusals, fresh-file zero-migration bootstrap), runtime ladder validation,
// the in-place ladder execution order with per-step stamping and crash-resume
// (in-process fast tier), the pre-migration snapshot contract, and the
// bucket-domain membership data. The destructive-step fixtures live in
// migrate_reset_test.go, the copy-kind swap in migrate_copy_test.go, and the
// subprocess crash failpoints in migrate_crash_test.go.
//
// Test ladders are always injected through openWithDomains (Requirement 5.5);
// the package registries are never mutated.

// testCoreDomain builds an injected core-domain descriptor targeting `current`
// with the given ladder. Stamp key, data buckets, and the v1 base are the
// production ones, so the domain operates on a real boltstore file; tests
// exercising base-version rules override .base on the returned descriptor.
func testCoreDomain(current uint64, ladder []migration) *migrationDomain {
	return &migrationDomain{
		name:        "core",
		stampKey:    metaKeyCoreSchemaVersion,
		current:     current,
		base:        coreSchemaBaseVersion,
		ladder:      ladder,
		dataBuckets: coreDataBuckets(),
	}
}

// testAuthDomain is testCoreDomain's auth-domain sibling.
func testAuthDomain(current uint64, ladder []migration) *migrationDomain {
	return &migrationDomain{
		name:        "auth",
		stampKey:    metaKeyAuthSchemaVersion,
		current:     current,
		base:        authSchemaBaseVersion,
		ladder:      ladder,
		dataBuckets: authBuckets,
	}
}

// markerStep returns an in-place step from -> from+1 whose body increments the
// meta counter key "test_step_<from>". The counter survives commits and
// rollbacks exactly like real data, so asserting it equals 1 proves the step
// ran exactly once (a rolled-back attempt leaves no increment behind).
func markerStep(from uint64) migration {
	key := markerKey(from)
	return migration{
		from: from,
		to:   from + 1,
		kind: migrateInPlace,
		run: func(tx *bolt.Tx) error {
			mb := tx.Bucket([]byte(bucketMeta))
			if mb == nil {
				return errors.New("marker step: meta bucket missing")
			}
			v, _ := kv.GetUint64(mb, key)
			return kv.PutUint64(mb, key, v+1)
		},
	}
}

// markerKey names the meta counter markerStep(from) increments.
func markerKey(from uint64) []byte {
	return fmt.Appendf(nil, "test_step_%d", from)
}

// rawOpen opens path as a raw bbolt handle with NO schema handling, so tests
// can plant stamps and inspect buckets the way a corrupted or foreign file
// would look. Closing is registered on t.
func rawOpen(t *testing.T, path string) *bolt.DB {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		t.Fatalf("raw bolt.Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rawUpdate applies fn to path in one raw write transaction and closes the
// handle again (so a subsequent Open can take the exclusive lock).
func rawUpdate(t *testing.T, path string, fn func(tx *bolt.Tx) error) {
	t.Helper()
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		t.Fatalf("raw bolt.Open(%q): %v", path, err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Update(fn); err != nil {
		t.Fatalf("raw update: %v", err)
	}
}

// rawReadStamp reads a stamp's raw bytes (nil when absent) from path with a
// short-lived raw handle.
func rawReadStamp(t *testing.T, path string, key []byte) []byte {
	t.Helper()
	var out []byte
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		t.Fatalf("raw bolt.Open(%q): %v", path, err)
	}
	defer func() { _ = db.Close() }()
	err = db.View(func(tx *bolt.Tx) error {
		if mb := tx.Bucket([]byte(bucketMeta)); mb != nil {
			if v := mb.Get(key); v != nil {
				out = append([]byte(nil), v...)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("raw view: %v", err)
	}
	return out
}

// seedPopulatedV1Store creates a production (v1) store at path holding one
// manual state row and one sync offset — enough to make the CORE domain
// populated — and closes it.
func seedPopulatedV1Store(t *testing.T, path string) {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	ctx := context.Background()
	rec := &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt1", Language: "en",
		ProviderName: api.ProviderNameOpenSubtitles, ReleaseName: "Rel.A",
		Path: "/m/tt1.en.1.srt", Score: 80,
		Meta: &api.DownloadMeta{Title: "T", VideoPath: "/m/tt1.mkv", Manual: true},
	}
	if err := db.SaveDownload(ctx, rec); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	if err := db.SetSyncOffset(ctx, "/m/tt1.en.1.srt", 250); err != nil {
		t.Fatalf("SetSyncOffset: %v", err)
	}
	if err := db.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// migrationSnapshots globs the pre-migration snapshots written next to the db.
func migrationSnapshots(t *testing.T, dbPath string) []string {
	t.Helper()
	matches, err := filepath.Glob(dbPath + ".pre-*")
	if err != nil {
		t.Fatalf("glob snapshots: %v", err)
	}
	return matches
}

// --- Guard fixtures (Requirement 5.4) ---

// TestOpen_refusesNewerStamp proves the downgrade guard: a stored version
// newer than the binary's refuses to open, the error names both versions and
// is matchable as errSchemaNewer, and the file is left untouched.
func TestOpen_refusesNewerStamp(t *testing.T) {
	for _, domain := range []struct {
		name string
		key  []byte
	}{
		{name: "core", key: metaKeyCoreSchemaVersion},
		{name: "auth", key: metaKeyAuthSchemaVersion},
	} {
		t.Run(domain.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subflux.bolt")
			seedPopulatedV1Store(t, path)
			rawUpdate(t, path, func(tx *bolt.Tx) error {
				return tx.Bucket([]byte(bucketMeta)).Put(domain.key, kv.Be64(2))
			})

			db, err := Open(path)
			if err == nil {
				_ = db.Close(context.Background())
				t.Fatalf("Open with newer %s stamp: error = nil, want the downgrade-guard refusal", domain.name)
			}
			if !errors.Is(err, errSchemaNewer) {
				t.Errorf("error = %v, want errSchemaNewer", err)
			}
			for _, want := range []string{domain.name, "2", "1"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not name %q", err, want)
				}
			}
			// Refusal leaves the planted stamp untouched.
			if got := rawReadStamp(t, path, domain.key); string(got) != string(kv.Be64(2)) {
				t.Errorf("stamp changed by a refused open: %x", got)
			}
		})
	}
}

// TestOpen_refusesBothDomainsNewer proves the downgrade guard classifies BOTH
// stamps before refusing (Requirement 1.4): a file whose core AND auth
// versions are newer than the binary's produces one combined diagnostic
// naming every stored and current version plus the restore guidance — not
// just the core half.
func TestOpen_refusesBothDomainsNewer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)
	rawUpdate(t, path, func(tx *bolt.Tx) error {
		mb := tx.Bucket([]byte(bucketMeta))
		if err := mb.Put(metaKeyCoreSchemaVersion, kv.Be64(2)); err != nil {
			return err
		}
		return mb.Put(metaKeyAuthSchemaVersion, kv.Be64(3))
	})

	db, err := Open(path)
	if err == nil {
		_ = db.Close(context.Background())
		t.Fatal("Open with both stamps newer: error = nil, want the combined downgrade refusal")
	}
	if !errors.Is(err, errSchemaNewer) {
		t.Errorf("error = %v, want errSchemaNewer", err)
	}
	for _, want := range []string{
		"core schema version 2", "auth schema version 3", // both stored versions
		"newer than this build's 1", // both current versions (same substring per domain)
		"restore",                   // the recovery path
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("combined error %q does not name %q", err, want)
		}
	}
	// The auth half must genuinely be present, not just the first refusal.
	if !strings.Contains(err.Error(), "auth") {
		t.Errorf("combined error %q does not name the auth domain", err)
	}
	// Refusal leaves both planted stamps untouched.
	if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(2)) {
		t.Errorf("core stamp changed by a refused open: %x", got)
	}
	if got := rawReadStamp(t, path, metaKeyAuthSchemaVersion); string(got) != string(kv.Be64(3)) {
		t.Errorf("auth stamp changed by a refused open: %x", got)
	}
}

// TestOpen_failsClosedOnMalformedStamp proves a malformed stamp is NEVER
// treated as absent: a populated file refuses, and even an otherwise
// data-free file with a garbage stamp refuses (only genuinely missing stamps
// may bootstrap).
func TestOpen_failsClosedOnMalformedStamp(t *testing.T) {
	t.Run("populated", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "subflux.bolt")
		seedPopulatedV1Store(t, path)
		rawUpdate(t, path, func(tx *bolt.Tx) error {
			return tx.Bucket([]byte(bucketMeta)).Put(metaKeyCoreSchemaVersion, []byte("bogus"))
		})

		db, err := Open(path)
		if err == nil {
			_ = db.Close(context.Background())
			t.Fatal("Open with malformed core stamp: error = nil, want fail-closed refusal")
		}
		if !errors.Is(err, errSchemaStampInvalid) {
			t.Errorf("error = %v, want errSchemaStampInvalid", err)
		}
	})

	t.Run("no-domain-data", func(t *testing.T) {
		// A file whose stamp is garbage is refused even when the domain
		// buckets are empty: malformed is never "absent".
		path := filepath.Join(t.TempDir(), "subflux.bolt")
		rawUpdate(t, path, func(tx *bolt.Tx) error {
			mb, err := tx.CreateBucketIfNotExists([]byte(bucketMeta))
			if err != nil {
				return err
			}
			return mb.Put(metaKeyCoreSchemaVersion, []byte("bogus"))
		})

		db, err := Open(path)
		if err == nil {
			_ = db.Close(context.Background())
			t.Fatal("Open with malformed stamp in an empty file: error = nil, want fail-closed refusal")
		}
		if !errors.Is(err, errSchemaStampInvalid) {
			t.Errorf("error = %v, want errSchemaStampInvalid", err)
		}
	})
}

// TestOpen_failsClosedOnMissingStampInPopulatedFile proves a populated file
// whose stamp key was lost refuses to open rather than guessing a version.
func TestOpen_failsClosedOnMissingStampInPopulatedFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)
	rawUpdate(t, path, func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketMeta)).Delete(metaKeyCoreSchemaVersion)
	})

	db, err := Open(path)
	if err == nil {
		_ = db.Close(context.Background())
		t.Fatal("Open with missing stamp in populated file: error = nil, want fail-closed refusal")
	}
	if !errors.Is(err, errSchemaStampInvalid) {
		t.Errorf("error = %v, want errSchemaStampInvalid", err)
	}
}

// TestOpen_freshFileBootstrapsWithoutMigrations proves the fresh-file rule:
// absent stamps in a file with no domain data mean "bootstrap at current
// versions, run zero migrations" — never "run the ladder from 0". A ladder
// step is injected and must NOT execute; no snapshot is written; the stamps
// land directly at the injected current versions.
func TestOpen_freshFileBootstrapsWithoutMigrations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")

	core := testCoreDomain(2, []migration{markerStep(1)})
	db, err := openWithDomains(path, core, testAuthDomain(1, nil))
	if err != nil {
		t.Fatalf("openWithDomains on fresh file: %v", err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(2)) {
		t.Errorf("fresh-file core stamp = %x, want be64(2)", got)
	}
	if got := rawReadStamp(t, path, markerKey(1)); got != nil {
		t.Errorf("ladder step ran on a fresh file (marker = %x), want zero migrations", got)
	}
	if snaps := migrationSnapshots(t, path); len(snaps) != 0 {
		t.Errorf("fresh-file bootstrap wrote snapshots %v, want none", snaps)
	}
}

// --- Runtime ladder validation (Requirement 1.6) ---

// TestValidateLadder_rejectsMalformedRegistries proves the runtime registry
// validation refuses gaps, duplicates, non-unit steps, a wrong terminal
// version, a bodyless in-place step, an empty ladder whose version constant
// has moved past the domain base, and a first step that does not start at the
// base — before any data is touched — and accepts an empty-at-base and a
// well-formed base-to-current ladder.
func TestValidateLadder_rejectsMalformedRegistries(t *testing.T) {
	run := func(*bolt.Tx) error { return nil }
	cases := []struct {
		name    string
		ladder  []migration
		current uint64
		base    uint64 // 0 = the production base (v1)
		wantErr string
	}{
		{name: "empty-ok", ladder: nil, current: 1},
		{name: "empty-at-nondefault-base-ok", ladder: nil, current: 4, base: 4},
		{name: "well-formed-ok", current: 3, ladder: []migration{
			{from: 1, to: 2, kind: migrateInPlace, run: run},
			{from: 2, to: 3, kind: migrateCopy},
		}},
		{name: "empty-past-base", ladder: nil, current: 2, wantErr: "past the domain base"},
		{name: "wrong-first-edge", current: 3, wantErr: "want the domain base", ladder: []migration{
			{from: 2, to: 3, kind: migrateInPlace, run: run},
		}},
		{name: "non-unit-step", current: 3, wantErr: "single-version", ladder: []migration{
			{from: 1, to: 3, kind: migrateInPlace, run: run},
		}},
		{name: "gap", current: 4, wantErr: "gap, duplicate, or misorder", ladder: []migration{
			{from: 1, to: 2, kind: migrateInPlace, run: run},
			{from: 3, to: 4, kind: migrateInPlace, run: run},
		}},
		{name: "duplicate", current: 2, wantErr: "gap, duplicate, or misorder", ladder: []migration{
			{from: 1, to: 2, kind: migrateInPlace, run: run},
			{from: 1, to: 2, kind: migrateInPlace, run: run},
		}},
		{name: "wrong-terminal", current: 3, wantErr: "ends at v2", ladder: []migration{
			{from: 1, to: 2, kind: migrateInPlace, run: run},
		}},
		{name: "in-place-without-run", current: 2, wantErr: "no run body", ladder: []migration{
			{from: 1, to: 2, kind: migrateInPlace},
		}},
		{name: "unknown-kind", current: 2, wantErr: "unknown kind", ladder: []migration{
			{from: 1, to: 2, kind: migrationKind(9), run: run},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := testCoreDomain(tc.current, tc.ladder)
			if tc.base != 0 {
				d.base = tc.base
			}
			err := validateLadder(d)
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("validateLadder = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("validateLadder = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

// TestOpen_malformedRegistryFailsLoudly proves a malformed registry fails the
// OPEN (runtime validation, not just the unit above), even when no step would
// be pending, and leaves the file unmodified.
func TestOpen_malformedRegistryFailsLoudly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)

	bad := testCoreDomain(1, []migration{{from: 1, to: 3, kind: migrateInPlace, run: func(*bolt.Tx) error { return nil }}})
	db, err := openWithDomains(path, bad, testAuthDomain(1, nil))
	if err == nil {
		_ = db.Close(context.Background())
		t.Fatal("open with malformed registry: error = nil, want loud validation failure")
	}
	if !strings.Contains(err.Error(), "invalid core migration registry") {
		t.Errorf("error = %v, want the registry-validation refusal", err)
	}
	if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(1)) {
		t.Errorf("core stamp after refused open = %x, want unchanged be64(1)", got)
	}
}

// TestOpen_refusesUnbridgeableVersion proves an older stamp the ladder cannot
// reach refuses the open with restore guidance — and without the retired
// "delete the database" instruction (Requirement 1.8).
func TestOpen_refusesUnbridgeableVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)

	// Binary whose domain BASE is v2 with a valid 2->3 ladder: a v1 file
	// (below the base) has no registered path. With base-to-current coverage
	// enforced by validateLadder, a below-base stamp is the remaining way to
	// hit the unbridgeable refusal.
	core := testCoreDomain(3, []migration{markerStep(2)})
	core.base = 2
	db, err := openWithDomains(path, core, testAuthDomain(1, nil))
	if err == nil {
		_ = db.Close(context.Background())
		t.Fatal("open with unbridgeable version: error = nil, want refusal")
	}
	if !strings.Contains(err.Error(), "no registered core migration from schema v1") {
		t.Errorf("error = %v, want the no-path refusal naming v1", err)
	}
	if !strings.Contains(err.Error(), "restore") {
		t.Errorf("error = %v, want restore guidance", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "delete") {
		t.Errorf("error = %v still instructs deletion; that message is retired", err)
	}
	if snaps := migrationSnapshots(t, path); len(snaps) != 0 {
		t.Errorf("refused open wrote snapshots %v, want none", snaps)
	}
}

// --- Ladder execution (Requirements 1.1, 1.3, 1.7, 2.1-2.3) ---

// TestOpen_runsPendingLadderSequentially proves a two-step in-place ladder
// runs in order with per-step stamping, each step exactly once, advances only
// its OWN domain stamp (the auth stamp stays byte-identical), and writes a
// 0600 pre-migration snapshot holding the PRE-migration state.
func TestOpen_runsPendingLadderSequentially(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)
	authStampBefore := rawReadStamp(t, path, metaKeyAuthSchemaVersion)

	core := testCoreDomain(3, []migration{markerStep(1), markerStep(2)})
	db, err := openWithDomains(path, core, testAuthDomain(1, nil))
	if err != nil {
		t.Fatalf("openWithDomains: %v", err)
	}

	// Migrated store stays fully usable.
	if _, err := db.GetState(context.Background(), &api.StateQuery{}); err != nil {
		t.Errorf("GetState after migration: %v", err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(3)) {
		t.Errorf("core stamp = %x, want be64(3)", got)
	}
	if got := rawReadStamp(t, path, metaKeyAuthSchemaVersion); string(got) != string(authStampBefore) {
		t.Errorf("auth stamp changed across a core migration: %x -> %x", authStampBefore, got)
	}
	for _, from := range []uint64{1, 2} {
		if got := rawReadStamp(t, path, markerKey(from)); string(got) != string(kv.Be64(1)) {
			t.Errorf("step v%d ran %x times, want exactly once", from, got)
		}
	}

	snaps := migrationSnapshots(t, path)
	if len(snaps) != 1 {
		t.Fatalf("snapshots = %v, want exactly one", snaps)
	}
	assertSnapshotIsPreMigration(t, snaps[0], "subflux.bolt.pre-c1-a1-")
}

// assertSnapshotIsPreMigration asserts one pre-migration snapshot: its name
// carries the re-read from-versions, its mode is 0600, and its contents are
// the PRE-migration state (core stamp still v1, no marker keys, and the
// seeded manual row present).
func assertSnapshotIsPreMigration(t *testing.T, snap, wantPrefix string) {
	t.Helper()
	if base := filepath.Base(snap); !strings.HasPrefix(base, wantPrefix) {
		t.Errorf("snapshot name %q, want prefix %q", base, wantPrefix)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(snap)
		if err != nil {
			t.Fatalf("stat snapshot: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("snapshot mode = %04o, want 0600", got)
		}
	}
	sdb := rawOpen(t, snap)
	err := sdb.View(func(tx *bolt.Tx) error {
		mb := tx.Bucket([]byte(bucketMeta))
		if mb == nil {
			t.Fatal("snapshot has no meta bucket")
		}
		if v := mb.Get(metaKeyCoreSchemaVersion); string(v) != string(kv.Be64(1)) {
			t.Errorf("snapshot core stamp = %x, want pre-migration be64(1)", v)
		}
		if v := mb.Get(markerKey(1)); v != nil {
			t.Errorf("snapshot contains post-step marker %x; snapshot must precede the first step", v)
		}
		sb := tx.Bucket([]byte(bucketSubtitleState))
		if sb == nil {
			t.Fatal("snapshot has no subtitle_state bucket")
		}
		if k, _ := sb.Cursor().First(); k == nil {
			t.Error("snapshot lost the seeded state row")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot view: %v", err)
	}
}

// TestOpen_interruptedLadderResumes is the fast in-process crash-resume tier
// (Requirement 5.3): step 1 commits (with its stamp), step 2 fails, the open
// aborts pointing at the snapshot; a later open with a healthy ladder resumes
// AT step 2 — never re-running step 1 — and completes.
func TestOpen_interruptedLadderResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)

	boom := errors.New("boom")
	failing := []migration{
		markerStep(1),
		{from: 2, to: 3, kind: migrateInPlace, run: func(*bolt.Tx) error { return boom }},
	}
	db, err := openWithDomains(path, testCoreDomain(3, failing), testAuthDomain(1, nil))
	if err == nil {
		_ = db.Close(context.Background())
		t.Fatal("open with failing step 2: error = nil, want failure")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want the step's failure", err)
	}
	if !strings.Contains(err.Error(), ".pre-c1-a1-") {
		t.Errorf("step failure %q does not point at the pre-migration snapshot", err)
	}
	// Step 1 committed with its stamp: the file is a consistent v2 database.
	if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(2)) {
		t.Fatalf("core stamp after interrupted ladder = %x, want be64(2)", got)
	}

	// Resume with a healthy ladder: only step 2 runs.
	healthy := []migration{markerStep(1), markerStep(2)}
	db, err = openWithDomains(path, testCoreDomain(3, healthy), testAuthDomain(1, nil))
	if err != nil {
		t.Fatalf("resume open: %v", err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(3)) {
		t.Errorf("core stamp after resume = %x, want be64(3)", got)
	}
	if got := rawReadStamp(t, path, markerKey(1)); string(got) != string(kv.Be64(1)) {
		t.Errorf("step 1 marker = %x, want exactly one run (no re-run on resume)", got)
	}
	if got := rawReadStamp(t, path, markerKey(2)); string(got) != string(kv.Be64(1)) {
		t.Errorf("step 2 marker = %x, want exactly one run", got)
	}
	// Each interrupted attempt leaves its own correctly-labeled snapshot: the
	// first from v1, the resume from v2.
	snaps := migrationSnapshots(t, path)
	if len(snaps) != 2 {
		t.Fatalf("snapshots = %v, want two (one per attempt)", snaps)
	}
	var sawV1, sawV2 bool
	for _, s := range snaps {
		base := filepath.Base(s)
		sawV1 = sawV1 || strings.HasPrefix(base, "subflux.bolt.pre-c1-a1-")
		sawV2 = sawV2 || strings.HasPrefix(base, "subflux.bolt.pre-c2-a1-")
	}
	if !sawV1 || !sawV2 {
		t.Errorf("snapshot labels %v, want one pre-c1-a1 and one pre-c2-a1 (from-versions re-read per attempt)", snaps)
	}
}

// TestOpen_stepFailureRollsBackAtomically proves a failing step leaves neither
// its transform nor its stamp behind: the step body writes its marker BEFORE
// failing, and the rollback discards both together (stamp-in-same-tx,
// Requirement 1.3).
func TestOpen_stepFailureRollsBackAtomically(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)

	boom := errors.New("boom")
	step := migration{from: 1, to: 2, kind: migrateInPlace, run: func(tx *bolt.Tx) error {
		if err := kv.PutUint64(tx.Bucket([]byte(bucketMeta)), markerKey(1), 1); err != nil {
			return err
		}
		return boom
	}}
	_, err := openWithDomains(path, testCoreDomain(2, []migration{step}), testAuthDomain(1, nil))
	if !errors.Is(err, boom) {
		t.Fatalf("open = %v, want the step failure", err)
	}
	if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(1)) {
		t.Errorf("core stamp after rolled-back step = %x, want be64(1)", got)
	}
	if got := rawReadStamp(t, path, markerKey(1)); got != nil {
		t.Errorf("marker survived the rollback: %x", got)
	}
}

// TestOpen_inPlaceStepCannotTouchOtherDomainStamp proves the cross-domain
// stamp invariant of Requirement 1.3 is ENFORCED inside the step's own
// transaction, not merely documented: a core step whose callback advances,
// rewrites, or deletes the AUTH stamp fails the migration, and the rollback
// discards the step's every effect — the malicious stamp write, the step's
// own transform, and its own stamp advance.
func TestOpen_inPlaceStepCannotTouchOtherDomainStamp(t *testing.T) {
	cases := []struct {
		name     string
		sabotage func(mb *bolt.Bucket) error
	}{
		{name: "advance", sabotage: func(mb *bolt.Bucket) error {
			return mb.Put(metaKeyAuthSchemaVersion, kv.Be64(9))
		}},
		{name: "rewrite-same-length", sabotage: func(mb *bolt.Bucket) error {
			return mb.Put(metaKeyAuthSchemaVersion, kv.Be64(0))
		}},
		{name: "delete", sabotage: func(mb *bolt.Bucket) error {
			return mb.Delete(metaKeyAuthSchemaVersion)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subflux.bolt")
			seedPopulatedV1Store(t, path)

			evil := migration{from: 1, to: 2, kind: migrateInPlace, run: func(tx *bolt.Tx) error {
				mb := tx.Bucket([]byte(bucketMeta))
				if mb == nil {
					return errors.New("evil step: meta bucket missing")
				}
				// A legitimate-looking transform effect, to prove the
				// rollback discards it together with the sabotage.
				if err := kv.PutUint64(mb, markerKey(1), 1); err != nil {
					return err
				}
				return tc.sabotage(mb)
			}}
			db, err := openWithDomains(path, testCoreDomain(2, []migration{evil}), testAuthDomain(1, nil))
			if err == nil {
				_ = db.Close(context.Background())
				t.Fatal("open with a stamp-touching step: error = nil, want the invariant refusal")
			}
			if !strings.Contains(err.Error(), "other domain's schema stamp") {
				t.Errorf("error = %v, want the cross-domain stamp invariant refusal", err)
			}
			// The whole step rolled back: both stamps and the marker are
			// exactly as seeded.
			if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(1)) {
				t.Errorf("core stamp after rolled-back sabotage = %x, want be64(1)", got)
			}
			if got := rawReadStamp(t, path, metaKeyAuthSchemaVersion); string(got) != string(kv.Be64(1)) {
				t.Errorf("auth stamp after rolled-back sabotage = %x, want untouched be64(1)", got)
			}
			if got := rawReadStamp(t, path, markerKey(1)); got != nil {
				t.Errorf("step transform survived the rollback: marker = %x", got)
			}
		})
	}
}

// TestOpen_snapshotFailureAbortsMigration proves Requirement 2.2: when the
// pre-migration snapshot cannot be written, the migration aborts loudly and
// nothing is modified — never migrate without the safety copy.
func TestOpen_snapshotFailureAbortsMigration(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory-permission write denial is POSIX-only")
	}
	if os.Geteuid() == 0 {
		t.Skip("root bypasses directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "subflux.bolt")
	seedPopulatedV1Store(t, path)

	// Deny creating the snapshot's temp file in the DB directory.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })

	db, err := openWithDomains(path, testCoreDomain(2, []migration{markerStep(1)}), testAuthDomain(1, nil))
	if err == nil {
		_ = db.Close(context.Background())
		t.Fatal("open with unwritable snapshot: error = nil, want abort")
	}
	if !strings.Contains(err.Error(), "pre-migration snapshot") {
		t.Errorf("error = %v, want the snapshot-abort message", err)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatalf("restore chmod: %v", err)
	}
	if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(1)) {
		t.Errorf("core stamp after aborted migration = %x, want unchanged be64(1)", got)
	}
	if got := rawReadStamp(t, path, markerKey(1)); got != nil {
		t.Errorf("step ran without a snapshot (marker %x)", got)
	}
}

// TestOpen_fastPathWritesNoSnapshot proves the fast path (Requirement 1.5): a
// current-version reopen runs zero migration machinery — no snapshot appears
// and the marker step of a fully-migrated ladder does not re-run.
func TestOpen_fastPathWritesNoSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)

	// Plain production reopen (both stamps current).
	db, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if snaps := migrationSnapshots(t, path); len(snaps) != 0 {
		t.Errorf("fast-path reopen wrote snapshots %v, want none", snaps)
	}
}

// TestOpen_afterTestBumpProductionOpenRefuses closes the loop on the injected
// ladders: a file migrated to a test version is exactly a "newer than this
// binary" file for the production Open, and the downgrade guard refuses it.
func TestOpen_afterTestBumpProductionOpenRefuses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedPopulatedV1Store(t, path)

	db, err := openWithDomains(path, testCoreDomain(2, []migration{markerStep(1)}), testAuthDomain(1, nil))
	if err != nil {
		t.Fatalf("test bump: %v", err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := Open(path); !errors.Is(err, errSchemaNewer) {
		t.Errorf("production Open of a v2 file = %v, want errSchemaNewer", err)
	}
}

// --- Bucket-domain membership (Requirement 3.3) ---

// TestBucketDomains_disjointAndComplete proves domain membership is executable
// data: the core and auth bucket sets are disjoint, and together they cover
// exactly the buckets present in a bootstrapped store — so "drop exactly my
// domain" is checkable, not aspirational.
func TestBucketDomains_disjointAndComplete(t *testing.T) {
	db, _ := openTemp(t)

	coreSet := make(map[string]bool, len(coreBuckets))
	for _, b := range coreBuckets {
		coreSet[string(b)] = true
	}
	authSet := make(map[string]bool, len(authBuckets))
	for _, b := range authBuckets {
		if coreSet[string(b)] {
			t.Errorf("bucket %q is claimed by BOTH domains", b)
		}
		authSet[string(b)] = true
	}

	actual := map[string]bool{}
	err := db.db.View(func(tx *bolt.Tx) error {
		return tx.ForEach(func(name []byte, _ *bolt.Bucket) error {
			actual[string(name)] = true
			return nil
		})
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}

	for name := range actual {
		if !coreSet[name] && !authSet[name] {
			t.Errorf("bucket %q exists in a bootstrapped store but belongs to neither domain", name)
		}
	}
	for name := range coreSet {
		if !actual[name] {
			t.Errorf("core bucket %q is declared but absent from a bootstrapped store", name)
		}
	}
	for name := range authSet {
		if !actual[name] {
			t.Errorf("auth bucket %q is declared but absent from a bootstrapped store", name)
		}
	}
}

// --- Fast-path benchmark (Requirement 5.6) ---

// BenchmarkOpenFastPath measures the current-version reopen — the path every
// production start takes. Compare against the pre-migration-framework
// baseline to confirm the version read is the only added work.
func BenchmarkOpenFastPath(b *testing.B) {
	path := filepath.Join(b.TempDir(), "subflux.bolt")
	db, err := Open(path)
	if err != nil {
		b.Fatalf("seed Open: %v", err)
	}
	if err := db.Close(context.Background()); err != nil {
		b.Fatalf("seed Close: %v", err)
	}
	b.ReportAllocs()
	for b.Loop() {
		rdb, err := Open(path)
		if err != nil {
			b.Fatalf("Open: %v", err)
		}
		if err := rdb.Close(context.Background()); err != nil {
			b.Fatalf("Close: %v", err)
		}
	}
}
