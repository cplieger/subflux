package boltstore

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cplieger/auth/v2"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/authstore"
	"github.com/cplieger/subflux/internal/store/kv"
	bolt "go.etcd.io/bbolt"
)

// This file is the subprocess crash tier (Requirement 5.3): the test binary
// re-executes itself as a child that runs a migration with an env-gated
// failpoint armed (SUBFLUX_MIGRATE_CRASHPOINT, read by the production
// crashPoint hooks in migrate.go) and dies abruptly via os.Exit at the named
// boundary — skipping every deferred close and commit exactly like a crash.
// The parent then reopens the same file, runs bbolt's Tx.Check, and asserts
// records, indexes, counters, sequences, stamps, and snapshot presence. The
// in-process interrupted-ladder test in migrate_test.go stays as the fast
// regression tier.

// Env vars of the parent/child protocol (the failpoint itself uses the
// production migrateCrashPointEnv).
const (
	crashChildEnv = "SUBFLUX_MIGRATE_CRASH_CHILD"
	crashDBEnv    = "SUBFLUX_MIGRATE_CRASH_DB"
	crashKindEnv  = "SUBFLUX_MIGRATE_CRASH_KIND"
)

// crashOffsetPath is the sync-offset record the copy-kind transform rewrites;
// its post-migration value proves the transform ran exactly once.
const crashOffsetPath = "/m/tt1.en.1.srt"

// crashDomains builds the injected ladder the child runs and the parent
// resumes. Shared so both processes execute the identical migration.
//
//   - "inplace": a two-step marker ladder to core v3 (exercises pre-commit
//     and between-steps boundaries).
//   - "copy": a single copy step to core v2 whose transform increments the
//     fixture's crashOffsetPath offset by 1 — a deliberately non-idempotent
//     effect, so a wrongly re-run step is visible as +2.
func crashDomains(kind string) (*migrationDomain, *migrationDomain) {
	switch kind {
	case "inplace":
		return testCoreDomain(3, []migration{markerStep(1), markerStep(2)}), testAuthDomain(1, nil)
	case "copy":
		transform := func(bucketPath []string, k, v []byte) ([]byte, []byte, error) {
			if len(bucketPath) == 1 && bucketPath[0] == bucketSyncOffsets && string(k) == crashOffsetPath {
				off, ok := kv.DecodeBe64(v)
				if !ok {
					return nil, nil, errors.New("crash fixture: malformed offset")
				}
				return k, kv.Be64(off + 1), nil
			}
			return k, v, nil
		}
		return testCoreDomain(2, []migration{{from: 1, to: 2, kind: migrateCopy, transform: transform}}), testAuthDomain(1, nil)
	default:
		panic("unknown crash kind " + kind)
	}
}

