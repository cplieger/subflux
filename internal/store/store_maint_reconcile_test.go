package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func TestReconcileState_removes_missing_files(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	realVideo := filepath.Join(dir, "exists.mkv")
	if err := os.WriteFile(realVideo, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	realSub := filepath.Join(dir, "exists.fr.srt")
	if err := os.WriteFile(realSub, []byte("sub"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Exists", Path: realSub,
		Score: 100, Meta: &api.DownloadMeta{VideoPath: realVideo},
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	missingSub := filepath.Join(dir, "missing.fr.srt")
	if err := os.WriteFile(missingSub, []byte("sub"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt222",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Missing", Path: missingSub,
		Score: 100, Meta: &api.DownloadMeta{VideoPath: filepath.Join(dir, "missing.mkv")},
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	if err := db.RecordNoResult(context.Background(), "movie", "tt222", "fr", "os",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult() unexpected error: %v", err)
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}
	if len(result.Deleted.Paths) != 1 {
		t.Errorf("ReconcileState().Deleted.Paths = %d, want 1", len(result.Deleted.Paths))
	}
	if len(result.Deleted.Paths) > 0 && result.Deleted.Paths[0] != missingSub {
		t.Errorf("ReconcileState().Deleted.Paths[0] = %q, want %q",
			result.Deleted.Paths[0], missingSub)
	}

	downloads, attempts, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d after reconcile, want 1", downloads)
	}

	if attempts != 0 {
		t.Errorf("Stats().attempts = %d after reconcile, want 0", attempts)
	}
}

func TestReconcileState_empty_path_skipped(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	_, err := db.db.ExecContext(t.Context(), `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual)
		VALUES ('movie', 'tt111', 'fr', 'os', 'Test', 100, '', 0)`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}
	if len(result.Deleted.Paths) != 0 || result.ResetCount != 0 {
		t.Errorf("ReconcileState() deleted=%d reset=%d, want 0/0 (empty video_path skipped)",
			len(result.Deleted.Paths), result.ResetCount)
	}
}

func TestReconcileState_no_records(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}
	if len(result.Deleted.Paths) != 0 || result.ResetCount != 0 {
		t.Errorf("ReconcileState() deleted=%d reset=%d, want 0/0",
			len(result.Deleted.Paths), result.ResetCount)
	}
}

func TestReconcileState_keeps_existing_files(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	video1 := filepath.Join(dir, "exists1.mkv")
	video2 := filepath.Join(dir, "exists2.mkv")
	sub1 := filepath.Join(dir, "exists1.fr.srt")
	sub2 := filepath.Join(dir, "exists2.fr.srt")
	for _, f := range []string{video1, video2, sub1, sub2} {
		if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
			t.Fatalf("WriteFile(%s) unexpected error: %v", f, err)
		}
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie1", Path: sub1,
		Score: 100, Meta: &api.DownloadMeta{VideoPath: video1},
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt222",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie2", Path: sub2,
		Score: 100, Meta: &api.DownloadMeta{VideoPath: video2},
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}
	if len(result.Deleted.Paths) != 0 || result.ResetCount != 0 {
		t.Errorf("ReconcileState() deleted=%d reset=%d, want 0/0 (both files exist)",
			len(result.Deleted.Paths), result.ResetCount)
	}
}

func TestReconcileState_multiple_missing_files(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	realVideo := filepath.Join(dir, "exists.mkv")
	realSub := filepath.Join(dir, "exists.fr.srt")
	for _, f := range []string{realVideo, realSub} {
		if err := os.WriteFile(f, []byte("data"), 0o644); err != nil {
			t.Fatalf("WriteFile() unexpected error: %v", err)
		}
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Exists", Path: realSub,
		Score: 100, Meta: &api.DownloadMeta{VideoPath: realVideo},
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt222",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Missing1", Path: filepath.Join(dir, "gone1.fr.srt"),
		Score: 100, Meta: &api.DownloadMeta{VideoPath: filepath.Join(dir, "gone1.mkv")},
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt333",
		Language: "en", ProviderName: "os",
		ReleaseName: "Missing2", Path: filepath.Join(dir, "gone2.en.srt"),
		Score: 100, Meta: &api.DownloadMeta{VideoPath: filepath.Join(dir, "gone2.mkv")},
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}
	if len(result.Deleted.Paths) != 2 {
		t.Errorf("ReconcileState().Deleted.Paths = %d, want 2", len(result.Deleted.Paths))
	}

	downloads, _, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d after reconcile, want 1", downloads)
	}
}

func TestReconcileState_resets_missing_subtitle(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile() unexpected error: %v", err)
	}

	missingSub := filepath.Join(dir, "movie.fr.srt")
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: missingSub,
		Score: 150, Meta: &api.DownloadMeta{VideoPath: video},
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	if err := db.RecordNoResult(context.Background(), "movie", "tt111", "fr", "os",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult() unexpected error: %v", err)
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}
	if result.ResetCount != 1 {
		t.Errorf("ReconcileState().ResetCount = %d, want 1", result.ResetCount)
	}
	if len(result.Deleted.Paths) != 0 {
		t.Errorf("ReconcileState().Deleted.Paths = %d, want 0 (reset, not deleted)",
			len(result.Deleted.Paths))
	}

	downloads, attempts, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d, want 1 (reset, not deleted)", downloads)
	}

	if attempts != 0 {
		t.Errorf("Stats().attempts = %d, want 0 (cleared for re-search)", attempts)
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState() unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("GetState() returned %d entries, want 1", len(entries))
	}
	if entries[0].Path != "" {
		t.Errorf("reset entry Path = %q, want empty", entries[0].Path)
	}
	if entries[0].Score != 0 {
		t.Errorf("reset entry Score = %d, want 0", entries[0].Score)
	}
}

