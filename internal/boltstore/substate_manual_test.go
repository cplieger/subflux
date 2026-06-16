package boltstore

import (
	"context"
	"testing"

	bolt "go.etcd.io/bbolt"

	"github.com/cplieger/subflux/internal/api"
)

// This file covers the task-4.3 manual-lock methods: locked/unlocked
// detection (Requirement 4.2), non-destructive ClearManualLock (Requirement
// 4.3), manual count (Requirement 15.6), manual paths (Requirement 15.6), and
// NextManualNumber = max ordinal + 1 with a no-manual base case (Requirement
// 4.4). IsManuallyLocked / ManualDownloadCount are asserted to answer from the
// ix_state_triple projection without dereferencing primaries (Requirement
// 18.3).

// saveManual appends a manual row for the default triple with the given path
// and ordinal-bearing filename.
func saveManual(t *testing.T, db *DB, provider api.ProviderID, release, path string, score int) {
	t.Helper()
	rec := &api.DownloadRecord{
		MediaType:    testMT,
		MediaID:      testMID,
		Language:     testLang,
		ProviderName: provider,
		ReleaseName:  release,
		Path:         path,
		Score:        score,
		Meta:         &api.DownloadMeta{Title: "Test", VideoPath: "/media/test.mkv", Manual: true},
	}
	if err := db.SaveDownload(context.Background(), rec); err != nil {
		t.Fatalf("SaveDownload(manual %s): %v", path, err)
	}
}

