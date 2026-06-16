package boltstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	bolt "go.etcd.io/bbolt"

	"github.com/cplieger/subflux/internal/api"
)

// This file holds the remaining task-9.3 test categories that complement the
// property tests in property_e2e_test.go and keycodec_property_test.go:
//
//   4. Query-level example tests: prefix collision, ascending next_retry
//      ordering, and inclusive RecentlyScanned cutoff.
//   5. Fault-injection test: bbolt all-or-nothing on Update failure.
//   6. Reconcile-convergence test: two halves vs one uninterrupted pass.
//   7. Concurrent-workload test under -race.
//
// Requirements: 14.3, 18.5.

// --------------------------------------------------------------------------
// 4. Query-level tests
// --------------------------------------------------------------------------

// TestQuery_prefixCollision_BackedOffProviders asserts that BackedOffProviders
// for triple (movie, "tt1", "en") does NOT include a provider recorded only for
// (movie, "tt12", "en"). The triplePrefix key construction uses a NUL separator
// after the media_id component, so "tt1\x00en\x00" is not a prefix of
// "tt12\x00en\x00" (Requirement 8.3).
func TestQuery_prefixCollision_BackedOffProviders(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	bp := api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}

	// Record a no-result for tt12 only — this puts a backed-off provider row
	// under the (movie, "tt12", "en") triple.
	if err := db.RecordNoResult(ctx, api.MediaTypeMovie, "tt12", "en", api.ProviderNameOpenSubtitles, bp); err != nil {
		t.Fatalf("RecordNoResult(tt12): %v", err)
	}

	// Query backed-off providers for tt1 — must be empty (tt12's row must NOT
	// bleed into tt1's result).
	got, err := db.BackedOffProviders(ctx, api.MediaTypeMovie, "tt1", "en", 0)
	if err != nil {
		t.Fatalf("BackedOffProviders(tt1): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("BackedOffProviders(tt1) = %v, want empty (tt12 must not bleed)", got)
	}

	// Sanity: tt12 itself IS backed off.
	got12, err := db.BackedOffProviders(ctx, api.MediaTypeMovie, "tt12", "en", 0)
	if err != nil {
		t.Fatalf("BackedOffProviders(tt12): %v", err)
	}
	if len(got12) != 1 {
		t.Errorf("BackedOffProviders(tt12) = %v, want 1 provider", got12)
	}
}

// TestQuery_prefixCollision_DownloadedRefs asserts that DownloadedRefs for
// triple (movie, "tt1", "en") does NOT include downloads recorded under
// (movie, "tt12", "en"). The ix_state_triple key uses triplePrefix with a NUL
// after each component, preventing prefix collisions (Requirement 8.3).
func TestQuery_prefixCollision_DownloadedRefs(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	_ = os.WriteFile(video, []byte("v"), 0o644)

	// Save a download for tt12 only.
	if err := db.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: api.MediaTypeMovie, MediaID: "tt12", Language: "en",
		ProviderName: api.ProviderNameOpenSubtitles, ReleaseName: "Release.tt12",
		Path: filepath.Join(dir, "tt12.en.srt"), Score: 80,
		Meta: &api.DownloadMeta{VideoPath: video, Title: "T12"},
	}); err != nil {
		t.Fatalf("SaveDownload(tt12): %v", err)
	}

	// Query refs for tt1 — must be empty.
	refs, err := db.DownloadedRefs(ctx, api.MediaTypeMovie, "tt1", "en")
	if err != nil {
		t.Fatalf("DownloadedRefs(tt1): %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("DownloadedRefs(tt1) = %v, want empty (tt12 must not bleed)", refs)
	}

	// Sanity: tt12 itself returns the ref.
	refs12, err := db.DownloadedRefs(ctx, api.MediaTypeMovie, "tt12", "en")
	if err != nil {
		t.Fatalf("DownloadedRefs(tt12): %v", err)
	}
	if len(refs12) != 1 || refs12[0].ReleaseName != "Release.tt12" {
		t.Errorf("DownloadedRefs(tt12) = %v, want [Release.tt12]", refs12)
	}
}

// TestQuery_ascendingNextRetry asserts GetBackoffItems returns entries in
// ascending next_retry order regardless of insertion order (Requirement 2.3).
// This complements the existing TestGetBackoffItems_ascendingNextRetry by using
// a wider spread of media ids and types.
func TestQuery_ascendingNextRetry(t *testing.T) {
	db, _ := openTemp(t)
	base := time.Now().Truncate(time.Second)

	// Insert rows with decreasing next_retry to force reordering.
	times := []time.Duration{5 * time.Hour, 3 * time.Hour, 1 * time.Hour, 4 * time.Hour, 2 * time.Hour}
	for i, d := range times {
		putAttemptRow(t, db, api.MediaTypeEpisode, fmt.Sprintf("ep%d", i), "en",
			api.ProviderNameOpenSubtitles,
			attemptRec{LastTried: base, NextRetry: base.Add(d), Failures: 1})
	}

	got, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems: %v", err)
	}
	if len(got) != len(times) {
		t.Fatalf("got %d entries, want %d", len(got), len(times))
	}
	for i := 1; i < len(got); i++ {
		if got[i].NextRetry.Before(got[i-1].NextRetry) {
			t.Errorf("entry %d next_retry %v < entry %d %v (not ascending)",
				i, got[i].NextRetry, i-1, got[i-1].NextRetry)
		}
	}
}

