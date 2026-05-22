package store

import (
	"context"
	"testing"
	"time"

	"subflux/internal/api"
)

func TestGetState_filters_all_combinations(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-FR", Path: "/path/fr.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "episode", MediaID: "tt222-s01e01",
		Language: "en", ProviderName: "yify",
		ReleaseName: "Show-EN", Path: "/path/en.srt",
		Score: 200, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt333",
		Language: "en", ProviderName: "os",
		ReleaseName: "Movie-EN", Path: "/path/en2.srt",
		Score: 150, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{MediaType: "movie"})
	if err != nil {
		t.Fatalf("GetState(movie) unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("GetState(movie) returned %d entries, want 2", len(entries))
	}

	entries, err = db.GetState(context.Background(), &api.StateQuery{Language: "en"})
	if err != nil {
		t.Fatalf("GetState(en) unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("GetState(lang=en) returned %d entries, want 2", len(entries))
	}

	entries, err = db.GetState(context.Background(), &api.StateQuery{Provider: "yify"})
	if err != nil {
		t.Fatalf("GetState(yify) unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("GetState(prov=yify) returned %d entries, want 1", len(entries))
	}

	entries, err = db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState(all) unexpected error: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("GetState(all) returned %d entries, want 3", len(entries))
	}
}

func TestGetState_all_filters_combined(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	records := []struct {
		mediaType                          api.MediaType
		mediaID, lang, prov, release, path string
	}{
		{"movie", "tt111", "fr", "os", "M1", "/p/1.srt"},
		{"movie", "tt222", "fr", "yify", "M2", "/p/2.srt"},
		{"movie", "tt333", "en", "os", "M3", "/p/3.srt"},
		{"episode", "tt444", "fr", "os", "E1", "/p/4.srt"},
	}
	for _, r := range records {
		if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
			MediaType: r.mediaType, MediaID: r.mediaID,
			Language: r.lang, ProviderName: api.ProviderID(r.prov),
			ReleaseName: r.release, Path: r.path,
			Score: 100, Meta: nil,
		}); err != nil {
			t.Fatalf("SaveDownload(%q) unexpected error: %v", r.release, err)
		}
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{MediaType: "movie", Language: "fr", Provider: "os"})
	if err != nil {
		t.Fatalf("GetState(movie,fr,os) unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("GetState(movie,fr,os) returned %d entries, want 1", len(entries))
	}
	if len(entries) == 1 && entries[0].MediaID != "tt111" {
		t.Errorf("GetState(movie,fr,os)[0].MediaID = %q, want %q", entries[0].MediaID, "tt111")
	}
}

func TestGetState_manual_flag_preserved(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload(auto) unexpected error: %v", err)
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt456",
		Language: "en", ProviderName: "os",
		ReleaseName: "Other-GRP", Path: "/path/other.en.srt",
		Score: 200, Meta: &api.DownloadMeta{Manual: true, Title: "Other"},
	}); err != nil {
		t.Fatalf("SaveDownload(manual) unexpected error: %v", err)
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState() unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("GetState() returned %d entries, want 2", len(entries))
	}

	for _, e := range entries {
		switch e.MediaID {
		case "tt123":
			if e.Manual {
				t.Errorf("entry tt123 Manual = true, want false (auto download)")
			}
		case "tt456":
			if !e.Manual {
				t.Errorf("entry tt456 Manual = false, want true (manual download)")
			}
		}
	}
}