// seedCrashFixture builds the v1 store the child migrates: one manual row
// (with its lock), one sync offset at crashOffsetPath (value 250), and one
// auth user — the irreplaceable set whose survival every crash asserts.
func seedCrashFixture(t *testing.T, path string) {
	t.Helper()
	seedPopulatedV1Store(t, path) // manual row + offset 250 at crashOffsetPath
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open for auth seed: %v", err)
	}
	as := authstore.New(db.BoltDB())
	if err := as.CreateUser(context.Background(), &auth.User{Username: "alice", Role: auth.RoleAdmin, PasswordHash: "hash-a", Enabled: true}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	if err := db.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestMigrateCrashChild is the subprocess body, inert unless the parent armed
// the protocol env. It opens the fixture with the injected ladder; the armed
// failpoint kills the process mid-migration (exit crashPointExitCode). If the
// open completes, the child exits 0 and the PARENT fails the run (it demanded
// a crash).
func TestMigrateCrashChild(t *testing.T) {
	if os.Getenv(crashChildEnv) != "1" {
		t.Skip("subprocess helper for TestMigrateCrash_*; run via the parent tests")
	}
	core, authDom := crashDomains(os.Getenv(crashKindEnv))
	db, err := openWithDomains(os.Getenv(crashDBEnv), core, authDom)
	if err != nil {
		t.Fatalf("child open failed without crashing: %v", err)
	}
	_ = db.Close(context.Background())
}

// runCrashChild re-executes the test binary as the crash child with the
// failpoint armed and asserts it died at the failpoint (exit code
// crashPointExitCode) rather than completing or failing ordinarily.
func runCrashChild(t *testing.T, dbPath, kind, point string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestMigrateCrashChild$", "-test.v")
	cmd.Env = append(os.Environ(),
		crashChildEnv+"=1",
		crashDBEnv+"="+dbPath,
		crashKindEnv+"="+kind,
		migrateCrashPointEnv+"="+point,
	)
	out, err := cmd.CombinedOutput()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != crashPointExitCode {
		t.Fatalf("child armed at %q: err = %v, want exit code %d\nchild output:\n%s", point, err, crashPointExitCode, out)
	}
}

// recoverAndAssert reopens the crashed file with the same injected ladder (the
// resume), then asserts full integrity: bbolt Tx.Check is clean, both stamps
// are terminal, the irreplaceable fixture survived (manual row + lock, offset,
// auth user), the state indexes equal a primary re-scan, the counters agree,
// and a fresh insert allocates past every existing id (sequence intact). It
// returns the open recovered handle (closed via t.Cleanup) for follow-up
// assertions.
func recoverAndAssert(t *testing.T, path, kind string, wantOffset int64) *DB {
	t.Helper()
	core, authDom := crashDomains(kind)
	db, err := openWithDomains(path, core, authDom)
	if err != nil {
		t.Fatalf("recovery open: %v", err)
	}
	ctx := context.Background()
	t.Cleanup(func() { _ = db.Close(ctx) })

	if err := checkDB(db.db); err != nil {
		t.Errorf("Tx.Check after crash recovery: %v", err)
	}
	if got := rawStampInOpenDB(t, db, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(core.current)) {
		t.Errorf("core stamp = %x, want be64(%d)", got, core.current)
	}
	if got := rawStampInOpenDB(t, db, metaKeyAuthSchemaVersion); string(got) != string(kv.Be64(1)) {
		t.Errorf("auth stamp = %x, want be64(1)", got)
	}

	// Irreplaceable fixture: the manual row, its lock, the offset, the user.
	entries, err := db.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	manuals := 0
	var maxID int64
	for _, e := range entries {
		if e.Manual {
			manuals++
		}
		maxID = max(maxID, e.ID)
	}
	if manuals != 1 {
		t.Errorf("manual rows after recovery = %d, want 1", manuals)
	}
	locked, err := db.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt1", "en", api.VariantStandard)
	if err != nil || !locked {
		t.Errorf("IsManuallyLocked = (%v, %v), want locked", locked, err)
	}
	if got, err := db.GetSyncOffset(ctx, crashOffsetPath); err != nil || got != wantOffset {
		t.Errorf("GetSyncOffset = (%d, %v), want %d", got, err, wantOffset)
	}
	as := authstore.New(db.BoltDB())
	if u, err := as.GetUserByUsername(ctx, "alice"); err != nil || u == nil || u.PasswordHash != "hash-a" {
		t.Errorf("auth user after recovery = (%+v, %v), want alice intact", u, err)
	}

	assertStateIndexesMatchPrimary(t, db)

	downloads, _, err := db.Stats(ctx)
	if err != nil || downloads != len(entries) {
		t.Errorf("Stats downloads = (%d, %v), want %d (counter equals rows)", downloads, err, len(entries))
	}

	// Sequence: a fresh insert must allocate past every surviving id.
	rec := &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt8", Language: "en",
		ProviderName: api.ProviderNameOpenSubtitles, Path: "/m/tt8.en.srt", Score: 5,
		Meta: &api.DownloadMeta{Title: "T8", VideoPath: "/m/tt8.mkv"},
	}
	if err := db.SaveDownload(ctx, rec); err != nil {
		t.Fatalf("SaveDownload after recovery: %v", err)
	}
	after, err := db.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState after insert: %v", err)
	}
	for _, e := range after {
		if e.MediaID == "tt8" && e.ID <= maxID {
			t.Errorf("fresh id %d collides with surviving max id %d (sequence lost)", e.ID, maxID)
		}
	}

	if snaps := migrationSnapshots(t, path); len(snaps) == 0 {
		t.Error("no pre-migration snapshot survived the crash")
	}
	return db
}