func TestReconcileState_partial_subtitle_missing_preserves_lock(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile(video): %v", err)
	}

	presentSub := filepath.Join(dir, "movie.fr.srt")
	if err := os.WriteFile(presentSub, []byte("sub"), 0o644); err != nil {
		t.Fatalf("WriteFile(presentSub): %v", err)
	}

	missingSub := filepath.Join(dir, "movie.fr.v2.srt")

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score,
		 path, manual, video_path)
		VALUES ('movie', 'tt111', 'fr', 'os', 'Present-Sub', 100,
		        ?, 1, ?)`, presentSub, video)
	if err != nil {
		t.Fatalf("insert present: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score,
		 path, manual, video_path)
		VALUES ('movie', 'tt111', 'fr', 'os', 'Missing-Sub', 80,
		        ?, 1, ?)`, missingSub, video)
	if err != nil {
		t.Fatalf("insert missing: %v", err)
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}

	if len(result.Deleted.Paths) != 0 {
		t.Errorf("ReconcileState().Deleted.Paths = %d, want 0", len(result.Deleted.Paths))
	}

	if result.ResetCount != 0 {
		t.Errorf("ReconcileState().ResetCount = %d, want 0", result.ResetCount)
	}

	downloads, _, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d, want 1 (missing row deleted, present preserved)",
			downloads)
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState(): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("GetState() returned %d entries, want 1", len(entries))
	}
	if entries[0].ReleaseName != "Present-Sub" {
		t.Errorf("remaining entry ReleaseName = %q, want %q",
			entries[0].ReleaseName, "Present-Sub")
	}
}

func TestReconcileState_all_subs_missing_deletes_manual_resets_auto(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile(video): %v", err)
	}

	autoSub := filepath.Join(dir, "movie.fr.auto.srt")
	manualSub := filepath.Join(dir, "movie.fr.manual.srt")

	ctx := t.Context()

	_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score,
		 path, manual, video_path)
		VALUES ('movie', 'tt111', 'fr', 'os', 'Auto-Sub', 100,
		        ?, 0, ?)`, autoSub, video)
	if err != nil {
		t.Fatalf("insert auto: %v", err)
	}

	_, err = db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score,
		 path, manual, video_path)
		VALUES ('movie', 'tt111', 'fr', 'os', 'Manual-Sub', 200,
		        ?, 1, ?)`, manualSub, video)
	if err != nil {
		t.Fatalf("insert manual: %v", err)
	}

	if err := db.RecordNoResult(context.Background(), "movie", "tt111", "fr", "os",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult(): %v", err)
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}

	if result.ResetCount != 1 {
		t.Errorf("ReconcileState().ResetCount = %d, want 1", result.ResetCount)
	}

	downloads, attempts, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d, want 1 (manual deleted, auto reset)",
			downloads)
	}
	if attempts != 0 {
		t.Errorf("Stats().attempts = %d, want 0 (cleared for re-search)", attempts)
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState(): %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("GetState() returned %d entries, want 1", len(entries))
	}
	if entries[0].Manual {
		t.Error("remaining entry Manual = true, want false (manual should be deleted)")
	}
	if entries[0].Path != "" {
		t.Errorf("remaining entry Path = %q, want empty (reset for re-search)",
			entries[0].Path)
	}
	if entries[0].Score != 0 {
		t.Errorf("remaining entry Score = %d, want 0 (reset for re-search)",
			entries[0].Score)
	}
}