// TestIsManuallyLocked_detectsLockState asserts a triple is unlocked with no
// manual row, locked once a manual row exists, and that an auto-only triple is
// not reported locked (Requirement 4.2).
func TestIsManuallyLocked_detectsLockState(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	locked, err := db.IsManuallyLocked(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("IsManuallyLocked (empty): %v", err)
	}
	if locked {
		t.Error("empty triple reported locked, want unlocked")
	}

	// An auto row must NOT lock the triple.
	if err := db.SaveDownload(ctx, autoRec(testProv, "Auto.Release", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	locked, err = db.IsManuallyLocked(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("IsManuallyLocked (auto only): %v", err)
	}
	if locked {
		t.Error("auto-only triple reported locked, want unlocked")
	}

	// A manual row locks it.
	saveManual(t, db, testProv, "Manual.Release", "/media/test.fr.1.srt", 70)
	locked, err = db.IsManuallyLocked(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("IsManuallyLocked (manual): %v", err)
	}
	if !locked {
		t.Error("triple with a manual row reported unlocked, want locked")
	}
}

// TestClearManualLock_nonDestructive asserts ClearManualLock flips the manual
// rows to auto WITHOUT deleting them: the rows remain present (same ids, same
// paths), the triple is no longer locked, the manual count drops to zero, and
// the rows are still visible to a triple read (Requirement 4.3).
func TestClearManualLock_nonDestructive(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	saveManual(t, db, testProv, "Manual.A", "/media/test.fr.1.srt", 70)
	saveManual(t, db, api.ProviderNameSubDL, "Manual.B", "/media/test.fr.2.srt", 75)

	before := readTripleRows(t, db, testMT, testMID, testLang)
	if len(before) != 2 {
		t.Fatalf("rows before clear = %d, want 2", len(before))
	}
	beforeIDs := map[int64]stateRec{}
	for _, r := range before {
		beforeIDs[r.ID] = r
	}

	if err := db.ClearManualLock(ctx, testMT, testMID, testLang); err != nil {
		t.Fatalf("ClearManualLock: %v", err)
	}

	// Rows are preserved (count, ids, paths) but now auto.
	after := readTripleRows(t, db, testMT, testMID, testLang)
	if len(after) != 2 {
		t.Fatalf("rows after clear = %d, want 2 (non-destructive, rows preserved)", len(after))
	}
	auto, manual := partitionRows(after)
	if len(manual) != 0 {
		t.Errorf("manual rows after clear = %d, want 0", len(manual))
	}
	if len(auto) != 2 {
		t.Errorf("auto rows after clear = %d, want 2", len(auto))
	}
	for _, r := range after {
		orig, ok := beforeIDs[r.ID]
		if !ok {
			t.Errorf("row id %d not present before clear (row was replaced, not flipped)", r.ID)
			continue
		}
		if r.Path != orig.Path || r.Score != orig.Score || r.Provider != orig.Provider {
			t.Errorf("row id %d data changed by clear: got %+v, want path/score/provider from %+v", r.ID, r, orig)
		}
		if r.Manual {
			t.Errorf("row id %d still manual after clear", r.ID)
		}
	}

	// The lock is gone and the manual count is zero.
	locked, err := db.IsManuallyLocked(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("IsManuallyLocked after clear: %v", err)
	}
	if locked {
		t.Error("triple still locked after ClearManualLock")
	}
	count, err := db.ManualDownloadCount(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("ManualDownloadCount after clear: %v", err)
	}
	if count != 0 {
		t.Errorf("manual count after clear = %d, want 0", count)
	}

	// The flipped rows remain visible as downloaded references.
	refs, err := db.DownloadedRefs(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("DownloadedRefs after clear: %v", err)
	}
	if len(refs) != 2 {
		t.Errorf("downloaded refs after clear = %d, want 2 (rows preserved)", len(refs))
	}
}

// TestClearManualLock_noManualRowsIsNoop asserts clearing a triple with no
// manual rows leaves any auto row untouched and is not an error.
func TestClearManualLock_noManualRowsIsNoop(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	if err := db.SaveDownload(ctx, autoRec(testProv, "Auto.Release", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	before := readTripleRows(t, db, testMT, testMID, testLang)

	if err := db.ClearManualLock(ctx, testMT, testMID, testLang); err != nil {
		t.Fatalf("ClearManualLock (no manual rows): %v", err)
	}

	after := readTripleRows(t, db, testMT, testMID, testLang)
	if len(after) != len(before) || len(after) != 1 {
		t.Fatalf("rows = %d, want unchanged (1)", len(after))
	}
	if after[0].ID != before[0].ID || after[0].Score != 80 {
		t.Errorf("auto row mutated by no-op clear: %+v", after[0])
	}
}

// TestManualDownloadCount asserts the count tracks only manual rows and ignores
// auto rows for the triple (Requirement 15.6).
func TestManualDownloadCount(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	if got, err := db.ManualDownloadCount(ctx, testMT, testMID, testLang); err != nil || got != 0 {
		t.Fatalf("empty count = (%d, %v), want (0, nil)", got, err)
	}

	// One auto row must not be counted.
	if err := db.SaveDownload(ctx, autoRec(testProv, "Auto.Release", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	saveManual(t, db, testProv, "Manual.A", "/media/test.fr.1.srt", 70)
	saveManual(t, db, api.ProviderNameSubDL, "Manual.B", "/media/test.fr.2.srt", 75)

	got, err := db.ManualDownloadCount(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("ManualDownloadCount: %v", err)
	}
	if got != 2 {
		t.Errorf("manual count = %d, want 2 (auto row excluded)", got)
	}
}

// TestManualSubtitlePaths_returnsNonEmptyManualPaths asserts only manual rows
// with a non-empty path are returned, and auto rows are excluded
// (Requirement 15.6).
func TestManualSubtitlePaths_returnsNonEmptyManualPaths(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Auto row (excluded) and two manual rows, one of which has an empty path.
	if err := db.SaveDownload(ctx, autoRec(testProv, "Auto.Release", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	saveManual(t, db, testProv, "Manual.A", "/media/test.fr.1.srt", 70)
	saveManual(t, db, api.ProviderNameSubDL, "Manual.Empty", "", 60)

	paths, err := db.ManualSubtitlePaths(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("ManualSubtitlePaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "/media/test.fr.1.srt" {
		t.Errorf("manual paths = %v, want [/media/test.fr.1.srt] (auto + empty-path excluded)", paths)
	}
}

// TestNextManualNumber asserts the next ordinal is one greater than the highest
// existing manual ordinal, the base case (no manual rows) is 1, and auto rows
// do not influence the result (Requirement 4.4).
func TestNextManualNumber(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Base case: no manual rows -> 1.
	if got := db.NextManualNumber(ctx, testMT, testMID, testLang); got != 1 {
		t.Errorf("NextManualNumber (empty) = %d, want 1", got)
	}

	// An auto row must not bump the ordinal.
	if err := db.SaveDownload(ctx, autoRec(testProv, "Auto.Release", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	if got := db.NextManualNumber(ctx, testMT, testMID, testLang); got != 1 {
		t.Errorf("NextManualNumber (auto only) = %d, want 1", got)
	}

	// Manual rows at ordinals 1 and 3 -> next is 4 (max + 1).
	saveManual(t, db, testProv, "Manual.1", "/media/test.fr.1.srt", 70)
	saveManual(t, db, testProv, "Manual.3", "/media/test.fr.3.srt", 72)
	if got := db.NextManualNumber(ctx, testMT, testMID, testLang); got != 4 {
		t.Errorf("NextManualNumber = %d, want 4 (max ordinal 3 + 1)", got)
	}
}

// TestNextManualNumber_variantPaths asserts ordinals are parsed from the HI and
// forced variant filename forms as well as the standard form (Requirement 4.4).
func TestNextManualNumber_variantPaths(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	saveManual(t, db, testProv, "Manual.hi", "/media/test.fr.hi.5.srt", 70)
	saveManual(t, db, testProv, "Manual.forced", "/media/test.fr.forced.2.srt", 72)

	if got := db.NextManualNumber(ctx, testMT, testMID, testLang); got != 6 {
		t.Errorf("NextManualNumber (variant paths) = %d, want 6 (max ordinal 5 + 1)", got)
	}
}

// TestManualLock_servedFromProjectionWithoutPrimaries asserts IsManuallyLocked
// and ManualDownloadCount answer purely from the ix_state_triple projection: it
// removes the primary subtitle_state rows while leaving the index in place, and
// the two methods still report the manual state correctly (Requirement 18.3).
func TestManualLock_servedFromProjectionWithoutPrimaries(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	saveManual(t, db, testProv, "Manual.A", "/media/test.fr.1.srt", 70)
	saveManual(t, db, api.ProviderNameSubDL, "Manual.B", "/media/test.fr.2.srt", 75)

	// Delete every primary subtitle_state row but leave ix_state_triple intact,
	// so a method that dereferenced primaries would see nothing. The projection
	// alone must still carry the manual flag.
	if err := db.db.Update(func(tx *bolt.Tx) error {
		sb := tx.Bucket([]byte(bucketSubtitleState))
		var keys [][]byte
		_ = sb.ForEach(func(k, _ []byte) error {
			kc := make([]byte, len(k))
			copy(kc, k)
			keys = append(keys, kc)
			return nil
		})
		for _, k := range keys {
			if err := sb.Delete(k); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("clearing primaries: %v", err)
	}

	locked, err := db.IsManuallyLocked(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("IsManuallyLocked: %v", err)
	}
	if !locked {
		t.Error("IsManuallyLocked = false with primaries removed; it dereferenced primaries instead of the projection")
	}
	count, err := db.ManualDownloadCount(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("ManualDownloadCount: %v", err)
	}
	if count != 2 {
		t.Errorf("ManualDownloadCount = %d with primaries removed, want 2 (answered from projection)", count)
	}
}
