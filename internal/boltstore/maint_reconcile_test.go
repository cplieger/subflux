package boltstore

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// This file covers the task-6.3 ReconcileState three-way, group-aware semantics
// (Requirements 7.1-7.5), tested against REAL files on disk so the production
// os.Stat-based classifier (DB.statFn defaults to os.Stat in Open) is exercised
// end to end rather than a fake oracle:
//
//   - video gone -> delete the whole fan-out (rows + orphaned subtitle files +
//     backoff + orphaned scan_state);
//   - subtitle gone with a sibling still present -> delete ONLY that row,
//     preserving the manual lock, clearing no backoff, resetting nothing;
//   - all subtitles for a triple gone -> reset the auto rows in place, delete
//     the manual rows, clear the triple's backoff;
//
// plus idempotency (run twice == run once), unrelated-media isolation, and
// counter consistency.

// mkfile writes a placeholder file at path so os.Stat reports it present, and
// returns the path for convenience.
func mkfile(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	return path
}

// rmfile removes a file so os.Stat reports it gone (os.ErrNotExist), driving the
// reconcile classifier's video-gone / subtitle-gone branches.
func rmfile(t *testing.T, path string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatalf("Remove(%q): %v", path, err)
	}
}

// assertDownloadsConsistent asserts the maintained downloads counter equals the
// number of state rows actually returned by GetState (a full primary walk), so
// every reconcile branch keeps the O(1) counter consistent with the rows.
func assertDownloadsConsistent(t *testing.T, db *DB) {
	t.Helper()
	downloads, _, _ := mustStats(t, db)
	entries, err := db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if downloads != len(entries) {
		t.Errorf("downloads counter = %d, but GetState returned %d rows (counter drift)",
			downloads, len(entries))
	}
}

// TestReconcileState_noRecords is a no-op on an empty store.
func TestReconcileState_noRecords(t *testing.T) {
	db, _ := openTemp(t)
	res, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	if len(res.Deleted.Paths) != 0 || res.ResetCount != 0 {
		t.Errorf("empty store: deleted=%d reset=%d, want 0/0",
			len(res.Deleted.Paths), res.ResetCount)
	}
}

// TestReconcileState_allPresentIsNoop leaves rows, backoff, and coverage intact
// when every video and subtitle file still exists.
func TestReconcileState_allPresentIsNoop(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	dir := t.TempDir()

	video := mkfile(t, filepath.Join(dir, "movie.mkv"))
	sub := mkfile(t, filepath.Join(dir, "movie.fr.srt"))
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameOpenSubtitles, "R", sub, video, 100, false)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}
	seedAttempt(t, db, api.MediaTypeMovie, "tt1", "fr", api.ProviderNameGestdown)

	res, err := db.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	if len(res.Deleted.Paths) != 0 || res.ResetCount != 0 {
		t.Errorf("all present: deleted=%d reset=%d, want 0/0",
			len(res.Deleted.Paths), res.ResetCount)
	}
	if rows := readTripleRows(t, db, api.MediaTypeMovie, "tt1", "fr"); len(rows) != 1 {
		t.Errorf("rows = %d, want 1 (untouched)", len(rows))
	}
	assertBackoffConsistent(t, db, 1) // backoff untouched
	assertDownloadsConsistent(t, db)
}