// assertStateIndexesMatchPrimary rebuilds the three state index expectations
// from a full primary re-scan (via the production key/projection builders)
// and compares them to the live buckets — the crash tier's index assertion.
func assertStateIndexesMatchPrimary(t *testing.T, db *DB) {
	t.Helper()
	err := db.db.View(func(tx *bolt.Tx) error {
		wantQuad := map[string]string{}
		wantImported := map[string]string{}
		wantVideo := map[string]string{}
		verr := tx.Bucket([]byte(bucketSubtitleState)).ForEach(func(_, v []byte) error {
			var sr stateRec
			if derr := kv.Decode(v, &sr); derr != nil {
				return derr
			}
			wantQuad[string(stateQuadKey(sr.MediaType, sr.MediaID, sr.Language, sr.Variant, sr.ID))] = string(encodeStateProjection(sr.Manual, sr.Score, sr.Provider))
			wantImported[string(stateImportedKey(sr.MediaImported, sr.ID))] = ""
			wantVideo[string(stateVideoKey(sr.VideoPath, sr.ID))] = ""
			return nil
		})
		if verr != nil {
			return verr
		}
		for name, want := range map[string]map[string]string{
			bucketIxStateQuad:     wantQuad,
			bucketIxStateImported: wantImported,
			bucketIxStateVideo:    wantVideo,
		} {
			got := bucketMap(tx, name)
			if len(got) != len(want) {
				t.Errorf("%s: %d entries, want %d (= primary rescan)", name, len(got), len(want))
			}
			for k, wv := range want {
				if gv, ok := got[k]; !ok || gv != wv {
					t.Errorf("%s: entry %x = %x, want %x", name, k, gv, wv)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("index verification: %v", err)
	}
}

// crashTestSupported skips the crash tier where its mechanics don't hold.
func crashTestSupported(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("subprocess crash tier relies on POSIX rename/fsync semantics (the swap is Linux-container-scoped)")
	}
	if testing.Short() {
		t.Skip("subprocess crash tier skipped in -short mode")
	}
}

// TestMigrateCrash_inPlace kills the child at the two in-place boundaries and
// proves recovery: a pre-commit crash loses nothing (the step's tx rolled
// back), a between-steps crash resumes at step 2 without re-running step 1.
func TestMigrateCrash_inPlace(t *testing.T) {
	crashTestSupported(t)
	cases := []struct {
		point string
		// wantStamp is the core stamp the crash must leave behind.
		wantStamp uint64
		// wantMarkers maps step -> expected committed runs at crash time.
		wantMarker1 []byte
	}{
		{point: crashInPlacePreCommit, wantStamp: 1, wantMarker1: nil},
		{point: crashBetweenSteps, wantStamp: 2, wantMarker1: kv.Be64(1)},
	}
	for _, tc := range cases {
		t.Run(tc.point, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subflux.bolt")
			seedCrashFixture(t, path)

			runCrashChild(t, path, "inplace", tc.point)

			// The crash left a consistent previous-version database.
			if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(tc.wantStamp)) {
				t.Errorf("core stamp after crash = %x, want be64(%d)", got, tc.wantStamp)
			}
			if got := rawReadStamp(t, path, markerKey(1)); string(got) != string(tc.wantMarker1) {
				t.Errorf("step-1 marker after crash = %x, want %x", got, tc.wantMarker1)
			}

			db := recoverAndAssert(t, path, "inplace", 250)

			// Steps ran exactly once across crash + resume.
			for _, from := range []uint64{1, 2} {
				if got := rawStampInOpenDB(t, db, markerKey(from)); string(got) != string(kv.Be64(1)) {
					t.Errorf("step v%d marker = %x, want exactly one run across crash + resume", from, got)
				}
			}
			if tc.point == crashBetweenSteps {
				assertSnapshotLabels(t, path, "subflux.bolt.pre-c1-a1-", "subflux.bolt.pre-c2-a1-")
			}
		})
	}
}

// TestMigrateCrash_copy kills the child at the three copy-swap boundaries and
// proves recovery. Before the rename the live file must still be the intact
// v1 original (the step re-runs on resume); after the rename the migrated
// file IS the database (a crash must NOT re-run the step — the transform's +1
// lands exactly once either way).
func TestMigrateCrash_copy(t *testing.T) {
	crashTestSupported(t)
	cases := []struct {
		point         string
		wantStamp     uint64 // core stamp visible in the file right after the crash
		wantRawOffset int64  // crashOffsetPath value right after the crash
	}{
		{point: crashCopyAfterCommit, wantStamp: 1, wantRawOffset: 250},
		{point: crashCopyAfterRename, wantStamp: 2, wantRawOffset: 251},
		{point: crashCopyBeforeReopen, wantStamp: 2, wantRawOffset: 251},
	}
	for _, tc := range cases {
		t.Run(tc.point, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "subflux.bolt")
			seedCrashFixture(t, path)

			runCrashChild(t, path, "copy", tc.point)

			if got := rawReadStamp(t, path, metaKeyCoreSchemaVersion); string(got) != string(kv.Be64(tc.wantStamp)) {
				t.Errorf("core stamp after crash = %x, want be64(%d)", got, tc.wantStamp)
			}
			if got := rawSyncOffset(t, path); got != tc.wantRawOffset {
				t.Errorf("offset after crash = %d, want %d", got, tc.wantRawOffset)
			}
			// A pre-rename crash leaves the built-but-unswapped copy behind;
			// a post-rename crash has already consumed it.
			wantTemps := 0
			if tc.point == crashCopyAfterCommit {
				wantTemps = 1
			}
			if temps := copyTempFiles(t, path); len(temps) != wantTemps {
				t.Errorf("copy temp files after crash at %s = %v, want %d", tc.point, temps, wantTemps)
			}

			// Recovery completes the ladder (or fast-paths a completed swap);
			// the transform's +1 must have landed exactly once.
			recoverAndAssert(t, path, "copy", 251)

			// A successful retry never strands a stale copy temp.
			if temps := copyTempFiles(t, path); len(temps) != 0 {
				t.Errorf("stale copy temp files after successful recovery: %v", temps)
			}
		})
	}
}