func TestReconcileState_empty_sub_path_with_existing_video_skipped(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score,
		 path, manual, video_path)
		VALUES ('movie', 'tt111', 'fr', 'os', 'Test', 100,
		        '', 0, ?)`, video)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}

	if len(result.Deleted.Paths) != 0 {
		t.Errorf("Deleted.Paths = %d, want 0", len(result.Deleted.Paths))
	}
	if result.ResetCount != 0 {
		t.Errorf("ResetCount = %d, want 0", result.ResetCount)
	}

	downloads, _, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d, want 1 (row preserved)", downloads)
	}
}

func TestReconcileState_video_stat_error_skips_entry(t *testing.T) {
	t.Parallel()
	if os.Getuid() == 0 {
		t.Skip("chmod semantics not enforced for root")
	}
	db := openTestDB(t)
	dir := t.TempDir()

	restrictedDir := filepath.Join(dir, "restricted")
	if err := os.Mkdir(restrictedDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}

	video := filepath.Join(restrictedDir, "movie.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: filepath.Join(restrictedDir, "movie.fr.srt"),
		Score: 100, Meta: &api.DownloadMeta{VideoPath: video},
	}); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	if err := os.Chmod(restrictedDir, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(restrictedDir, 0o755) })

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}

	if len(result.Deleted.Paths) != 0 {
		t.Errorf("Deleted.Paths = %d, want 0 (stat error → skip)", len(result.Deleted.Paths))
	}
	if result.ResetCount != 0 {
		t.Errorf("ResetCount = %d, want 0 (stat error → skip)", result.ResetCount)
	}

	downloads, _, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d, want 1 (entry preserved)", downloads)
	}
}

func TestReconcileState_subtitle_stat_error_assumes_present(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile(video): %v", err)
	}

	restrictedDir := filepath.Join(dir, "restricted")
	if err := os.Mkdir(restrictedDir, 0o755); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	sub := filepath.Join(restrictedDir, "movie.fr.srt")
	if err := os.WriteFile(sub, []byte("sub"), 0o644); err != nil {
		t.Fatalf("WriteFile(sub): %v", err)
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: sub,
		Score: 100, Meta: &api.DownloadMeta{VideoPath: video},
	}); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	if err := os.Chmod(restrictedDir, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(restrictedDir, 0o755) })

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}

	if len(result.Deleted.Paths) != 0 {
		t.Errorf("Deleted.Paths = %d, want 0", len(result.Deleted.Paths))
	}
	if result.ResetCount != 0 {
		t.Errorf("ResetCount = %d, want 0", result.ResetCount)
	}

	downloads, _, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d, want 1 (entry preserved)", downloads)
	}
}

func TestReconcileState_multiple_auto_rows_all_reset(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	dir := t.TempDir()

	video := filepath.Join(dir, "movie.mkv")
	if err := os.WriteFile(video, []byte("video"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	ctx := t.Context()
	for i, sub := range []string{"movie.fr.v1.srt", "movie.fr.v2.srt"} {
		_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
			(media_type, media_id, language, provider, release_name, score,
			 path, manual, video_path)
			VALUES ('movie', 'tt111', 'fr', 'os', ?, ?,
			        ?, 0, ?)`,
			"Release-"+sub, 100+i,
			filepath.Join(dir, sub), video)
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	result, err := db.ReconcileState(context.Background())
	if err != nil {
		t.Fatalf("ReconcileState() unexpected error: %v", err)
	}

	if result.ResetCount != 1 {
		t.Errorf("ResetCount = %d, want 1 (one group reset)", result.ResetCount)
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState(): %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("GetState() returned %d entries, want 2", len(entries))
	}
	for i, e := range entries {
		if e.Path != "" {
			t.Errorf("entries[%d].Path = %q, want empty (reset)", i, e.Path)
		}
		if e.Score != 0 {
			t.Errorf("entries[%d].Score = %d, want 0 (reset)", i, e.Score)
		}
	}
}
