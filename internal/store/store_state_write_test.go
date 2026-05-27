package store

import (
	"context"
	"testing"
	"time"

	"subflux/internal/api"
)

func TestSaveDownload(t *testing.T) {
	t.Parallel()
	cases := []struct {
		assert  func(t *testing.T, db *DB)
		name    string
		records []*api.DownloadRecord
	}{
		{
			name: "clears_attempts",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "opensubtitles",
					ReleaseName: "Movie.2024.BluRay-GRP", Path: "/path/movie.fr.srt", Score: 180},
			},
			assert: func(t *testing.T, db *DB) {
				// Seed a backoff first.
				if err := db.RecordNoResult(context.Background(), "movie", "tt123", "fr", "testprov",
					api.BackoffParams{InitialDelay: time.Millisecond, MaxDelay: time.Hour, Multiplier: 2}); err != nil {
					t.Fatalf("RecordNoResult(): %v", err)
				}
				if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
					MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "opensubtitles",
					ReleaseName: "Movie.2024.BluRay-GRP", Path: "/path/movie.fr.srt", Score: 180,
				}); err != nil {
					t.Fatalf("SaveDownload(): %v", err)
				}
				backed, err := db.BackedOffProviders(context.Background(), "movie", "tt123", "fr", 5)
				if err != nil {
					t.Fatalf("BackedOffProviders(): %v", err)
				}
				if len(backed) != 0 {
					t.Errorf("BackedOffProviders() = %v after SaveDownload, want empty", backed)
				}
			},
		},
		{
			name: "with_metadata",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie.2024-GRP", Path: "/path/movie.fr.srt", Score: 200,
					Meta: &api.DownloadMeta{Title: "Test Movie", ImdbID: "tt123", ReleaseTag: "BluRay"}},
			},
			assert: func(t *testing.T, db *DB) {
				downloads, _, _ := db.Stats(context.Background())
				if downloads != 1 {
					t.Errorf("Stats().downloads = %d, want 1", downloads)
				}
			},
		},
		{
			name: "nil_meta_does_not_panic",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt", Score: 100},
			},
			assert: func(t *testing.T, db *DB) {
				if locked, _ := db.IsManuallyLocked(context.Background(), "movie", "tt123", "fr"); locked {
					t.Error("SaveDownload(nil meta) created manual lock, want auto")
				}
			},
		},
		{
			name: "manual_meta_creates_lock",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt", Score: 100,
					Meta: &api.DownloadMeta{Manual: true}},
			},
			assert: func(t *testing.T, db *DB) {
				if locked, _ := db.IsManuallyLocked(context.Background(), "movie", "tt123", "fr"); !locked {
					t.Error("SaveDownload(manual=true) did not create lock")
				}
			},
		},
		{
			name: "auto_replaces_previous_auto",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-V1", Path: "/path/v1.srt", Score: 100},
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-V2", Path: "/path/v2.srt", Score: 150},
			},
			assert: func(t *testing.T, db *DB) {
				downloads, _, _ := db.Stats(context.Background())
				if downloads != 1 {
					t.Errorf("Stats().downloads = %d after 2 auto downloads, want 1", downloads)
				}
				refs, _ := db.DownloadedRefs(context.Background(), "movie", "tt123", "fr")
				if len(refs) != 1 {
					t.Fatalf("DownloadedRefs() returned %d refs, want 1", len(refs))
				}
				if refs[0].ReleaseName != "Movie-V2" {
					t.Errorf("DownloadedRefs()[0].ReleaseName = %q, want %q", refs[0].ReleaseName, "Movie-V2")
				}
			},
		},
		{
			name: "manual_does_not_replace_previous",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-V1", Path: "/path/v1.srt", Score: 100,
					Meta: &api.DownloadMeta{Manual: true}},
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-V2", Path: "/path/v2.srt", Score: 150,
					Meta: &api.DownloadMeta{Manual: true}},
			},
			assert: func(t *testing.T, db *DB) {
				downloads, _, _ := db.Stats(context.Background())
				if downloads != 2 {
					t.Errorf("Stats().downloads = %d after 2 manual downloads, want 2", downloads)
				}
			},
		},
		{
			name: "manual_preserves_auto_records",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Auto-V1", Path: "/path/auto.srt", Score: 100},
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Manual-V1", Path: "/path/manual.srt", Score: 200,
					Meta: &api.DownloadMeta{Manual: true}},
			},
			assert: func(t *testing.T, db *DB) {
				downloads, _, _ := db.Stats(context.Background())
				if downloads != 2 {
					t.Errorf("Stats().downloads = %d, want 2 (manual preserves auto)", downloads)
				}
			},
		},
		{
			name: "metadata_fields_stored",
			records: []*api.DownloadRecord{
				{MediaType: "episode", MediaID: "tt0903747-s05e16", Language: "en", ProviderName: "os",
					ReleaseName: "Breaking.Bad.S05E16.BluRay-DEMAND", Path: "/path/bb.en.srt", Score: 250,
					Meta: &api.DownloadMeta{Title: "Breaking Bad S05E16", ImdbID: "tt0903747",
						ReleaseTag: "BluRay.x264-DEMAND", Season: 5, Episode: 16}},
			},
			assert: func(t *testing.T, db *DB) {
				entries, err := db.GetState(context.Background(), &api.StateQuery{MediaType: "episode"})
				if err != nil {
					t.Fatalf("GetState(): %v", err)
				}
				if len(entries) != 1 {
					t.Fatalf("GetState() returned %d entries, want 1", len(entries))
				}
				e := entries[0]
				if e.Title != "Breaking Bad S05E16" {
					t.Errorf("Title = %q, want %q", e.Title, "Breaking Bad S05E16")
				}
				if e.ImdbID != "tt0903747" {
					t.Errorf("ImdbID = %q, want %q", e.ImdbID, "tt0903747")
				}
				if e.Season != 5 {
					t.Errorf("Season = %d, want 5", e.Season)
				}
				if e.Episode != 16 {
					t.Errorf("Episode = %d, want 16", e.Episode)
				}
				if e.Score != 250 {
					t.Errorf("Score = %d, want 250", e.Score)
				}
				if e.Manual {
					t.Error("Manual = true, want false")
				}
			},
		},
		{
			name:    "preserves_media_imported_timestamp_on_upgrade",
			records: nil, // custom setup
			assert: func(t *testing.T, db *DB) {
				if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
					MediaType: "movie", MediaID: "tt999", Language: "fr", ProviderName: "os",
					ReleaseName: "V1", Path: "/p/v1.srt", Score: 80,
					Meta: &api.DownloadMeta{Title: "Movie", ImdbID: "tt999"},
				}); err != nil {
					t.Fatalf("SaveDownload(v1): %v", err)
				}
				ctx := t.Context()
				_, err := db.db.ExecContext(ctx,
					`UPDATE subtitle_state SET media_imported = datetime('now', '-5 days') WHERE media_id = 'tt999'`)
				if err != nil {
					t.Fatalf("backdate: %v", err)
				}
				var origTS string
				if err := db.db.QueryRowContext(ctx,
					`SELECT media_imported FROM subtitle_state WHERE media_id = 'tt999'`).Scan(&origTS); err != nil {
					t.Fatalf("read original timestamp: %v", err)
				}
				if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
					MediaType: "movie", MediaID: "tt999", Language: "fr", ProviderName: "yify",
					ReleaseName: "V2", Path: "/p/v2.srt", Score: 95,
					Meta: &api.DownloadMeta{Title: "Movie", ImdbID: "tt999"},
				}); err != nil {
					t.Fatalf("SaveDownload(v2): %v", err)
				}
				var newTS string
				if err := db.db.QueryRowContext(ctx,
					`SELECT media_imported FROM subtitle_state WHERE media_id = 'tt999'`).Scan(&newTS); err != nil {
					t.Fatalf("read new timestamp: %v", err)
				}
				if newTS != origTS {
					t.Errorf("media_imported changed: got %q, want %q", newTS, origTS)
				}
				score, _, found, _ := db.CurrentScore(context.Background(), "movie", "tt999", "fr")
				if !found {
					t.Fatal("CurrentScore: not found after upgrade")
				}
				if score != 95 {
					t.Errorf("score = %d, want 95", score)
				}
			},
		},
		{
			name: "manual_stores_video_path",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt", Score: 100,
					Meta: &api.DownloadMeta{Manual: true, Title: "Test Movie", VideoPath: "/media/movie.mkv"}},
			},
			assert: func(t *testing.T, db *DB) {
				var videoPath string
				if err := db.db.QueryRowContext(t.Context(),
					`SELECT video_path FROM subtitle_state WHERE media_id = 'tt123'`).Scan(&videoPath); err != nil {
					t.Fatalf("query video_path: %v", err)
				}
				if videoPath != "/media/movie.mkv" {
					t.Errorf("video_path = %q, want %q", videoPath, "/media/movie.mkv")
				}
			},
		},
		{
			name: "auto_stores_video_path",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt", Score: 100,
					Meta: &api.DownloadMeta{Title: "Test Movie", VideoPath: "/media/movie.mkv"}},
			},
			assert: func(t *testing.T, db *DB) {
				var videoPath string
				if err := db.db.QueryRowContext(t.Context(),
					`SELECT video_path FROM subtitle_state WHERE media_id = 'tt123'`).Scan(&videoPath); err != nil {
					t.Fatalf("query video_path: %v", err)
				}
				if videoPath != "/media/movie.mkv" {
					t.Errorf("video_path = %q, want %q", videoPath, "/media/movie.mkv")
				}
			},
		},
		{
			name: "auto_update_changes_video_path",
			records: []*api.DownloadRecord{
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "V1", Path: "/p/v1.srt", Score: 80,
					Meta: &api.DownloadMeta{VideoPath: "/media/old.mkv"}},
				{MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "yify",
					ReleaseName: "V2", Path: "/p/v2.srt", Score: 150,
					Meta: &api.DownloadMeta{VideoPath: "/media/new.mkv"}},
			},
			assert: func(t *testing.T, db *DB) {
				var videoPath string
				if err := db.db.QueryRowContext(t.Context(),
					`SELECT video_path FROM subtitle_state WHERE media_id = 'tt123'`).Scan(&videoPath); err != nil {
					t.Fatalf("query video_path: %v", err)
				}
				if videoPath != "/media/new.mkv" {
					t.Errorf("video_path after upgrade = %q, want %q", videoPath, "/media/new.mkv")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)

			// For cases with custom assert that handles its own setup.
			if tc.name == "clears_attempts" || tc.name == "preserves_media_imported_timestamp_on_upgrade" {
				tc.assert(t, db)
				return
			}

			for _, rec := range tc.records {
				if err := db.SaveDownload(context.Background(), rec); err != nil {
					t.Fatalf("SaveDownload() error: %v", err)
				}
			}
			tc.assert(t, db)
		})
	}
}