// TestReconcileState_videoGoneDeletesFanout asserts the video-gone branch
// removes every row backed by the missing video (auto + manual across
// languages), clears the affected triples' backoff, cleans orphaned coverage
// (subtitle_files + scan_state), and returns the deleted rows' non-empty
// subtitle paths (Requirement 7.1).
func TestReconcileState_videoGoneDeletesFanout(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	dir := t.TempDir()

	video := filepath.Join(dir, "show.s01e01.mkv")
	mkfile(t, video)
	subFR := mkfile(t, filepath.Join(dir, "show.fr.srt"))
	subFRm := mkfile(t, filepath.Join(dir, "show.fr.1.srt"))
	subEN := mkfile(t, filepath.Join(dir, "show.en.srt"))

	// Auto fr + manual fr + auto en, all backed by the same video file.
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeEpisode, "ep1", "fr",
		api.ProviderNameOpenSubtitles, "Auto.FR", subFR, video, 80, false)); err != nil {
		t.Fatalf("SaveDownload(auto fr): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeEpisode, "ep1", "fr",
		api.ProviderNameSubDL, "Manual.FR", subFRm, video, 50, true)); err != nil {
		t.Fatalf("SaveDownload(manual fr): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeEpisode, "ep1", "en",
		api.ProviderNameOpenSubtitles, "Auto.EN", subEN, video, 90, false)); err != nil {
		t.Fatalf("SaveDownload(auto en): %v", err)
	}
	seedFile(t, db, api.MediaTypeEpisode, "ep1", "fr", subFR)
	seedScan(t, db, api.MediaTypeEpisode, "ep1")
	// Backoff on both affected triples (a provider the saves did not clear).
	seedAttempt(t, db, api.MediaTypeEpisode, "ep1", "fr", api.ProviderNameGestdown)
	seedAttempt(t, db, api.MediaTypeEpisode, "ep1", "en", api.ProviderNameGestdown)

	// Video disappears from disk.
	rmfile(t, video)

	res, err := db.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}

	// Three rows had non-empty subtitle paths -> three returned paths.
	if len(res.Deleted.Paths) != 3 {
		t.Errorf("Deleted.Paths = %v, want 3 entries", res.Deleted.Paths)
	}
	if res.ResetCount != 0 {
		t.Errorf("ResetCount = %d, want 0 (video gone deletes, never resets)", res.ResetCount)
	}

	// All rows for both triples removed.
	if rows := readTripleRows(t, db, api.MediaTypeEpisode, "ep1", "fr"); len(rows) != 0 {
		t.Errorf("fr rows = %d, want 0", len(rows))
	}
	if rows := readTripleRows(t, db, api.MediaTypeEpisode, "ep1", "en"); len(rows) != 0 {
		t.Errorf("en rows = %d, want 0", len(rows))
	}
	// Backoff cleared, coverage orphans cleaned.
	assertBackoffConsistent(t, db, 0)
	if total, _ := db.TotalSubtitleFiles(ctx); total != 0 {
		t.Errorf("TotalSubtitleFiles = %d, want 0 (orphaned coverage cleaned)", total)
	}
	states, err := db.GetScanStates(ctx, api.MediaTypeEpisode, "ep1")
	if err != nil {
		t.Fatalf("GetScanStates: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("GetScanStates = %d, want 0 (orphaned scan_state cleaned)", len(states))
	}
	assertDownloadsConsistent(t, db)
}

// TestReconcileState_siblingPresentDeletesOnlyMissingRow asserts the sibling-
// present branch: when some rows' subtitles are gone but another row for the
// SAME triple still has its subtitle on disk, EVERY missing-subtitle row is
// deleted (not just the first) while the present row is preserved. The manual
// lock is PRESERVED, backoff is NOT cleared, and nothing is reset
// (Requirement 7.2).
func TestReconcileState_siblingPresentDeletesOnlyMissingRow(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	dir := t.TempDir()

	video := mkfile(t, filepath.Join(dir, "movie.mkv"))
	autoSub := mkfile(t, filepath.Join(dir, "movie.fr.srt"))      // will be removed
	manualSub := mkfile(t, filepath.Join(dir, "movie.fr.1.srt"))  // stays present (the lock)
	manualGone := mkfile(t, filepath.Join(dir, "movie.fr.2.srt")) // will be removed

	// Auto row (missing sub) + a manual row whose sub is also removed + a manual
	// row whose sub stays present and holds the lock. Two of the three rows are
	// missing-subtitle, so the delete loop must run more than once.
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameOpenSubtitles, "Auto", autoSub, video, 80, false)); err != nil {
		t.Fatalf("SaveDownload(auto): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameSubDL, "Manual", manualSub, video, 50, true)); err != nil {
		t.Fatalf("SaveDownload(manual present): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameSubDL, "ManualGone", manualGone, video, 50, true)); err != nil {
		t.Fatalf("SaveDownload(manual gone): %v", err)
	}
	// Seed backoff AFTER the saves (each save clears it) so we can assert it
	// survives the sibling-present branch.
	seedAttempt(t, db, api.MediaTypeMovie, "tt1", "fr", api.ProviderNameGestdown)

	rmfile(t, autoSub)    // the auto row's subtitle disappears
	rmfile(t, manualGone) // a second row's subtitle disappears; manualSub remains

	res, err := db.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	if len(res.Deleted.Paths) != 0 {
		t.Errorf("Deleted.Paths = %d, want 0 (no video-gone delete)", len(res.Deleted.Paths))
	}
	if res.ResetCount != 0 {
		t.Errorf("ResetCount = %d, want 0 (sibling present -> no reset)", res.ResetCount)
	}

	// Both missing-subtitle rows are deleted; only the manual (present) row
	// survives, and it still holds the lock.
	rows := readTripleRows(t, db, api.MediaTypeMovie, "tt1", "fr")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (every missing-sub row deleted, present sibling kept)", len(rows))
	}
	if !rows[0].Manual || rows[0].ReleaseName != "Manual" {
		t.Errorf("surviving row = {manual:%v release:%q}, want {true Manual}",
			rows[0].Manual, rows[0].ReleaseName)
	}
	locked, err := db.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt1", "fr")
	if err != nil {
		t.Fatalf("IsManuallyLocked: %v", err)
	}
	if !locked {
		t.Error("IsManuallyLocked = false, want true (manual lock must be preserved)")
	}
	// Backoff NOT cleared in this branch.
	assertBackoffConsistent(t, db, 1)
	assertDownloadsConsistent(t, db)
}

