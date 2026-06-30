package boltstore

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// maintBP is a backoff config for seeding search_attempts in the delete tests.
var maintBP = api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}

// dlRec builds a DownloadRecord for the (mt, mid, lang) triple backed by
// videoPath. manual selects an auto (false) or manual-lock (true) row.
func dlRec(mt api.MediaType, mid, lang string, prov api.ProviderID, release, path, videoPath string, score int, manual bool) *api.DownloadRecord {
	return &api.DownloadRecord{
		MediaType: mt, MediaID: mid, Language: lang,
		ProviderName: prov, ReleaseName: release, Path: path, Score: score,
		Meta: &api.DownloadMeta{VideoPath: videoPath, Manual: manual},
	}
}

// seedFile upserts one external subtitle_files row for a media item.
func seedFile(t *testing.T, db *DB, mt api.MediaType, mid, lang, path string) {
	t.Helper()
	if err := db.UpsertSubtitleFile(context.Background(), mt, mid, &api.SubtitleFile{
		Language: lang, Variant: api.Variant("standard"), Source: api.SourceExternal,
		Codec: "subrip", Path: path,
	}); err != nil {
		t.Fatalf("UpsertSubtitleFile(%s/%s): %v", mid, lang, err)
	}
}

// seedScan records one scan_state row for a media item.
func seedScan(t *testing.T, db *DB, mt api.MediaType, mid string) {
	t.Helper()
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{
		MediaType: mt, MediaID: mid, Title: "Title " + mid,
	}); err != nil {
		t.Fatalf("RecordScanState(%s): %v", mid, err)
	}
}

// TestDeleteStateByPaths_emptyInput is a no-op that returns an empty result.
func TestDeleteStateByPaths_emptyInput(t *testing.T) {
	db, _ := openTemp(t)
	res, err := db.DeleteStateByPaths(context.Background(), nil)
	if err != nil {
		t.Fatalf("DeleteStateByPaths(nil): %v", err)
	}
	if len(res.Paths) != 0 {
		t.Errorf("Paths = %d, want 0", len(res.Paths))
	}
}

// TestDeleteStateByPaths_noMatch leaves the store untouched when no row is
// backed by the given path.
func TestDeleteStateByPaths_noMatch(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameOpenSubtitles, "R", "/p/tt1.fr.srt", "/media/tt1.mkv", 100, false)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	res, err := db.DeleteStateByPaths(ctx, []string{"/media/other.mkv"})
	if err != nil {
		t.Fatalf("DeleteStateByPaths: %v", err)
	}
	if len(res.Paths) != 0 {
		t.Errorf("Paths = %d, want 0", len(res.Paths))
	}
	downloads, _, _ := mustStats(t, db)
	if downloads != 1 {
		t.Errorf("downloads = %d, want 1 (row preserved)", downloads)
	}
}