// Kills CONDITIONALS_BOUNDARY at store.go:392 (manual == 1 → manual != 1).
// Verifies that GetState correctly maps manual=1 to Manual=true.
func TestGetState_manual_flag_mapping(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()

	_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual, media_imported)
		VALUES ('movie', 'tt555', 'fr', 'os', 'Manual-Sub', 100, '/p/manual.srt', 1, datetime('now'))`)
	if err != nil {
		t.Fatalf("insert manual: %v", err)
	}

	_, err = db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual, media_imported)
		VALUES ('movie', 'tt555', 'fr', 'os', 'Auto-Sub', 80, '/p/auto.srt', 0, datetime('now', '-1 hour'))`)
	if err != nil {
		t.Fatalf("insert auto: %v", err)
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{Limit: 10})
	if err != nil {
		t.Fatalf("GetState() unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("GetState() returned %d entries, want 2", len(entries))
	}

	if !entries[0].Manual {
		t.Error("GetState()[0].Manual = false, want true (manual=1 row)")
	}
	if entries[0].ReleaseName != "Manual-Sub" {
		t.Errorf("GetState()[0].ReleaseName = %q, want %q", entries[0].ReleaseName, "Manual-Sub")
	}

	if entries[1].Manual {
		t.Error("GetState()[1].Manual = true, want false (manual=0 row)")
	}
	if entries[1].ReleaseName != "Auto-Sub" {
		t.Errorf("GetState()[1].ReleaseName = %q, want %q", entries[1].ReleaseName, "Auto-Sub")
	}
}

func TestGetState_returns_most_recent_first(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual, media_imported)
		VALUES ('movie', 'tt-old', 'fr', 'os', 'Old', 100, '/p/old.srt', 0, datetime('now', '-2 days'))`)
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual, media_imported)
		VALUES ('movie', 'tt-new', 'fr', 'os', 'New', 200, '/p/new.srt', 0, datetime('now'))`)
	if err != nil {
		t.Fatalf("insert new: %v", err)
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState() unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("GetState() returned %d entries, want 2", len(entries))
	}
	if entries[0].MediaID != "tt-new" {
		t.Errorf("GetState()[0].MediaID = %q, want %q (most recent first)", entries[0].MediaID, "tt-new")
	}
	if entries[1].MediaID != "tt-old" {
		t.Errorf("GetState()[1].MediaID = %q, want %q", entries[1].MediaID, "tt-old")
	}
}

func TestGetState_limit_with_filter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	for i := range 5 {
		mediaID := "tt" + string(rune('A'+i))
		if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
			MediaType: "movie", MediaID: mediaID,
			Language: "fr", ProviderName: "os",
			ReleaseName: "R-" + mediaID, Path: "/p/" + mediaID + ".srt",
			Score: 100 + i, Meta: nil,
		}); err != nil {
			t.Fatalf("SaveDownload() unexpected error: %v", err)
		}
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{MediaType: "movie", Limit: 2})
	if err != nil {
		t.Fatalf("GetState(limit=2) unexpected error: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("GetState(movie, limit=2) returned %d entries, want 2", len(entries))
	}
}

// Kills CONDITIONALS_BOUNDARY at store.go:464 (limit > 0 → >= 0).
// GetState with limit=0 should return all rows (no LIMIT clause).
func TestGetState_limit_zero_returns_all(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	for i := range 5 {
		_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
			(media_type, media_id, language, provider, release_name, score, path, manual, media_imported)
			VALUES ('movie', ?, 'fr', 'os', 'Sub', 100, '/p/sub.srt', 0, datetime('now'))`,
			"tt"+string(rune('0'+i)))
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{})
	if err != nil {
		t.Fatalf("GetState(limit=0) unexpected error: %v", err)
	}
	if len(entries) != 5 {
		t.Errorf("GetState(limit=0) returned %d entries, want 5 (all rows)", len(entries))
	}
}

// GetState with limit=1 should return exactly 1 row.
func TestGetState_limit_one_returns_one(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	for i := range 3 {
		_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
			(media_type, media_id, language, provider, release_name, score, path, manual, media_imported)
			VALUES ('movie', ?, 'fr', 'os', 'Sub', 100, '/p/sub.srt', 0, datetime('now'))`,
			"tt"+string(rune('0'+i)))
		if err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	entries, err := db.GetState(context.Background(), &api.StateQuery{Limit: 1})
	if err != nil {
		t.Fatalf("GetState(limit=1) unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("GetState(limit=1) returned %d entries, want 1", len(entries))
	}
}

func TestStats_counts_both_tables(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/p/movie.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}
	if err := db.RecordNoResult(context.Background(), "movie", "tt456", "en", "yify",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult() unexpected error: %v", err)
	}

	downloads, attempts, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d, want 1", downloads)
	}
	if attempts != 1 {
		t.Errorf("Stats().attempts = %d, want 1", attempts)
	}
}