// TestReconcileState_allSubsGoneResetsAutoDeletesManual asserts the all-gone
// branch: when every subtitle for a triple is gone (video present), the auto
// rows are reset in place (path/score/provider/release_name cleared,
// media_imported bumped to now), the manual rows are deleted, and the triple's
// backoff is cleared (Requirement 7.3).
func TestReconcileState_allSubsGoneResetsAutoDeletesManual(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	dir := t.TempDir()

	video := mkfile(t, filepath.Join(dir, "movie.mkv"))
	autoSub := mkfile(t, filepath.Join(dir, "movie.fr.srt"))
	manualSub := mkfile(t, filepath.Join(dir, "movie.fr.1.srt"))

	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameOpenSubtitles, "Auto", autoSub, video, 100, false)); err != nil {
		t.Fatalf("SaveDownload(auto): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameSubDL, "Manual", manualSub, video, 200, true)); err != nil {
		t.Fatalf("SaveDownload(manual): %v", err)
	}
	seedAttempt(t, db, api.MediaTypeMovie, "tt1", "fr", api.ProviderNameGestdown)

	// Capture the auto row's original media_imported to prove the bump.
	autoBefore, _ := partitionRows(readTripleRows(t, db, api.MediaTypeMovie, "tt1", "fr"))
	if len(autoBefore) != 1 {
		t.Fatalf("setup: auto rows = %d, want 1", len(autoBefore))
	}
	origImported := autoBefore[0].MediaImported

	// Both subtitle files disappear (video still present).
	rmfile(t, autoSub)
	rmfile(t, manualSub)
	time.Sleep(10 * time.Millisecond) // ensure the reset timestamp is strictly later

	res, err := db.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	if len(res.Deleted.Paths) != 0 {
		t.Errorf("Deleted.Paths = %d, want 0 (video present -> reset, not delete)", len(res.Deleted.Paths))
	}
	if res.ResetCount != 1 {
		t.Errorf("ResetCount = %d, want 1 (one triple reset)", res.ResetCount)
	}

	// The manual row is gone; one auto row remains, reset in place.
	rows := readTripleRows(t, db, api.MediaTypeMovie, "tt1", "fr")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1 (manual deleted, auto reset)", len(rows))
	}
	r := rows[0]
	if r.Manual {
		t.Error("surviving row Manual = true, want false (manual row should be deleted)")
	}
	if r.Path != "" || r.Score != 0 || r.Provider != "" || r.ReleaseName != "" {
		t.Errorf("reset row = {path:%q score:%d provider:%q release:%q}, want all-empty/zero",
			r.Path, r.Score, r.Provider, r.ReleaseName)
	}
	if r.VideoPath != video {
		t.Errorf("reset row VideoPath = %q, want %q (video_path preserved)", r.VideoPath, video)
	}
	if !r.MediaImported.After(origImported) {
		t.Errorf("reset row MediaImported = %v, want strictly after original %v (bumped to now)",
			r.MediaImported, origImported)
	}
	// Lock cleared (manual row deleted) and backoff cleared.
	locked, err := db.IsManuallyLocked(ctx, api.MediaTypeMovie, "tt1", "fr")
	if err != nil {
		t.Fatalf("IsManuallyLocked: %v", err)
	}
	if locked {
		t.Error("IsManuallyLocked = true, want false (manual rows deleted)")
	}
	assertBackoffConsistent(t, db, 0)
	assertDownloadsConsistent(t, db)
}