func TestDownloadedRefs_empty_for_unknown_media(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	refs, _ := db.DownloadedRefs(context.Background(), "movie", "tt123", "fr")
	if len(refs) != 0 {
		t.Errorf("DownloadedRefs() = %v, want empty", refs)
	}
}

func TestDownloadedRefs_returns_auto_row(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	refs, _ := db.DownloadedRefs(context.Background(), "movie", "tt123", "fr")
	if len(refs) != 1 {
		t.Fatalf("DownloadedRefs() returned %d refs, want 1", len(refs))
	}
	if refs[0] != (api.DownloadedRef{ReleaseName: "Movie-GRP", Provider: "os"}) {
		t.Errorf("DownloadedRefs()[0] = %+v, want Movie-GRP/os", refs[0])
	}
}

func TestDownloadedRefs_returns_every_distinct_history_pair(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := t.Context()

	// One auto + two manuals. All three should be returned.
	if err := db.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie.WEB-DL-GRP", Path: "/p/a.srt",
		Score: 100,
	}); err != nil {
		t.Fatalf("auto SaveDownload: %v", err)
	}
	if err := db.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "subdl",
		ReleaseName: "Movie.BluRay-OTHER", Path: "/p/b.srt",
		Score: 150, Meta: &api.DownloadMeta{Manual: true},
	}); err != nil {
		t.Fatalf("manual1 SaveDownload: %v", err)
	}
	if err := db.SaveDownload(ctx, &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "yify",
		ReleaseName: "Movie.x265-ENCD", Path: "/p/c.srt",
		Score: 80, Meta: &api.DownloadMeta{Manual: true},
	}); err != nil {
		t.Fatalf("manual2 SaveDownload: %v", err)
	}

	got, _ := db.DownloadedRefs(ctx, "movie", "tt123", "fr")
	want := map[api.DownloadedRef]bool{
		{ReleaseName: "Movie.WEB-DL-GRP", Provider: "os"}:      true,
		{ReleaseName: "Movie.BluRay-OTHER", Provider: "subdl"}: true,
		{ReleaseName: "Movie.x265-ENCD", Provider: "yify"}:     true,
	}
	if len(got) != len(want) {
		t.Fatalf("DownloadedRefs() returned %d refs, want %d", len(got), len(want))
	}
	for _, ref := range got {
		if !want[ref] {
			t.Errorf("unexpected ref: %+v", ref)
		}
	}
}

