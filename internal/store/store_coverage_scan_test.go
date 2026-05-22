package store

import (
	"context"
	"testing"
	"time"

	"subflux/internal/api"
)

func TestRecordScanState_inserts_new_record(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: "tvdb-111-s01e01", Title: "Breaking Bad", AudioLang: "eng", Season: 1, Episode: 1})
	if err != nil {
		t.Fatalf("RecordScanState() unexpected error: %v", err)
	}

	rows, err := db.GetScanStates(context.Background(), "episode", "tvdb-111")
	if err != nil {
		t.Fatalf("GetScanStates() unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetScanStates() returned %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.MediaID != "tvdb-111-s01e01" {
		t.Errorf("MediaID = %q, want %q", r.MediaID, "tvdb-111-s01e01")
	}
	if r.Title != "Breaking Bad" {
		t.Errorf("Title = %q, want %q", r.Title, "Breaking Bad")
	}
	if r.AudioLang != "eng" {
		t.Errorf("AudioLang = %q, want %q", r.AudioLang, "eng")
	}
	if r.Season != 1 {
		t.Errorf("Season = %d, want 1", r.Season)
	}
	if r.Episode != 1 {
		t.Errorf("Episode = %d, want 1", r.Episode)
	}
	if r.ScannedAt == "" {
		t.Error("ScannedAt is empty, want non-empty timestamp")
	}
}

func TestRecordScanState_upsert_updates_existing(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "movie", MediaID: "tmdb-222", Title: "Old Title", AudioLang: "eng"}); err != nil {
		t.Fatalf("RecordScanState(initial): %v", err)
	}

	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "movie", MediaID: "tmdb-222", Title: "New Title", AudioLang: "fre"}); err != nil {
		t.Fatalf("RecordScanState(upsert): %v", err)
	}

	rows, err := db.GetScanStates(context.Background(), "movie", "")
	if err != nil {
		t.Fatalf("GetScanStates() unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("GetScanStates() returned %d rows, want 1 (upsert, not insert)", len(rows))
	}
	if rows[0].Title != "New Title" {
		t.Errorf("Title = %q, want %q (updated by upsert)", rows[0].Title, "New Title")
	}
	if rows[0].AudioLang != "fre" {
		t.Errorf("AudioLang = %q, want %q (updated by upsert)", rows[0].AudioLang, "fre")
	}
}

func TestGetScanStates_empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	rows, err := db.GetScanStates(context.Background(), "movie", "")
	if err != nil {
		t.Fatalf("GetScanStates() unexpected error: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("GetScanStates() returned %d rows, want 0", len(rows))
	}
}

func TestGetScanStates_filters_by_media_type(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: "tvdb-111-s01e01", Title: "Show", AudioLang: "eng", Season: 1, Episode: 1}); err != nil {
		t.Fatalf("RecordScanState(episode): %v", err)
	}
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "movie", MediaID: "tmdb-222", Title: "Movie", AudioLang: "fre"}); err != nil {
		t.Fatalf("RecordScanState(movie): %v", err)
	}

	rows, err := db.GetScanStates(context.Background(), "episode", "")
	if err != nil {
		t.Fatalf("GetScanStates(episode) unexpected error: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("GetScanStates(episode) returned %d rows, want 1", len(rows))
	}
	if len(rows) == 1 && rows[0].MediaID != "tvdb-111-s01e01" {
		t.Errorf("rows[0].MediaID = %q, want %q", rows[0].MediaID, "tvdb-111-s01e01")
	}
}

func TestGetScanStates_prefix_filter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: "tvdb-111-s01e01", Title: "Show A", AudioLang: "eng", Season: 1, Episode: 1}); err != nil {
		t.Fatalf("RecordScanState(s01e01): %v", err)
	}
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: "tvdb-111-s01e02", Title: "Show A", AudioLang: "eng", Season: 1, Episode: 2}); err != nil {
		t.Fatalf("RecordScanState(s01e02): %v", err)
	}
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: "tvdb-222-s01e01", Title: "Show B", AudioLang: "fre", Season: 1, Episode: 1}); err != nil {
		t.Fatalf("RecordScanState(other show): %v", err)
	}

	rows, err := db.GetScanStates(context.Background(), "episode", "tvdb-111-")
	if err != nil {
		t.Fatalf("GetScanStates(prefix) unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("GetScanStates(tvdb-111-) returned %d rows, want 2", len(rows))
	}
}

func TestGetScanStates_ordered_by_media_id(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: "tvdb-111-s01e02", Title: "Ep2", AudioLang: "eng", Season: 1, Episode: 2}); err != nil {
		t.Fatalf("RecordScanState(e02): %v", err)
	}
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: "tvdb-111-s01e01", Title: "Ep1", AudioLang: "eng", Season: 1, Episode: 1}); err != nil {
		t.Fatalf("RecordScanState(e01): %v", err)
	}

	rows, err := db.GetScanStates(context.Background(), "episode", "")
	if err != nil {
		t.Fatalf("GetScanStates() unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("GetScanStates() returned %d rows, want 2", len(rows))
	}
	if rows[0].MediaID != "tvdb-111-s01e01" {
		t.Errorf("rows[0].MediaID = %q, want %q (ordered by media_id)",
			rows[0].MediaID, "tvdb-111-s01e01")
	}
	if rows[1].MediaID != "tvdb-111-s01e02" {
		t.Errorf("rows[1].MediaID = %q, want %q",
			rows[1].MediaID, "tvdb-111-s01e02")
	}
}