func TestCurrentScore_no_record_returns_not_found(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	score, mediaImported, found, _ := db.CurrentScore(context.Background(), "movie", "tt999", "fr")

	if found {
		t.Error("CurrentScore() found = true, want false (no records)")
	}
	if score != 0 {
		t.Errorf("CurrentScore() score = %d, want 0", score)
	}
	if !mediaImported.IsZero() {
		t.Errorf("CurrentScore() mediaImported = %v, want zero time", mediaImported)
	}
}

func TestCurrentScore_returns_best_auto_score(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-V1", Path: "/p/v1.srt",
		Score: 120, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	score, mediaImported, found, _ := db.CurrentScore(context.Background(), "movie", "tt123", "fr")

	if !found {
		t.Fatal("CurrentScore() found = false, want true")
	}
	if score != 120 {
		t.Errorf("CurrentScore() score = %d, want 120", score)
	}
	if mediaImported.IsZero() {
		t.Error("CurrentScore() media_imported is zero, want non-zero")
	}
}

func TestCurrentScore_ignores_manual_downloads(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-Manual", Path: "/p/manual.srt",
		Score: 200, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	_, _, found, _ := db.CurrentScore(context.Background(), "movie", "tt123", "fr")
	if found {
		t.Error("CurrentScore() found = true, want false (only manual downloads)")
	}
}

func TestCurrentScore_returns_highest_auto_score(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual, media_imported)
		VALUES ('movie', 'tt123', 'fr', 'os', 'Low-Score', 50, '/p/low.srt', 0, datetime('now', '-1 hour'))`)
	if err != nil {
		t.Fatalf("insert low: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual, media_imported)
		VALUES ('movie', 'tt123', 'fr', 'yify', 'High-Score', 200, '/p/high.srt', 0, datetime('now'))`)
	if err != nil {
		t.Fatalf("insert high: %v", err)
	}

	score, _, found, _ := db.CurrentScore(context.Background(), "movie", "tt123", "fr")
	if !found {
		t.Fatal("CurrentScore() found = false, want true")
	}
	if score != 200 {
		t.Errorf("CurrentScore() = %d, want 200 (highest auto score)", score)
	}
}

func TestLastScanTime_no_records_returns_empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	got, _ := db.LastScanTime(context.Background())
	if got != "" {
		t.Errorf("LastScanTime() = %q, want empty (no records)", got)
	}
}

func TestLastScanTime_returns_most_recent(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO scan_state
		(media_type, media_id, title, season, episode, audio_lang, scanned_at)
		VALUES ('episode', 'tvdb-111-s01e01', 'Show', 1, 1, 'eng', '2025-01-01 00:00:00')`)
	if err != nil {
		t.Fatalf("insert old: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO scan_state
		(media_type, media_id, title, season, episode, audio_lang, scanned_at)
		VALUES ('movie', 'tmdb-222', 'Movie', 0, 0, 'fre', '2026-06-15 12:30:00')`)
	if err != nil {
		t.Fatalf("insert new: %v", err)
	}

	got, _ := db.LastScanTime(context.Background())
	if got != "2026-06-15 12:30:00" {
		t.Errorf("LastScanTime() = %q, want %q", got, "2026-06-15 12:30:00")
	}
}

func TestLastScanTime_after_scan_returns_timestamp(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordScanState(context.Background(), &api.ScanRecord{MediaType: "movie", MediaID: "tmdb-123", Title: "Movie", AudioLang: "eng"}); err != nil {
		t.Fatalf("RecordScanState(): %v", err)
	}

	got, _ := db.LastScanTime(context.Background())
	if got == "" {
		t.Error("LastScanTime() = empty after RecordScanState, want non-empty")
	}
}

func TestTotalSubtitleFiles_empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if got, _ := db.TotalSubtitleFiles(context.Background()); got != 0 {
		t.Errorf("TotalSubtitleFiles() = %d, want 0", got)
	}
}

