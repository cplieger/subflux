package store

import (
	"context"
	"testing"

	"subflux/internal/api"
)

func TestIsManuallyLocked(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if locked, _ := db.IsManuallyLocked(context.Background(), "movie", "tt123", "fr"); locked {
		t.Error("IsManuallyLocked() = true before any manual download")
	}

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	if locked, _ := db.IsManuallyLocked(context.Background(), "movie", "tt123", "fr"); !locked {
		t.Error("IsManuallyLocked() = false after manual download")
	}
}

func TestIsManuallyLocked_count_boundary(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if locked, _ := db.IsManuallyLocked(context.Background(), "movie", "tt123", "fr"); locked {
		t.Error("IsManuallyLocked() = true with no manual downloads")
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload(auto) unexpected error: %v", err)
	}
	if locked, _ := db.IsManuallyLocked(context.Background(), "movie", "tt123", "fr"); locked {
		t.Error("IsManuallyLocked() = true with only auto download")
	}

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-Manual", Path: "/path/manual.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload(manual) unexpected error: %v", err)
	}
	if locked, _ := db.IsManuallyLocked(context.Background(), "movie", "tt123", "fr"); !locked {
		t.Error("IsManuallyLocked() = false with manual download")
	}
}

func TestClearManualLock(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	if err := db.ClearManualLock(context.Background(), "movie", "tt123", "fr"); err != nil {
		t.Fatalf("ClearManualLock() unexpected error: %v", err)
	}

	if locked, _ := db.IsManuallyLocked(context.Background(), "movie", "tt123", "fr"); locked {
		t.Error("IsManuallyLocked() = true after ClearManualLock")
	}
}

func TestClearManualLock_no_manual_records_is_noop(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/path/movie.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	if err := db.ClearManualLock(context.Background(), "movie", "tt123", "fr"); err != nil {
		t.Fatalf("ClearManualLock() unexpected error: %v", err)
	}

	downloads, _, _ := db.Stats(context.Background())
	if downloads != 1 {
		t.Errorf("Stats().downloads = %d, want 1", downloads)
	}
}

func TestManualDownloadCount(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if got, _ := db.ManualDownloadCount(context.Background(), "movie", "tt123", "fr"); got != 0 {
		t.Errorf("ManualDownloadCount() = %d, want 0", got)
	}

	meta := &api.DownloadMeta{Manual: true}
	for range 3 {
		if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
			MediaType: "movie", MediaID: "tt123",
			Language: "fr", ProviderName: "os",
			ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt",
			Score: 100, Meta: meta,
		}); err != nil {
			t.Fatalf("SaveDownload() unexpected error: %v", err)
		}
	}

	if got, _ := db.ManualDownloadCount(context.Background(), "movie", "tt123", "fr"); got != 3 {
		t.Errorf("ManualDownloadCount() = %d, want 3", got)
	}
}

func TestGetManualLocks(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/path/movie.fr.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	locks, err := db.GetManualLocks(context.Background())
	if err != nil {
		t.Fatalf("GetManualLocks() unexpected error: %v", err)
	}
	if len(locks) != 1 {
		t.Fatalf("GetManualLocks() returned %d locks, want 1", len(locks))
	}
	if locks[0].MediaID != "tt123" {
		t.Errorf("locks[0].MediaID = %q, want %q", locks[0].MediaID, "tt123")
	}
}

func TestGetManualLocks_multiple_with_counts(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	meta := &api.DownloadMeta{Manual: true}

	for range 2 {
		if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
			MediaType: "movie", MediaID: "tt111",
			Language: "fr", ProviderName: "os",
			ReleaseName: "M1", Path: "/p/1.srt",
			Score: 100, Meta: meta,
		}); err != nil {
			t.Fatalf("SaveDownload() unexpected error: %v", err)
		}
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt222",
		Language: "en", ProviderName: "os",
		ReleaseName: "M2", Path: "/p/2.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload() unexpected error: %v", err)
	}

	locks, err := db.GetManualLocks(context.Background())
	if err != nil {
		t.Fatalf("GetManualLocks() unexpected error: %v", err)
	}
	if len(locks) != 2 {
		t.Fatalf("GetManualLocks() returned %d locks, want 2", len(locks))
	}

	if locks[0].MediaID != "tt111" || locks[0].Count != 2 {
		t.Errorf("locks[0] = {MediaID:%q, Count:%d}, want {tt111, 2}",
			locks[0].MediaID, locks[0].Count)
	}
	if locks[1].MediaID != "tt222" || locks[1].Count != 1 {
		t.Errorf("locks[1] = {MediaID:%q, Count:%d}, want {tt222, 1}",
			locks[1].MediaID, locks[1].Count)
	}
}