// TestReconcileState_allSubsGoneAutoOnlyReset covers the all-gone branch with
// multiple auto rows and no manual row: every auto row is reset and the triple
// counts once in ResetCount.
func TestReconcileState_allSubsGoneAutoOnlyReset(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	dir := t.TempDir()

	video := mkfile(t, filepath.Join(dir, "movie.mkv"))
	sub1 := mkfile(t, filepath.Join(dir, "movie.fr.v1.srt"))
	sub2 := mkfile(t, filepath.Join(dir, "movie.fr.v2.srt"))

	// SaveDownload upserts auto rows in place, so two auto rows for one triple
	// are produced the way the live store produces them: save an auto row plus
	// a manual row, then ClearManualLock, which non-destructively flips the
	// manual row to an auto row — yielding two auto rows on the same triple.
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameOpenSubtitles, "RelA", sub1, video, 100, false)); err != nil {
		t.Fatalf("SaveDownload(auto A): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "tt1", "fr",
		api.ProviderNameSubDL, "RelB", sub2, video, 110, true)); err != nil {
		t.Fatalf("SaveDownload(manual B): %v", err)
	}
	if err := db.ClearManualLock(ctx, api.MediaTypeMovie, "tt1", "fr"); err != nil {
		t.Fatalf("ClearManualLock: %v", err)
	}
	// Now two auto rows exist for the triple.
	if auto, _ := partitionRows(readTripleRows(t, db, api.MediaTypeMovie, "tt1", "fr")); len(auto) != 2 {
		t.Fatalf("setup: auto rows = %d, want 2", len(auto))
	}

	rmfile(t, sub1)
	rmfile(t, sub2)

	res, err := db.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}
	if res.ResetCount != 1 {
		t.Errorf("ResetCount = %d, want 1 (one triple, counted once)", res.ResetCount)
	}
	rows := readTripleRows(t, db, api.MediaTypeMovie, "tt1", "fr")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (both auto rows reset, none deleted)", len(rows))
	}
	for i, r := range rows {
		if r.Path != "" || r.Score != 0 || r.Provider != "" {
			t.Errorf("row[%d] not reset: {path:%q score:%d provider:%q}", i, r.Path, r.Score, r.Provider)
		}
	}
	assertDownloadsConsistent(t, db)
}

// TestReconcileState_unrelatedMediaUntouched asserts a reconcile that deletes
// and resets one media item leaves an unrelated, fully-present media item's
// rows, backoff, and coverage completely intact.
func TestReconcileState_unrelatedMediaUntouched(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	dir := t.TempDir()

	// Doomed item: video will be removed.
	delVideo := mkfile(t, filepath.Join(dir, "del.mkv"))
	delSub := mkfile(t, filepath.Join(dir, "del.fr.srt"))
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttDel", "fr",
		api.ProviderNameOpenSubtitles, "Del", delSub, delVideo, 100, false)); err != nil {
		t.Fatalf("SaveDownload(del): %v", err)
	}

	// Survivor: video + subtitle remain present throughout.
	keepVideo := mkfile(t, filepath.Join(dir, "keep.mkv"))
	keepSub := mkfile(t, filepath.Join(dir, "keep.en.srt"))
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttKeep", "en",
		api.ProviderNameOpenSubtitles, "Keep", keepSub, keepVideo, 100, false)); err != nil {
		t.Fatalf("SaveDownload(keep): %v", err)
	}
	seedAttempt(t, db, api.MediaTypeMovie, "ttKeep", "en", api.ProviderNameGestdown)
	seedFile(t, db, api.MediaTypeMovie, "ttKeep", "en", keepSub)
	seedScan(t, db, api.MediaTypeMovie, "ttKeep")

	rmfile(t, delVideo)

	if _, err := db.ReconcileState(ctx); err != nil {
		t.Fatalf("ReconcileState: %v", err)
	}

	// Doomed item gone.
	if rows := readTripleRows(t, db, api.MediaTypeMovie, "ttDel", "fr"); len(rows) != 0 {
		t.Errorf("ttDel rows = %d, want 0", len(rows))
	}
	// Survivor fully intact.
	if rows := readTripleRows(t, db, api.MediaTypeMovie, "ttKeep", "en"); len(rows) != 1 {
		t.Errorf("ttKeep rows = %d, want 1 (untouched)", len(rows))
	}
	assertBackoffConsistent(t, db, 1) // only the survivor's backoff remains
	if total, _ := db.TotalSubtitleFiles(ctx); total != 1 {
		t.Errorf("TotalSubtitleFiles = %d, want 1 (survivor coverage kept)", total)
	}
	states, err := db.GetScanStates(ctx, api.MediaTypeMovie, "ttKeep")
	if err != nil {
		t.Fatalf("GetScanStates: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("GetScanStates(ttKeep) = %d, want 1", len(states))
	}
	assertDownloadsConsistent(t, db)
}