func TestTotalSubtitleFiles_counts_all_rows(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	files := []api.SubtitleFile{
		{Language: "en", Variant: "standard", Source: "external", Codec: "subrip", Path: "/p/en.srt"},
		{Language: "fr", Variant: "hi", Source: "embedded", Codec: "ass"},
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-111", files); err != nil {
		t.Fatalf("RecordSubtitleFiles(111): %v", err)
	}
	if _, err := db.RecordSubtitleFiles(context.Background(), "movie", "tmdb-222", files[:1]); err != nil {
		t.Fatalf("RecordSubtitleFiles(222): %v", err)
	}

	if got, _ := db.TotalSubtitleFiles(context.Background()); got != 3 {
		t.Errorf("TotalSubtitleFiles() = %d, want 3", got)
	}
}

func TestHistoryMediaIDs_empty_returns_nil(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ids, err := db.HistoryMediaIDs(context.Background(), "movie", "")
	if err != nil {
		t.Fatalf("HistoryMediaIDs() unexpected error: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("HistoryMediaIDs() = %v, want empty", ids)
	}
}

func TestHistoryMediaIDs_returns_distinct_ids(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-FR", Path: "/p/fr.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload(fr): %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "en", ProviderName: "os",
		ReleaseName: "Movie-EN", Path: "/p/en.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload(en): %v", err)
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt456",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Other-FR", Path: "/p/other.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload(tt456): %v", err)
	}

	ids, err := db.HistoryMediaIDs(context.Background(), "movie", "")
	if err != nil {
		t.Fatalf("HistoryMediaIDs() unexpected error: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("HistoryMediaIDs() returned %d ids, want 2 (distinct)", len(ids))
	}
}

func TestHistoryMediaIDs_filters_by_media_type(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie", Path: "/p/m.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload(movie): %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "episode", MediaID: "tt222-s01e01",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Episode", Path: "/p/e.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload(episode): %v", err)
	}

	ids, err := db.HistoryMediaIDs(context.Background(), "movie", "")
	if err != nil {
		t.Fatalf("HistoryMediaIDs(movie) unexpected error: %v", err)
	}
	if len(ids) != 1 {
		t.Fatalf("HistoryMediaIDs(movie) returned %d ids, want 1", len(ids))
	}
	if ids[0] != "tt111" {
		t.Errorf("HistoryMediaIDs(movie)[0] = %q, want %q", ids[0], "tt111")
	}
}

func TestHistoryMediaIDs_prefix_filter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	for _, id := range []string{"tvdb-111-s01e01", "tvdb-111-s01e02", "tvdb-222-s01e01"} {
		if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
			MediaType: "episode", MediaID: id,
			Language: "en", ProviderName: "os",
			ReleaseName: "Sub-" + id, Path: "/p/" + id + ".srt",
			Score: 100, Meta: nil,
		}); err != nil {
			t.Fatalf("SaveDownload(%q): %v", id, err)
		}
	}

	ids, err := db.HistoryMediaIDs(context.Background(), "episode", "tvdb-111-")
	if err != nil {
		t.Fatalf("HistoryMediaIDs(prefix) unexpected error: %v", err)
	}
	if len(ids) != 2 {
		t.Errorf("HistoryMediaIDs(tvdb-111-) returned %d ids, want 2", len(ids))
	}
}

func TestHistoryMediaIDs_prefix_escapes_wildcards(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	for _, id := range []string{"tt%special", "tt_special", "tt-normal"} {
		if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
			MediaType: "movie", MediaID: id,
			Language: "en", ProviderName: "os",
			ReleaseName: "Sub", Path: "/p/sub.srt",
			Score: 100, Meta: nil,
		}); err != nil {
			t.Fatalf("SaveDownload(%q): %v", id, err)
		}
	}

	ids, err := db.HistoryMediaIDs(context.Background(), "movie", "tt%")
	if err != nil {
		t.Fatalf("HistoryMediaIDs(tt%%) error: %v", err)
	}
	if len(ids) != 1 {
		t.Errorf("HistoryMediaIDs(\"tt%%\") returned %d ids, want 1 (escaped wildcard)", len(ids))
	}
}