// TestDeleteStateByPaths_removesAllRowsForVideo asserts a single video file
// backing several rows (auto + manual across two languages) is fully removed,
// its non-empty subtitle paths returned, and the affected triples' backoff
// cleared. Counters are consistent afterward.
func TestDeleteStateByPaths_removesAllRowsForVideo(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	const video = "/media/show.s01e01.mkv"

	// Auto fr + manual fr + auto en, all backed by the same video file.
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeEpisode, "ep1", "fr",
		api.ProviderNameOpenSubtitles, "Auto.FR", "/p/ep1.fr.srt", video, 80, false)); err != nil {
		t.Fatalf("SaveDownload(auto fr): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeEpisode, "ep1", "fr",
		api.ProviderNameSubDL, "Manual.FR", "/p/ep1.fr.1.srt", video, 50, true)); err != nil {
		t.Fatalf("SaveDownload(manual fr): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeEpisode, "ep1", "en",
		api.ProviderNameOpenSubtitles, "Auto.EN", "/p/ep1.en.srt", video, 90, false)); err != nil {
		t.Fatalf("SaveDownload(auto en): %v", err)
	}

	// Seed backoff for both affected triples (a different provider so the save
	// did not already clear it).
	if err := db.RecordNoResult(ctx, api.MediaTypeEpisode, "ep1", "fr", api.ProviderNameGestdown, maintBP); err != nil {
		t.Fatalf("RecordNoResult(fr): %v", err)
	}
	if err := db.RecordNoResult(ctx, api.MediaTypeEpisode, "ep1", "en", api.ProviderNameGestdown, maintBP); err != nil {
		t.Fatalf("RecordNoResult(en): %v", err)
	}

	res, err := db.DeleteStateByPaths(ctx, []string{video})
	if err != nil {
		t.Fatalf("DeleteStateByPaths: %v", err)
	}

	// Three rows had non-empty paths -> three returned subtitle paths.
	if len(res.Paths) != 3 {
		t.Errorf("Paths = %v, want 3 entries", res.Paths)
	}

	downloads, attempts, _ := mustStats(t, db)
	if downloads != 0 {
		t.Errorf("downloads = %d, want 0 (all rows removed)", downloads)
	}
	if attempts != 0 {
		t.Errorf("attempts = %d, want 0 (affected backoff cleared)", attempts)
	}

	// No rows remain for either triple.
	if rows := readTripleRows(t, db, api.MediaTypeEpisode, "ep1", "fr"); len(rows) != 0 {
		t.Errorf("fr rows = %d, want 0", len(rows))
	}
	if rows := readTripleRows(t, db, api.MediaTypeEpisode, "ep1", "en"); len(rows) != 0 {
		t.Errorf("en rows = %d, want 0", len(rows))
	}
}

// TestDeleteStateByPaths_cleansOrphanedCoverage asserts subtitle_files and
// scan_state for a media item left with no remaining state are removed.
func TestDeleteStateByPaths_cleansOrphanedCoverage(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	const video = "/media/movie.mkv"

	seedFile(t, db, api.MediaTypeMovie, "tt9", "fr", "/p/tt9.fr.srt")
	seedScan(t, db, api.MediaTypeMovie, "tt9")
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt9", "fr",
		api.ProviderNameOpenSubtitles, "R", "/p/tt9.fr.srt", video, 100, false)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	if _, err := db.DeleteStateByPaths(ctx, []string{video}); err != nil {
		t.Fatalf("DeleteStateByPaths: %v", err)
	}

	if total, _ := db.TotalSubtitleFiles(ctx); total != 0 {
		t.Errorf("TotalSubtitleFiles = %d, want 0 (orphaned files cleaned)", total)
	}
	states, err := db.GetScanStates(ctx, api.MediaTypeMovie, "tt9")
	if err != nil {
		t.Fatalf("GetScanStates: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("GetScanStates = %d, want 0 (orphaned scan_state cleaned)", len(states))
	}
}

// TestDeleteStateByPaths_preservesCoverageWhenStateRemains asserts coverage and
// scan_state survive when the media item still has a state row for another
// video file (only one of two languages is deleted).
func TestDeleteStateByPaths_preservesCoverageWhenStateRemains(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Same media item, two languages backed by DIFFERENT video files.
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt7", "fr",
		api.ProviderNameOpenSubtitles, "FR", "/p/tt7.fr.srt", "/media/tt7.mkv", 100, false)); err != nil {
		t.Fatalf("SaveDownload(fr): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt7", "en",
		api.ProviderNameOpenSubtitles, "EN", "/p/tt7.en.srt", "/media/tt7-en.mkv", 100, false)); err != nil {
		t.Fatalf("SaveDownload(en): %v", err)
	}
	seedFile(t, db, api.MediaTypeMovie, "tt7", "fr", "/p/tt7.fr.srt")
	seedScan(t, db, api.MediaTypeMovie, "tt7")

	if _, err := db.DeleteStateByPaths(ctx, []string{"/media/tt7.mkv"}); err != nil {
		t.Fatalf("DeleteStateByPaths: %v", err)
	}

	// The en state row remains, so the media item still owns its coverage.
	if total, _ := db.TotalSubtitleFiles(ctx); total != 1 {
		t.Errorf("TotalSubtitleFiles = %d, want 1 (coverage preserved)", total)
	}
	states, err := db.GetScanStates(ctx, api.MediaTypeMovie, "tt7")
	if err != nil {
		t.Fatalf("GetScanStates: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("GetScanStates = %d, want 1 (scan_state preserved)", len(states))
	}
	if rows := readTripleRows(t, db, api.MediaTypeMovie, "tt7", "en"); len(rows) != 1 {
		t.Errorf("en rows = %d, want 1 (preserved)", len(rows))
	}
	if rows := readTripleRows(t, db, api.MediaTypeMovie, "tt7", "fr"); len(rows) != 0 {
		t.Errorf("fr rows = %d, want 0 (deleted)", len(rows))
	}
}