func TestDownloadedRefs_deduplicates(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := t.Context()

	// Two manual downloads of the same (release, provider) — should appear once.
	for range 2 {
		if err := db.SaveDownload(ctx, &api.DownloadRecord{
			MediaType: "movie", MediaID: "tt1", Language: "fr",
			ProviderName: "os", ReleaseName: "Movie-GRP",
			Path: "/p/x.srt", Score: 100,
			Meta: &api.DownloadMeta{Manual: true},
		}); err != nil {
			t.Fatalf("SaveDownload: %v", err)
		}
	}

	refs, _ := db.DownloadedRefs(ctx, "movie", "tt1", "fr")
	if len(refs) != 1 {
		t.Errorf("DownloadedRefs() returned %d refs, want 1 (deduplicated)", len(refs))
	}
}

func TestDownloadedRefs_skips_empty_release_names(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := t.Context()

	_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual)
		VALUES ('movie', 'tt1', 'fr', 'os', '', 100, '/p/x.srt', 0)`)
	if err != nil {
		t.Fatalf("insert empty release: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score, path, manual)
		VALUES ('movie', 'tt1', 'fr', 'os', NULL, 100, '/p/y.srt', 1)`)
	if err != nil {
		t.Fatalf("insert null release: %v", err)
	}

	refs, _ := db.DownloadedRefs(ctx, "movie", "tt1", "fr")
	if len(refs) != 0 {
		t.Errorf("DownloadedRefs() = %+v, want empty (both rows had no release_name)", refs)
	}
}