// TestMigrateCrash_copyTempDoesNotAccumulate proves the deterministic temp
// path: every retry of a copy step that keeps crashing after building its
// destination targets the SAME <db>.migrate sibling (removing the previous
// attempt's leftover first), so a crash-retry loop holds at most ONE stale
// full-size copy — never one per attempt — and a finally-successful retry
// leaves none.
func TestMigrateCrash_copyTempDoesNotAccumulate(t *testing.T) {
	crashTestSupported(t)
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	seedCrashFixture(t, path)

	// Two consecutive crashes at the same post-build boundary: each attempt
	// builds the full destination, so a per-attempt unique name would leave
	// two copies here.
	runCrashChild(t, path, "copy", crashCopyAfterCommit)
	runCrashChild(t, path, "copy", crashCopyAfterCommit)
	if temps := copyTempFiles(t, path); len(temps) != 1 {
		t.Fatalf("copy temp files after two crashed attempts = %v, want exactly one (deterministic path, no accumulation)", temps)
	}

	// The successful retry reuses and consumes the path; the transform's +1
	// still lands exactly once (the crashed attempts never swapped).
	recoverAndAssert(t, path, "copy", 251)
	if temps := copyTempFiles(t, path); len(temps) != 0 {
		t.Errorf("stale copy temp files after successful retry: %v", temps)
	}
}

// rawSyncOffset reads crashOffsetPath's stored offset from path with a raw
// short-lived handle (the file must not be open elsewhere).
func rawSyncOffset(t *testing.T, path string) int64 {
	t.Helper()
	var out int64
	db, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: openTimeout})
	if err != nil {
		t.Fatalf("raw open: %v", err)
	}
	defer func() { _ = db.Close() }()
	err = db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucketSyncOffsets))
		if b == nil {
			return errors.New("sync_offsets bucket missing")
		}
		v := b.Get([]byte(crashOffsetPath))
		if v == nil {
			return errors.New("offset record missing")
		}
		u, ok := kv.DecodeBe64(v)
		if !ok {
			return errors.New("malformed offset value")
		}
		out = int64(u)
		return nil
	})
	if err != nil {
		t.Fatalf("raw offset read: %v", err)
	}
	return out
}

// assertSnapshotLabels asserts a snapshot with each wanted prefix exists —
// the "from-versions re-read per attempt" labeling proof.
func assertSnapshotLabels(t *testing.T, dbPath string, wantPrefixes ...string) {
	t.Helper()
	snaps := migrationSnapshots(t, dbPath)
	for _, want := range wantPrefixes {
		found := false
		for _, s := range snaps {
			if strings.HasPrefix(filepath.Base(s), want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no snapshot labeled %q among %v", want, snaps)
		}
	}
}