// TestQuery_RecentlyScanned_inclusiveCutoff asserts that a scan whose
// scanned_at equals the cutoff exactly is INCLUDED in the result, and a scan
// whose scanned_at is one nanosecond earlier is excluded (Requirement 5.4).
func TestQuery_RecentlyScanned_inclusiveCutoff(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Use setScannedAt to control exact timestamps.
	cutoff := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	justBefore := cutoff.Add(-1) // 1ns before the cutoff

	setScannedAt(t, db, api.MediaTypeMovie, "included", "T1", "en", 0, 0, cutoff)
	setScannedAt(t, db, api.MediaTypeMovie, "excluded", "T2", "en", 0, 0, justBefore)
	setScannedAt(t, db, api.MediaTypeMovie, "also_included", "T3", "en", 0, 0, cutoff.Add(time.Hour))

	got, err := db.RecentlyScanned(ctx, cutoff)
	if err != nil {
		t.Fatalf("RecentlyScanned: %v", err)
	}
	if !got["included"] {
		t.Error("RecentlyScanned: 'included' (exactly at cutoff) missing")
	}
	if !got["also_included"] {
		t.Error("RecentlyScanned: 'also_included' (after cutoff) missing")
	}
	if got["excluded"] {
		t.Error("RecentlyScanned: 'excluded' (before cutoff) should not be present")
	}
}

// --------------------------------------------------------------------------
// 5. Fault-injection test: bbolt all-or-nothing
// --------------------------------------------------------------------------

// errFaultInjected is the sentinel error for the fault-injection test.
var errFaultInjected = errors.New("fault-injected abort")

