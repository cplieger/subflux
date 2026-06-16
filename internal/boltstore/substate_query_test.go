package boltstore

import (
	"context"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

// This file covers task-4.2: CurrentScore (highest auto-row score plus that
// row's media_imported, found=false when no auto row, manual rows ignored;
// Requirement 3.4) and DownloadedRefs (distinct (release_name, provider) pairs
// across auto and manual rows, empty release names excluded; Requirement 3.5).

// manualRec builds a manual DownloadRecord whose ordinal is encoded in path,
// mirroring the handler's movie.lang.N.srt filename (Requirement 4.5).
func manualRec(provider api.ProviderID, release, path string, score int) *api.DownloadRecord {
	r := autoRec(provider, release, path, score)
	r.Meta.Manual = true
	return r
}

// --- CurrentScore ---

// TestCurrentScore_notFoundWhenNoRow asserts an untouched triple reports
// found=false with zero score and zero time and no error (Requirement 3.4).
func TestCurrentScore_notFoundWhenNoRow(t *testing.T) {
	db, _ := openTemp(t)

	score, imported, found, err := db.CurrentScore(context.Background(), testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("CurrentScore: %v", err)
	}
	if found {
		t.Errorf("found = true, want false for a triple with no auto row")
	}
	if score != 0 || !imported.IsZero() {
		t.Errorf("score=%d imported=%v, want 0 and zero time when not found", score, imported)
	}
}

// TestCurrentScore_highestAutoScoreAndImported asserts the highest auto-row
// score is returned together with that winning row's media_imported. Because
// SaveDownload upgrades the single auto row in place (preserving
// media_imported), the surviving auto row carries the latest score and the
// original import time (Requirement 3.4, cross-check with 3.1).
func TestCurrentScore_highestAutoScoreAndImported(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	if err := db.SaveDownload(ctx, autoRec(testProv, "Release.A", "/media/test.fr.srt", 70)); err != nil {
		t.Fatalf("first SaveDownload: %v", err)
	}
	auto, _ := partitionRows(readTripleRows(t, db, testMT, testMID, testLang))
	if len(auto) != 1 {
		t.Fatalf("auto rows = %d, want 1", len(auto))
	}
	wantImported := auto[0].MediaImported

	// Upgrade the auto row to a higher score; media_imported is preserved.
	if err := db.SaveDownload(ctx, autoRec(api.ProviderNameSubDL, "Release.B", "/media/test.fr.srt", 95)); err != nil {
		t.Fatalf("upgrade SaveDownload: %v", err)
	}

	score, imported, found, err := db.CurrentScore(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("CurrentScore: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true")
	}
	if score != 95 {
		t.Errorf("score = %d, want 95 (highest auto score)", score)
	}
	if !imported.Equal(wantImported) {
		t.Errorf("media_imported = %v, want preserved %v", imported, wantImported)
	}
}

// TestCurrentScore_ignoresManualRows asserts manual rows never contribute to
// CurrentScore: a high-scoring manual row alongside a lower auto row yields the
// auto score; a triple with only manual rows reports found=false
// (Requirement 3.4).
func TestCurrentScore_ignoresManualRows(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Auto row at score 40, plus a higher-scoring manual row at 99.
	if err := db.SaveDownload(ctx, autoRec(testProv, "Auto.Rel", "/media/test.fr.srt", 40)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	if err := db.SaveDownload(ctx, manualRec(testProv, "Manual.Rel", "/media/test.fr.1.srt", 99)); err != nil {
		t.Fatalf("manual SaveDownload: %v", err)
	}

	score, _, found, err := db.CurrentScore(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("CurrentScore: %v", err)
	}
	if !found {
		t.Fatal("found = false, want true (auto row present)")
	}
	if score != 40 {
		t.Errorf("score = %d, want 40 (manual row's 99 must be ignored)", score)
	}

	// A manual-only triple has no auto row: found=false.
	if err := db.SaveDownload(ctx, manualRec(testProv, "Only.Manual", "/media/test.es.1.srt", 88)); err != nil {
		t.Fatalf("manual-only SaveDownload: %v", err)
	}
	_, _, found2, err := db.CurrentScore(ctx, testMT, testMID, "es")
	if err != nil {
		t.Fatalf("CurrentScore (manual-only): %v", err)
	}
	if found2 {
		t.Error("found = true for a manual-only triple, want false (no auto row)")
	}
}

// --- DownloadedRefs ---

// TestDownloadedRefs_emptyForUnknownTriple asserts an untouched triple returns
// no refs and no error.
func TestDownloadedRefs_emptyForUnknownTriple(t *testing.T) {
	db, _ := openTemp(t)

	refs, err := db.DownloadedRefs(context.Background(), testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("DownloadedRefs: %v", err)
	}
	if len(refs) != 0 {
		t.Errorf("refs = %v, want empty", refs)
	}
}

// TestDownloadedRefs_distinctAcrossAutoAndManual asserts DownloadedRefs returns
// the distinct (release_name, provider) pairs across both the auto row and the
// manual rows, deduplicating repeats (Requirement 3.5).
func TestDownloadedRefs_distinctAcrossAutoAndManual(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Auto row (release R1/opensubtitles), then two manual rows: one distinct
	// pair (R2/subdl) and one duplicate of the auto pair (R1/opensubtitles).
	if err := db.SaveDownload(ctx, autoRec(testProv, "R1", "/media/test.fr.srt", 50)); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	if err := db.SaveDownload(ctx, manualRec(api.ProviderNameSubDL, "R2", "/media/test.fr.1.srt", 60)); err != nil {
		t.Fatalf("manual SaveDownload R2: %v", err)
	}
	if err := db.SaveDownload(ctx, manualRec(testProv, "R1", "/media/test.fr.2.srt", 70)); err != nil {
		t.Fatalf("manual SaveDownload dup R1: %v", err)
	}

	refs, err := db.DownloadedRefs(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("DownloadedRefs: %v", err)
	}

	want := map[api.DownloadedRef]int{
		{ReleaseName: "R1", Provider: testProv}:              0,
		{ReleaseName: "R2", Provider: api.ProviderNameSubDL}: 0,
	}
	if len(refs) != len(want) {
		t.Fatalf("refs = %v, want %d distinct pairs", refs, len(want))
	}
	for _, r := range refs {
		if _, ok := want[r]; !ok {
			t.Errorf("unexpected ref %v", r)
			continue
		}
		want[r]++
	}
	for ref, n := range want {
		if n != 1 {
			t.Errorf("ref %v seen %d times, want exactly 1 (distinct)", ref, n)
		}
	}
}

// TestDownloadedRefs_excludesEmptyReleaseName asserts rows with an empty
// release_name are excluded, matching the legacy `release_name <> ”` filter
// (Requirement 3.5). A manual download with a non-empty release name on the
// same triple is still returned.
func TestDownloadedRefs_excludesEmptyReleaseName(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Auto row with an EMPTY release name (legacy provider that exposed none).
	if err := db.SaveDownload(ctx, autoRec(testProv, "", "/media/test.fr.srt", 50)); err != nil {
		t.Fatalf("empty-release auto SaveDownload: %v", err)
	}
	// Manual row with a real release name.
	if err := db.SaveDownload(ctx, manualRec(api.ProviderNameSubDL, "Real.Release", "/media/test.fr.1.srt", 60)); err != nil {
		t.Fatalf("manual SaveDownload: %v", err)
	}

	refs, err := db.DownloadedRefs(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("DownloadedRefs: %v", err)
	}
	if len(refs) != 1 {
		t.Fatalf("refs = %v, want exactly 1 (empty release excluded)", refs)
	}
	got := refs[0]
	if got.ReleaseName != "Real.Release" || got.Provider != api.ProviderNameSubDL {
		t.Errorf("ref = %v, want {Real.Release, subdl}", got)
	}
}