func TestRecentlyScanned_empty_returns_empty_map(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	got, err := db.RecentlyScanned(context.Background(), time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("RecentlyScanned() unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("RecentlyScanned() = %v, want empty map", got)
	}
}

func TestRecentlyScanned_returns_ids_after_cutoff(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO scan_state
		(media_type, media_id, title, season, episode, audio_lang, scanned_at)
		VALUES ('movie', 'tmdb-111', 'Old Movie', 0, 0, 'eng', '2025-01-01 00:00:00')`)
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO scan_state
		(media_type, media_id, title, season, episode, audio_lang, scanned_at)
		VALUES ('movie', 'tmdb-222', 'New Movie', 0, 0, 'eng', '2026-06-15 12:00:00')`)
	if err != nil {
		t.Fatalf("insert new: %v", err)
	}

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := db.RecentlyScanned(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("RecentlyScanned() unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("RecentlyScanned() returned %d ids, want 1", len(got))
	}
	if !got["tmdb-222"] {
		t.Errorf("RecentlyScanned() missing tmdb-222, got %v", got)
	}
	if got["tmdb-111"] {
		t.Errorf("RecentlyScanned() should not include tmdb-111 (before cutoff)")
	}
}

func TestRecentlyScanned_cutoff_at_exact_timestamp(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO scan_state
		(media_type, media_id, title, season, episode, audio_lang, scanned_at)
		VALUES ('episode', 'tvdb-111-s01e01', 'Show', 1, 1, 'eng', '2026-06-15 12:00:00')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	cutoff := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	got, err := db.RecentlyScanned(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("RecentlyScanned() unexpected error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("RecentlyScanned(exact cutoff) returned %d ids, want 1", len(got))
	}
	if !got["tvdb-111-s01e01"] {
		t.Errorf("RecentlyScanned(exact cutoff) missing tvdb-111-s01e01")
	}
}

func TestRecentlyScanned_future_cutoff_returns_empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "movie", MediaID: "tmdb-123", Title: "Movie", AudioLang: "eng"}); err != nil {
		t.Fatalf("RecordScanState(): %v", err)
	}

	cutoff := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	got, err := db.RecentlyScanned(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("RecentlyScanned() unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("RecentlyScanned(future cutoff) = %v, want empty", got)
	}
}

func TestRecentlyScanned_includes_all_media_types(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "movie", MediaID: "tmdb-111", Title: "Movie", AudioLang: "eng"}); err != nil {
		t.Fatalf("RecordScanState(movie): %v", err)
	}
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "episode", MediaID: "tvdb-222-s01e01", Title: "Show", AudioLang: "eng", Season: 1, Episode: 1}); err != nil {
		t.Fatalf("RecordScanState(episode): %v", err)
	}

	cutoff := time.Now().Add(-time.Hour)
	got, err := db.RecentlyScanned(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("RecentlyScanned() unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("RecentlyScanned() returned %d ids, want 2", len(got))
	}
	if !got["tmdb-111"] {
		t.Errorf("RecentlyScanned() missing tmdb-111")
	}
	if !got["tvdb-222-s01e01"] {
		t.Errorf("RecentlyScanned() missing tvdb-222-s01e01")
	}
}

// Verifies that embedded subtitle files always get score=0 even when a
// matching auto download exists with a non-zero score. The SQL CASE WHEN
// forces score=0 for source='embedded' regardless of the JOIN result.

func TestGetSubtitleFiles_embedded_source_always_score_zero(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tmdb-789",
		Language: "en", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/p/movie.en.srt",
		Score: 200, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/movie.en.srt"},
		{Language: "en", Variant: "standard", Source: "embedded", Codec: "subrip", Path: ""},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-789", files); err != nil {
		t.Fatalf("RecordSubtitleFiles() unexpected error: %v", err)
	}

	rows, err := db.GetSubtitleFiles(context.Background(), "movie", "tmdb-789")
	if err != nil {
		t.Fatalf("GetSubtitleFiles() unexpected error: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("GetSubtitleFiles() returned %d rows, want 2", len(rows))
	}

	if rows[0].Source != "embedded" {
		t.Fatalf("rows[0].Source = %q, want %q", rows[0].Source, "embedded")
	}
	if rows[0].Score != 0 {
		t.Errorf("GetSubtitleFiles() embedded source Score = %d, want 0 (forced by CASE WHEN)",
			rows[0].Score)
	}

	if rows[1].Source != "external" {
		t.Fatalf("rows[1].Source = %q, want %q", rows[1].Source, "external")
	}
	if rows[1].Score != 200 {
		t.Errorf("GetSubtitleFiles() external source Score = %d, want 200 (from download join)",
			rows[1].Score)
	}
}

// Verifies that GetSubtitleFiles populates VideoPath from the subtitle_state
// join for external sources, and returns empty VideoPath for embedded sources.