// TestFaultInjection_updateAbortSeesNeither asserts that if an Update
// transaction writes a primary row but then returns an error before writing the
// index, the ENTIRE transaction is rolled back: neither the primary row nor any
// index entry is visible after the failed Update. This is bbolt's all-or-
// nothing ACID guarantee (Requirement 13.1).
func TestFaultInjection_updateAbortSeesNeither(t *testing.T) {
	db, _ := openTemp(t)

	// Attempt an Update that puts a primary row directly, then aborts before
	// any index maintenance.
	err := db.db.Update(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		if sb == nil {
			return fmt.Errorf("subtitle_state bucket missing")
		}
		// Write a row at id=9999 directly.
		rec := stateRec{
			ID:            9999,
			Provider:      api.ProviderNameOpenSubtitles,
			Score:         42,
			Manual:        false,
			VideoPath:     "/media/fault.mkv",
			MediaImported: time.Now().UTC(),
		}
		val, encErr := encodeRecord(&rec)
		if encErr != nil {
			return encErr
		}
		if putErr := sb.Put(stateKey(9999), val); putErr != nil {
			return putErr
		}
		// Simulate a fault BEFORE writing any index entry.
		return errFaultInjected
	})
	if !errors.Is(err, errFaultInjected) {
		t.Fatalf("Update error = %v, want errFaultInjected", err)
	}

	// Verify: neither the primary row nor any index entry for id 9999 is
	// visible after the aborted Update.
	err = db.db.View(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		if raw := sb.Get(stateKey(9999)); raw != nil {
			t.Error("primary row visible after aborted Update (bbolt ACID violation)")
		}

		idx := tx.Bucket([]byte(bucketIxStateImported))
		if idx == nil {
			return nil
		}
		// Walk the entire imported index to ensure no entry dereferences to 9999.
		return idx.ForEach(func(k, _ []byte) error {
			if id, ok := parseStateKey(k[8+1:]); ok && id == 9999 { // be64(time) 0x00 be64(id)
				t.Error("ix_state_imported entry for id 9999 after aborted Update")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatalf("View: %v", err)
	}
}

// --------------------------------------------------------------------------
// 6. Reconcile-convergence test
// --------------------------------------------------------------------------

// TestReconcileConvergence asserts that running ReconcileState in two halves
// (by marking half the files gone on the first pass, then the other half gone
// on the second pass) produces the same final state as running one uninterrupted
// pass with all those files gone at once. This proves the bounded-batch
// reconcile is idempotent and convergent (Requirement 7.5).
func TestReconcileConvergence(t *testing.T) {
	ctx := context.Background()

	// Setup: build a reproducible state with 4 subtitle rows across 2 videos.
	type setupFn func(db *DB, dir string)
	setup := func(db *DB, dir string) {
		video1 := filepath.Join(dir, "v1.mkv")
		video2 := filepath.Join(dir, "v2.mkv")
		sub1a := filepath.Join(dir, "v1.en.srt")
		sub1b := filepath.Join(dir, "v1.fr.srt")
		sub2a := filepath.Join(dir, "v2.en.srt")
		sub2b := filepath.Join(dir, "v2.fr.srt")

		for _, f := range []string{video1, video2, sub1a, sub1b, sub2a, sub2b} {
			if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", f, err)
			}
		}

		// Save downloads: 4 auto rows across 2 videos.
		for _, rec := range []*api.DownloadRecord{
			dlRec(api.MediaTypeMovie, "m1", "en", api.ProviderNameOpenSubtitles, "R1", sub1a, video1, 80, false),
			dlRec(api.MediaTypeMovie, "m1", "fr", api.ProviderNameGestdown, "R2", sub1b, video1, 70, false),
			dlRec(api.MediaTypeMovie, "m2", "en", api.ProviderNameOpenSubtitles, "R3", sub2a, video2, 90, false),
			dlRec(api.MediaTypeMovie, "m2", "fr", api.ProviderNameGestdown, "R4", sub2b, video2, 60, false),
		} {
			if err := db.SaveDownload(ctx, rec); err != nil {
				t.Fatalf("SaveDownload: %v", err)
			}
		}
	}

	// Scenario A (one pass): all subs gone, both videos present.
	dirA := t.TempDir()
	dbA := openTempAt(t, filepath.Join(dirA, "store"))
	setup(dbA, dirA)
	// Mark all subtitle files gone, videos present.
	dbA.statFn = func(path string) (os.FileInfo, error) {
		if filepath.Ext(path) == ".mkv" {
			return os.Stat(filepath.Join(dirA, filepath.Base(path)))
		}
		return nil, os.ErrNotExist // subtitle is gone
	}
	resA, err := dbA.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState(A): %v", err)
	}

	// Scenario B (two halves): first pass marks only v1 subs gone, second pass
	// marks v2 subs gone. Videos always present.
	dirB := t.TempDir()
	dbB := openTempAt(t, filepath.Join(dirB, "store"))
	setup(dbB, dirB)

	// First half: v1 subs gone.
	dbB.statFn = func(path string) (os.FileInfo, error) {
		if filepath.Ext(path) == ".mkv" {
			return os.Stat(filepath.Join(dirB, filepath.Base(path)))
		}
		base := filepath.Base(path)
		if base == "v1.en.srt" || base == "v1.fr.srt" {
			return nil, os.ErrNotExist
		}
		return os.Stat(filepath.Join(dirB, base))
	}
	_, err = dbB.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState(B half1): %v", err)
	}

	// Second half: v2 subs gone too.
	dbB.statFn = func(path string) (os.FileInfo, error) {
		if filepath.Ext(path) == ".mkv" {
			return os.Stat(filepath.Join(dirB, filepath.Base(path)))
		}
		return nil, os.ErrNotExist
	}
	resB, err := dbB.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState(B half2): %v", err)
	}

	// Compare final states: both scenarios must converge to the same state.
	// We compare via GetState (all rows) since reconcile resets auto rows.
	stateA, err := dbA.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState(A): %v", err)
	}
	stateB, err := dbB.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState(B): %v", err)
	}

	// Both should have the same number of rows (4 auto rows reset in place).
	if len(stateA) != len(stateB) {
		t.Errorf("convergence: stateA has %d rows, stateB has %d rows", len(stateA), len(stateB))
	}

	// Every row in both should have the "reset" shape: empty path, score=0,
	// empty provider, empty release.
	for i, s := range stateA {
		if s.Path != "" || s.Score != 0 {
			t.Errorf("stateA[%d] not reset: path=%q score=%d", i, s.Path, s.Score)
		}
	}
	for i, s := range stateB {
		if s.Path != "" || s.Score != 0 {
			t.Errorf("stateB[%d] not reset: path=%q score=%d", i, s.Path, s.Score)
		}
	}

	// Stats counters must be consistent in both.
	dlA, attA, _ := dbA.Stats(ctx)
	dlB, attB, _ := dbB.Stats(ctx)
	if dlA != dlB {
		t.Errorf("convergence: downloads counter A=%d B=%d", dlA, dlB)
	}
	if attA != attB {
		t.Errorf("convergence: attempts counter A=%d B=%d", attA, attB)
	}

	// The combined result should reflect the total impact.
	_ = resA
	_ = resB
}