func TestGetManualLocks_empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	locks, err := db.GetManualLocks(context.Background())
	if err != nil {
		t.Fatalf("GetManualLocks() unexpected error: %v", err)
	}
	if len(locks) != 0 {
		t.Errorf("GetManualLocks() returned %d locks, want 0", len(locks))
	}
}

func TestGetManualLocks_ordered_by_media_type_then_id(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt222",
		Language: "fr", ProviderName: "os",
		ReleaseName: "M2", Path: "/p/2.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload(movie/tt222): %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "episode", MediaID: "tt111",
		Language: "en", ProviderName: "os",
		ReleaseName: "E1", Path: "/p/1.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload(episode/tt111): %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt111",
		Language: "fr", ProviderName: "os",
		ReleaseName: "M1", Path: "/p/3.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload(movie/tt111): %v", err)
	}

	locks, err := db.GetManualLocks(context.Background())
	if err != nil {
		t.Fatalf("GetManualLocks() unexpected error: %v", err)
	}
	if len(locks) != 3 {
		t.Fatalf("GetManualLocks() returned %d locks, want 3", len(locks))
	}

	if locks[0].MediaType != "episode" {
		t.Errorf("locks[0].MediaType = %q, want %q", locks[0].MediaType, "episode")
	}
	if locks[1].MediaType != "movie" || locks[1].MediaID != "tt111" {
		t.Errorf("locks[1] = {%q, %q}, want {movie, tt111}",
			locks[1].MediaType, locks[1].MediaID)
	}
	if locks[2].MediaType != "movie" || locks[2].MediaID != "tt222" {
		t.Errorf("locks[2] = {%q, %q}, want {movie, tt222}",
			locks[2].MediaType, locks[2].MediaID)
	}
}