// TestReconcileState_idempotent asserts a second reconcile pass over a store
// containing all three branches converges to the same state as one pass
// (run-twice == run-once, Requirement 7.5): deleted rows stay gone, the reset
// auto row is NOT reset again (its bumped media_imported is stable), and the
// second pass reports no further work.
func TestReconcileState_idempotent(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	dir := t.TempDir()

	// Triple A: video gone (delete branch).
	videoGone := mkfile(t, filepath.Join(dir, "gone.mkv"))
	subGone := mkfile(t, filepath.Join(dir, "gone.fr.srt"))
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttGone", "fr",
		api.ProviderNameOpenSubtitles, "Gone", subGone, videoGone, 100, false)); err != nil {
		t.Fatalf("SaveDownload(gone): %v", err)
	}

	// Triple B: all subs gone (reset branch), video present.
	videoReset := mkfile(t, filepath.Join(dir, "reset.mkv"))
	subReset := mkfile(t, filepath.Join(dir, "reset.fr.srt"))
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttReset", "fr",
		api.ProviderNameOpenSubtitles, "Reset", subReset, videoReset, 100, false)); err != nil {
		t.Fatalf("SaveDownload(reset): %v", err)
	}

	// Triple C: sibling present (partial-delete branch).
	videoSib := mkfile(t, filepath.Join(dir, "sib.mkv"))
	sibAuto := mkfile(t, filepath.Join(dir, "sib.fr.srt"))
	sibManual := mkfile(t, filepath.Join(dir, "sib.fr.1.srt"))
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttSib", "fr",
		api.ProviderNameOpenSubtitles, "SibAuto", sibAuto, videoSib, 80, false)); err != nil {
		t.Fatalf("SaveDownload(sib auto): %v", err)
	}
	if err := db.SaveDownload(ctx, dlRec(api.MediaTypeMovie, "ttSib", "fr",
		api.ProviderNameSubDL, "SibManual", sibManual, videoSib, 50, true)); err != nil {
		t.Fatalf("SaveDownload(sib manual): %v", err)
	}

	// Apply the filesystem changes that drive each branch.
	rmfile(t, videoGone)
	rmfile(t, subReset)
	rmfile(t, sibAuto)
	time.Sleep(10 * time.Millisecond)

	first, err := db.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState(first): %v", err)
	}
	if first.ResetCount != 1 {
		t.Errorf("first ResetCount = %d, want 1", first.ResetCount)
	}
	if len(first.Deleted.Paths) != 1 {
		t.Errorf("first Deleted.Paths = %d, want 1", len(first.Deleted.Paths))
	}

	// Snapshot the post-first-pass state.
	gone1 := readTripleRows(t, db, api.MediaTypeMovie, "ttGone", "fr")
	reset1 := readTripleRows(t, db, api.MediaTypeMovie, "ttReset", "fr")
	sib1 := readTripleRows(t, db, api.MediaTypeMovie, "ttSib", "fr")
	if len(gone1) != 0 || len(reset1) != 1 || len(sib1) != 1 {
		t.Fatalf("post-first state: gone=%d reset=%d sib=%d, want 0/1/1",
			len(gone1), len(reset1), len(sib1))
	}
	resetImported := reset1[0].MediaImported

	// Second pass must be a no-op: nothing deleted, nothing reset.
	second, err := db.ReconcileState(ctx)
	if err != nil {
		t.Fatalf("ReconcileState(second): %v", err)
	}
	if len(second.Deleted.Paths) != 0 {
		t.Errorf("second Deleted.Paths = %d, want 0 (idempotent)", len(second.Deleted.Paths))
	}
	if second.ResetCount != 0 {
		t.Errorf("second ResetCount = %d, want 0 (idempotent)", second.ResetCount)
	}

	// State after the second pass equals the state after the first.
	gone2 := readTripleRows(t, db, api.MediaTypeMovie, "ttGone", "fr")
	reset2 := readTripleRows(t, db, api.MediaTypeMovie, "ttReset", "fr")
	sib2 := readTripleRows(t, db, api.MediaTypeMovie, "ttSib", "fr")
	if len(gone2) != 0 || len(reset2) != 1 || len(sib2) != 1 {
		t.Fatalf("post-second state: gone=%d reset=%d sib=%d, want 0/1/1 (converged)",
			len(gone2), len(reset2), len(sib2))
	}
	// The reset auto row must NOT be reset a second time (media_imported stable).
	if !reset2[0].MediaImported.Equal(resetImported) {
		t.Errorf("reset row MediaImported changed on re-run: %v -> %v (not idempotent)",
			resetImported, reset2[0].MediaImported)
	}
	if reset2[0].Path != "" {
		t.Errorf("reset row Path = %q after re-run, want empty", reset2[0].Path)
	}
	assertDownloadsConsistent(t, db)
}