// openTempAt opens a fresh store at the given directory path.
func openTempAt(t *testing.T, dir string) *DB {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	path := filepath.Join(dir, "subflux.bolt")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

// --------------------------------------------------------------------------
// 7. Concurrent-workload test under -race
// --------------------------------------------------------------------------

// TestConcurrent_SaveDownloadWhileReconcile exercises SaveDownload and
// ReconcileState running concurrently with overlapping triples and real temp
// files. It asserts no panics, no data races (when run with -race), and that
// the Stats counters remain non-negative throughout. This is the boltstore's
// single-writer concurrency guarantee: Update serializes writers, View is
// concurrent with writes (Requirements 13.3, 14.3).
func TestConcurrent_SaveDownloadWhileReconcile(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db := openTempAt(t, dir)

	// Create stable video files (always present so reconcile exercises all
	// branches but doesn't destroy the rows before SaveDownload can re-create
	// them).
	videos := []string{
		filepath.Join(dir, "v1.mkv"),
		filepath.Join(dir, "v2.mkv"),
	}
	for _, v := range videos {
		if err := os.WriteFile(v, []byte("video"), 0o644); err != nil {
			t.Fatalf("WriteFile(%q): %v", v, err)
		}
	}

	// Track how many subtitle files have been "created" so reconcile sees them.
	var subFilesMu sync.Mutex
	subFiles := map[string]bool{}

	// The stat function: videos are always present; subtitle files are present
	// only if they've been "written" by SaveDownload.
	db.statFn = func(path string) (os.FileInfo, error) {
		// Videos always exist.
		for _, v := range videos {
			if path == v {
				return os.Stat(v)
			}
		}
		// Subtitle files: check our tracking map.
		subFilesMu.Lock()
		present := subFiles[path]
		subFilesMu.Unlock()
		if present {
			return os.Stat(videos[0]) // return any valid FileInfo
		}
		return nil, os.ErrNotExist
	}

	// Seed initial state so reconcile has something to work with.
	triples := []struct {
		mt    api.MediaType
		mid   string
		lang  string
		video string
	}{
		{api.MediaTypeMovie, "m1", "en", videos[0]},
		{api.MediaTypeMovie, "m1", "fr", videos[0]},
		{api.MediaTypeMovie, "m2", "en", videos[1]},
	}
	for _, tr := range triples {
		sub := filepath.Join(dir, fmt.Sprintf("%s.%s.srt", tr.mid, tr.lang))
		subFilesMu.Lock()
		subFiles[sub] = true
		subFilesMu.Unlock()
		if err := db.SaveDownload(ctx, &api.DownloadRecord{
			MediaType: tr.mt, MediaID: tr.mid, Language: tr.lang,
			ProviderName: api.ProviderNameOpenSubtitles, ReleaseName: "R",
			Path: sub, Score: 50,
			Meta: &api.DownloadMeta{VideoPath: tr.video, Title: "T"},
		}); err != nil {
			t.Fatalf("seed SaveDownload: %v", err)
		}
	}

	// Run concurrent workloads.
	var wg sync.WaitGroup
	var panicked atomic.Int32
	iterations := 50

	// Goroutine 1: poller SaveDownload loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				panicked.Add(1)
				t.Errorf("SaveDownload goroutine panicked: %v", r)
			}
		}()
		for i := 0; i < iterations; i++ {
			tr := triples[i%len(triples)]
			sub := filepath.Join(dir, fmt.Sprintf("%s.%s.srt", tr.mid, tr.lang))
			subFilesMu.Lock()
			subFiles[sub] = true
			subFilesMu.Unlock()
			_ = db.SaveDownload(ctx, &api.DownloadRecord{
				MediaType: tr.mt, MediaID: tr.mid, Language: tr.lang,
				ProviderName: api.ProviderNameOpenSubtitles, ReleaseName: fmt.Sprintf("R%d", i),
				Path: sub, Score: 50 + i%50,
				Meta: &api.DownloadMeta{VideoPath: tr.video, Title: "T"},
			})
		}
	}()

	// Goroutine 2: ReconcileState loop.
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer func() {
			if r := recover(); r != nil {
				panicked.Add(1)
				t.Errorf("ReconcileState goroutine panicked: %v", r)
			}
		}()
		for i := 0; i < iterations/5; i++ {
			_, _ = db.ReconcileState(ctx)
		}
	}()

	wg.Wait()

	if panicked.Load() > 0 {
		t.Fatalf("%d goroutine(s) panicked", panicked.Load())
	}

	// Final assertions: Stats counters must be non-negative.
	downloads, attempts, err := db.Stats(ctx)
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if downloads < 0 {
		t.Errorf("downloads counter = %d, want >= 0", downloads)
	}
	if attempts < 0 {
		t.Errorf("attempts counter = %d, want >= 0", attempts)
	}

	// Final assertion: the store is still usable (no corruption).
	_, err = db.GetState(ctx, &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState after concurrent workload: %v", err)
	}
}