func TestNextManualNumber(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		mediaType api.MediaType
		mediaID   string
		lang      string
		seedSQL   []string
		want      int
	}{
		{
			name:      "no manual downloads returns one",
			seedSQL:   nil,
			mediaType: "movie",
			mediaID:   "tt123",
			lang:      "fr",
			want:      1,
		},
		{
			name: "increments from existing",
			seedSQL: []string{
				`INSERT INTO subtitle_state (media_type, media_id, language, provider, release_name, score, path, manual, video_path) VALUES ('movie', 'tt123', 'fr', 'os', 'Sub1', 100, '/media/movie.fr.1', 1, '/media/movie.mkv')`,
				`INSERT INTO subtitle_state (media_type, media_id, language, provider, release_name, score, path, manual, video_path) VALUES ('movie', 'tt123', 'fr', 'os', 'Sub2', 100, '/media/movie.fr.2', 1, '/media/movie.mkv')`,
			},
			mediaType: "movie",
			mediaID:   "tt123",
			lang:      "fr",
			want:      3,
		},
		{
			name: "handles gap in numbering",
			seedSQL: []string{
				`INSERT INTO subtitle_state (media_type, media_id, language, provider, release_name, score, path, manual, video_path) VALUES ('movie', 'tt123', 'fr', 'os', 'Sub', 100, '/media/movie.fr.1', 1, '/media/movie.mkv')`,
				`INSERT INTO subtitle_state (media_type, media_id, language, provider, release_name, score, path, manual, video_path) VALUES ('movie', 'tt123', 'fr', 'os', 'Sub', 100, '/media/movie.fr.5', 1, '/media/movie.mkv')`,
			},
			mediaType: "movie",
			mediaID:   "tt123",
			lang:      "fr",
			want:      6,
		},
		{
			name: "different language independent",
			seedSQL: []string{
				`INSERT INTO subtitle_state (media_type, media_id, language, provider, release_name, score, path, manual, video_path) VALUES ('movie', 'tt123', 'en', 'os', 'Sub', 100, '/media/movie.en.3', 1, '/media/movie.mkv')`,
			},
			mediaType: "movie",
			mediaID:   "tt123",
			lang:      "fr",
			want:      1,
		},
		{
			name: "fallback on non matching paths",
			seedSQL: []string{
				`INSERT INTO subtitle_state (media_type, media_id, language, provider, release_name, score, path, manual, video_path) VALUES ('movie', 'tt123', 'fr', 'os', 'Sub', 100, '/media/movie.srt', 1, '/media/movie.mkv')`,
			},
			mediaType: "movie",
			mediaID:   "tt123",
			lang:      "fr",
			want:      1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			ctx := t.Context()
			for _, sql := range tc.seedSQL {
				if _, err := db.db.ExecContext(ctx, sql); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			got := db.NextManualNumber(ctx, tc.mediaType, tc.mediaID, tc.lang)
			if got != tc.want {
				t.Errorf("NextManualNumber() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestManualSubtitlePaths_no_manual_downloads(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	paths, _ := db.ManualSubtitlePaths(context.Background(), "movie", "tt123", "fr")
	if len(paths) != 0 {
		t.Errorf("ManualSubtitlePaths() = %v, want empty", paths)
	}
}

func TestManualSubtitlePaths_returns_manual_paths(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Sub1", Path: "/media/movie.fr.1.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload(1): %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Sub2", Path: "/media/movie.fr.2.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload(2): %v", err)
	}

	paths, _ := db.ManualSubtitlePaths(context.Background(), "movie", "tt123", "fr")
	if len(paths) != 2 {
		t.Fatalf("ManualSubtitlePaths() returned %d paths, want 2", len(paths))
	}
}

func TestManualSubtitlePaths_excludes_empty_paths(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()

	_, err := db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score,
		 path, manual, video_path)
		VALUES ('movie', 'tt123', 'fr', 'os', 'Sub', 100,
		        '', 1, '/media/movie.mkv')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	_, err = db.db.ExecContext(ctx, `INSERT INTO subtitle_state
		(media_type, media_id, language, provider, release_name, score,
		 path, manual, video_path)
		VALUES ('movie', 'tt123', 'fr', 'os', 'Sub2', 100,
		        '/media/movie.fr.srt', 1, '/media/movie.mkv')`)
	if err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	paths, _ := db.ManualSubtitlePaths(context.Background(), "movie", "tt123", "fr")
	if len(paths) != 1 {
		t.Fatalf("ManualSubtitlePaths() returned %d paths, want 1 (empty excluded)", len(paths))
	}
	if paths[0] != "/media/movie.fr.srt" {
		t.Errorf("ManualSubtitlePaths()[0] = %q, want %q", paths[0], "/media/movie.fr.srt")
	}
}

func TestManualSubtitlePaths_excludes_auto_downloads(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Auto", Path: "/media/auto.srt",
		Score: 100, Meta: nil,
	}); err != nil {
		t.Fatalf("SaveDownload(auto): %v", err)
	}

	meta := &api.DownloadMeta{Manual: true}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Manual", Path: "/media/manual.srt",
		Score: 100, Meta: meta,
	}); err != nil {
		t.Fatalf("SaveDownload(manual): %v", err)
	}

	paths, _ := db.ManualSubtitlePaths(context.Background(), "movie", "tt123", "fr")
	if len(paths) != 1 {
		t.Fatalf("ManualSubtitlePaths() returned %d paths, want 1 (auto excluded)", len(paths))
	}
	if paths[0] != "/media/manual.srt" {
		t.Errorf("ManualSubtitlePaths()[0] = %q, want %q", paths[0], "/media/manual.srt")
	}
}