// TestDeleteStateByPaths_idempotent asserts a second run on the same path is a
// safe no-op with consistent counters.
func TestDeleteStateByPaths_idempotent(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	const video = "/media/idem.mkv"

	seedFile(t, db, api.MediaTypeMovie, "tti", "fr", "/p/tti.fr.srt")
	seedScan(t, db, api.MediaTypeMovie, "tti")
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tti", "fr",
		api.ProviderNameOpenSubtitles, "R", "/p/tti.fr.srt", video, 100, false)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	first, err := db.DeleteStateByPaths(ctx, []string{video})
	if err != nil {
		t.Fatalf("DeleteStateByPaths(first): %v", err)
	}
	if len(first.Paths) != 1 {
		t.Errorf("first Paths = %d, want 1", len(first.Paths))
	}

	second, err := db.DeleteStateByPaths(ctx, []string{video})
	if err != nil {
		t.Fatalf("DeleteStateByPaths(second): %v", err)
	}
	if len(second.Paths) != 0 {
		t.Errorf("second Paths = %d, want 0 (no-op re-run)", len(second.Paths))
	}

	downloads, attempts, _ := mustStats(t, db)
	if downloads != 0 || attempts != 0 {
		t.Errorf("counters after re-run = (%d,%d), want (0,0)", downloads, attempts)
	}
	if total, _ := db.TotalSubtitleFiles(ctx); total != 0 {
		t.Errorf("TotalSubtitleFiles = %d, want 0", total)
	}
}

// TestDeleteStateByPaths_unrelatedUntouched asserts a delete of one video path
// leaves unrelated media, rows, backoff, and coverage intact.
func TestDeleteStateByPaths_unrelatedUntouched(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Target row.
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttDel", "fr",
		api.ProviderNameOpenSubtitles, "Del", "/p/del.fr.srt", "/media/del.mkv", 100, false)); err != nil {
		t.Fatalf("SaveDownload(del): %v", err)
	}
	// Unrelated row + backoff + coverage that must survive.
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttKeep", "en",
		api.ProviderNameOpenSubtitles, "Keep", "/p/keep.en.srt", "/media/keep.mkv", 100, false)); err != nil {
		t.Fatalf("SaveDownload(keep): %v", err)
	}
	if err := db.RecordNoResult(ctx, api.MediaTypeMovie, "ttKeep", "en", api.ProviderNameGestdown, maintBP); err != nil {
		t.Fatalf("RecordNoResult(keep): %v", err)
	}
	seedFile(t, db, api.MediaTypeMovie, "ttKeep", "en", "/p/keep.en.srt")
	seedScan(t, db, api.MediaTypeMovie, "ttKeep")

	if _, err := db.DeleteStateByPaths(ctx, []string{"/media/del.mkv"}); err != nil {
		t.Fatalf("DeleteStateByPaths: %v", err)
	}

	// Unrelated state row, backoff, and coverage all survive.
	downloads, attempts, _ := mustStats(t, db)
	if downloads != 1 {
		t.Errorf("downloads = %d, want 1 (unrelated row kept)", downloads)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (unrelated backoff kept)", attempts)
	}
	if total, _ := db.TotalSubtitleFiles(ctx); total != 1 {
		t.Errorf("TotalSubtitleFiles = %d, want 1 (unrelated coverage kept)", total)
	}
	states, err := db.GetScanStates(ctx, api.MediaTypeMovie, "ttKeep")
	if err != nil {
		t.Fatalf("GetScanStates: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("GetScanStates(ttKeep) = %d, want 1", len(states))
	}
	if rows := readTripleRows(t, db, api.MediaTypeMovie, "ttKeep", "en"); len(rows) != 1 {
		t.Errorf("ttKeep rows = %d, want 1", len(rows))
	}
}

// TestDeleteStateByPaths_cleansMultipleMediaAndFiles asserts the orphan-cleanup
// fan-out is exhaustive in two dimensions at once: every subtitle_files row of
// an orphaned media item is removed (not just the first), and every orphaned
// media item in the batch is cleaned (not just the first). Media A is orphaned
// with TWO coverage files; media B is a second orphaned item in the same
// DeleteStateByPaths call. After the delete, no files and no scan_state may
// remain for either.
func TestDeleteStateByPaths_cleansMultipleMediaAndFiles(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	const videoA = "/media/a.mkv"
	const videoB = "/media/b.mkv"

	// Media A: one state row backed by videoA, but TWO coverage files (fr+en).
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttA", "fr",
		api.ProviderNameOpenSubtitles, "A", "/p/a.fr.srt", videoA, 100, false)); err != nil {
		t.Fatalf("SaveDownload(A): %v", err)
	}
	seedFile(t, db, api.MediaTypeMovie, "ttA", "fr", "/p/a.fr.srt")
	seedFile(t, db, api.MediaTypeMovie, "ttA", "en", "/p/a.en.srt")
	seedScan(t, db, api.MediaTypeMovie, "ttA")

	// Media B: a second orphaned item in the same batch, one coverage file.
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttB", "fr",
		api.ProviderNameOpenSubtitles, "B", "/p/b.fr.srt", videoB, 100, false)); err != nil {
		t.Fatalf("SaveDownload(B): %v", err)
	}
	seedFile(t, db, api.MediaTypeMovie, "ttB", "fr", "/p/b.fr.srt")
	seedScan(t, db, api.MediaTypeMovie, "ttB")

	if total, _ := db.TotalSubtitleFiles(ctx); total != 3 {
		t.Fatalf("seeded TotalSubtitleFiles = %d, want 3", total)
	}

	// Delete both video files in a single call so both media items are orphaned
	// within one cleanup pass.
	if _, err := db.DeleteStateByPaths(ctx, []string{videoA, videoB}); err != nil {
		t.Fatalf("DeleteStateByPaths: %v", err)
	}

	// All three coverage files gone (A's second file proves the per-media loop
	// did not stop after the first).
	if total, _ := db.TotalSubtitleFiles(ctx); total != 0 {
		t.Errorf("TotalSubtitleFiles = %d, want 0 (every file of every orphan removed)", total)
	}
	// Both media items' scan_state gone (B proves the cleanup loop did not stop
	// after the first media item).
	for _, mid := range []string{"ttA", "ttB"} {
		states, err := db.GetScanStates(ctx, api.MediaTypeMovie, mid)
		if err != nil {
			t.Fatalf("GetScanStates(%s): %v", mid, err)
		}
		if len(states) != 0 {
			t.Errorf("GetScanStates(%s) = %d, want 0 (orphan scan_state cleaned)", mid, len(states))
		}
	}
}

// mustStats reads Stats and fails the test on error.
func mustStats(t *testing.T, db *DB) (downloads, attempts int, _ error) {
	t.Helper()
	d, a, err := db.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	return d, a, nil
}
