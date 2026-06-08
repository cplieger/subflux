package store

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func TestDeleteStateByPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		seedDownloads []api.DownloadRecord
		seedAttempts  []struct {
			mediaType               api.MediaType
			mediaID, lang, provider string
		}
		videoPaths    []string
		wantPaths     int
		wantDownloads int
		wantAttempts  int
	}{
		{
			name:          "empty_input",
			videoPaths:    nil,
			wantPaths:     0,
			wantDownloads: 0,
			wantAttempts:  0,
		},
		{
			name:          "no_matching_records",
			videoPaths:    []string{"/nonexistent/video.mkv"},
			wantPaths:     0,
			wantDownloads: 0,
			wantAttempts:  0,
		},
		{
			name: "removes_matching_records",
			seedDownloads: []api.DownloadRecord{
				{
					MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-GRP", Path: "/p/movie.fr.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/media/movie.mkv"},
				},
			},
			seedAttempts: []struct {
				mediaType               api.MediaType
				mediaID, lang, provider string
			}{
				{"movie", "tt123", "fr", "os"},
			},
			videoPaths:    []string{"/media/movie.mkv"},
			wantPaths:     1,
			wantDownloads: 0,
			wantAttempts:  0,
		},
		{
			name: "multiple_video_paths",
			seedDownloads: []api.DownloadRecord{
				{
					MediaType: "movie", MediaID: "tt111", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie1", Path: "/p/1.fr.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/media/movie1.mkv"},
				},
				{
					MediaType: "movie", MediaID: "tt222", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie2", Path: "/p/2.fr.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/media/movie2.mkv"},
				},
				{
					MediaType: "movie", MediaID: "tt333", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie3", Path: "/p/3.fr.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/media/movie3.mkv"},
				},
			},
			videoPaths:    []string{"/media/movie1.mkv", "/media/movie3.mkv"},
			wantPaths:     2,
			wantDownloads: 1,
			wantAttempts:  0,
		},
		{
			name:          "skips_empty_subtitle_paths",
			seedDownloads: nil, // seeded via raw SQL below
			videoPaths:    []string{"/media/movie.mkv"},
			wantPaths:     0,
			wantDownloads: 0,
			wantAttempts:  0,
		},
		{
			name: "clears_attempts_for_each_affected_media",
			seedDownloads: []api.DownloadRecord{
				{
					MediaType: "movie", MediaID: "tt111", Language: "fr", ProviderName: "os",
					ReleaseName: "M1", Path: "/p/1.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/media/v1.mkv"},
				},
				{
					MediaType: "movie", MediaID: "tt222", Language: "en", ProviderName: "os",
					ReleaseName: "M2", Path: "/p/2.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/media/v2.mkv"},
				},
				{
					MediaType: "movie", MediaID: "tt333", Language: "fr", ProviderName: "os",
					ReleaseName: "M3", Path: "/p/3.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/media/v3.mkv"},
				},
			},
			seedAttempts: []struct {
				mediaType               api.MediaType
				mediaID, lang, provider string
			}{
				{"movie", "tt111", "fr", "os"},
				{"movie", "tt222", "en", "os"},
				{"movie", "tt333", "fr", "os"},
			},
			videoPaths:    []string{"/media/v1.mkv", "/media/v2.mkv"},
			wantPaths:     2,
			wantDownloads: 1,
			wantAttempts:  1,
		},
		{
			name: "preserves_unrelated_attempts",
			seedDownloads: []api.DownloadRecord{
				{
					MediaType: "movie", MediaID: "ttA", Language: "fr", ProviderName: "os",
					ReleaseName: "MA", Path: "/p/a.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/media/a.mkv"},
				},
			},
			seedAttempts: []struct {
				mediaType               api.MediaType
				mediaID, lang, provider string
			}{
				{"movie", "ttA", "fr", "os"},
				{"movie", "ttB", "en", "os"},
			},
			videoPaths:    []string{"/media/a.mkv"},
			wantPaths:     1,
			wantDownloads: 0,
			wantAttempts:  1,
		},
		{
			name: "returns_subtitle_paths",
			seedDownloads: []api.DownloadRecord{
				{
					MediaType: "movie", MediaID: "tt123", Language: "fr", ProviderName: "os",
					ReleaseName: "Movie-GRP", Path: "/subs/movie.fr.srt", Score: 100,
					Meta: &api.DownloadMeta{VideoPath: "/video/movie.mkv"},
				},
			},
			videoPaths:    []string{"/video/movie.mkv"},
			wantPaths:     1,
			wantDownloads: 0,
			wantAttempts:  0,
		},
	}

	bp := api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)

			// Special raw-SQL seed for skips_empty_subtitle_paths case.
			if tt.name == "skips_empty_subtitle_paths" {
				_, err := db.db.ExecContext(t.Context(), `INSERT INTO subtitle_state
					(media_type, media_id, language, provider, release_name, score,
					 path, manual, video_path)
					VALUES ('movie', 'tt123', 'fr', 'os', 'Test', 100,
					        '', 0, '/media/movie.mkv')`)
				if err != nil {
					t.Fatalf("insert: %v", err)
				}
			}

			for i := range tt.seedDownloads {
				if err := db.SaveDownload(context.Background(), &tt.seedDownloads[i]); err != nil {
					t.Fatalf("SaveDownload(%d): %v", i, err)
				}
			}
			for _, s := range tt.seedAttempts {
				if err := db.RecordNoResult(context.Background(), s.mediaType, s.mediaID, s.lang, api.ProviderID(s.provider), bp); err != nil {
					t.Fatalf("RecordNoResult(%s/%s): %v", s.mediaID, s.lang, err)
				}
			}

			result, err := db.DeleteStateByPaths(context.Background(), tt.videoPaths)
			if err != nil {
				t.Fatalf("DeleteStateByPaths() unexpected error: %v", err)
			}
			if len(result.Paths) != tt.wantPaths {
				t.Errorf("DeleteStateByPaths().Paths = %d, want %d", len(result.Paths), tt.wantPaths)
			}

			downloads, attempts, _ := db.Stats(context.Background())
			if downloads != tt.wantDownloads {
				t.Errorf("Stats().downloads = %d, want %d", downloads, tt.wantDownloads)
			}
			if attempts != tt.wantAttempts {
				t.Errorf("Stats().attempts = %d, want %d", attempts, tt.wantAttempts)
			}
		})
	}
}

func TestDeleteStateByPaths_cleans_orphaned_subtitle_files(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tt123",
		&api.SubtitleFile{
			Language: "fr", Variant: "standard", Source: "external",
			Codec: "subrip", Path: "/p/fr.srt",
		}); err != nil {
		t.Fatalf("UpsertSubtitleFile(): %v", err)
	}
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{
		MediaType: "movie", MediaID: "tt123",
		Title: "Test Movie",
	}); err != nil {
		t.Fatalf("RecordScanState(): %v", err)
	}

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-GRP", Path: "/p/fr.srt",
		Score: 100, Meta: &api.DownloadMeta{VideoPath: "/media/movie.mkv"},
	}); err != nil {
		t.Fatalf("SaveDownload(): %v", err)
	}

	_, err := db.DeleteStateByPaths(context.Background(), []string{"/media/movie.mkv"})
	if err != nil {
		t.Fatalf("DeleteStateByPaths(): %v", err)
	}

	total, _ := db.TotalSubtitleFiles(context.Background())
	if total != 0 {
		t.Errorf("TotalSubtitleFiles() = %d, want 0 (orphaned files cleaned)", total)
	}

	states, err := db.GetScanStates(context.Background(), "movie", "tt123")
	if err != nil {
		t.Fatalf("GetScanStates(): %v", err)
	}
	if len(states) != 0 {
		t.Errorf("GetScanStates() = %d, want 0 (orphaned scan_state cleaned)", len(states))
	}
}

func TestDeleteStateByPaths_preserves_coverage_when_state_remains(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "fr", ProviderName: "os",
		ReleaseName: "Movie-FR", Path: "/p/fr.srt",
		Score: 100, Meta: &api.DownloadMeta{VideoPath: "/media/movie.mkv"},
	}); err != nil {
		t.Fatalf("SaveDownload(fr): %v", err)
	}
	if err := db.SaveDownload(context.Background(), &api.DownloadRecord{
		MediaType: "movie", MediaID: "tt123",
		Language: "en", ProviderName: "os",
		ReleaseName: "Movie-EN", Path: "/p/en.srt",
		Score: 100, Meta: &api.DownloadMeta{VideoPath: "/media/movie2.mkv"},
	}); err != nil {
		t.Fatalf("SaveDownload(en): %v", err)
	}

	if err := db.UpsertSubtitleFile(context.Background(), "movie", "tt123",
		&api.SubtitleFile{
			Language: "fr", Variant: "standard", Source: "external",
			Codec: "subrip", Path: "/p/fr.srt",
		}); err != nil {
		t.Fatalf("UpsertSubtitleFile(): %v", err)
	}
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{
		MediaType: "movie", MediaID: "tt123",
		Title: "Test Movie",
	}); err != nil {
		t.Fatalf("RecordScanState(): %v", err)
	}

	_, err := db.DeleteStateByPaths(context.Background(), []string{"/media/movie.mkv"})
	if err != nil {
		t.Fatalf("DeleteStateByPaths(): %v", err)
	}

	total, _ := db.TotalSubtitleFiles(context.Background())
	if total != 1 {
		t.Errorf("TotalSubtitleFiles() = %d, want 1 (coverage preserved)", total)
	}
	states, err := db.GetScanStates(context.Background(), "movie", "tt123")
	if err != nil {
		t.Fatalf("GetScanStates(): %v", err)
	}
	if len(states) != 1 {
		t.Errorf("GetScanStates() = %d, want 1 (scan_state preserved)", len(states))
	}
}

func TestCleanupDrift(t *testing.T) {
	t.Parallel()
	bp := api.BackoffParams{InitialDelay: time.Minute, MaxDelay: time.Hour, Multiplier: 2}
	tests := []struct {
		name string
		seed []struct {
			mediaType               api.MediaType
			mediaID, lang, provider string
		}
		drift        api.ConfigDrift
		wantAttempts int
	}{
		{
			name:         "empty_drift_is_noop",
			seed:         nil,
			drift:        api.ConfigDrift{},
			wantAttempts: 0,
		},
		{
			name: "adaptive_disabled_clears_all",
			seed: []struct {
				mediaType               api.MediaType
				mediaID, lang, provider string
			}{
				{"episode", "id1", "en", "os"},
				{"movie", "id2", "fr", "bs"},
			},
			drift:        api.ConfigDrift{AdaptiveDisabled: true},
			wantAttempts: 0,
		},
		{
			name: "removed_language_clears_matching",
			seed: []struct {
				mediaType               api.MediaType
				mediaID, lang, provider string
			}{
				{"episode", "id1", "en", "os"},
				{"episode", "id1", "fr", "os"},
			},
			drift:        api.ConfigDrift{RemovedLanguages: []string{"fr"}},
			wantAttempts: 1,
		},
		{
			name: "removed_provider_clears_matching",
			seed: []struct {
				mediaType               api.MediaType
				mediaID, lang, provider string
			}{
				{"episode", "id1", "en", "os"},
				{"episode", "id1", "en", "bs"},
			},
			drift:        api.ConfigDrift{RemovedProviders: []api.ProviderID{"bs"}},
			wantAttempts: 1,
		},
		{
			name: "combined_languages_and_providers",
			seed: []struct {
				mediaType               api.MediaType
				mediaID, lang, provider string
			}{
				{"movie", "id1", "en", "os"},
				{"movie", "id1", "fr", "os"},
				{"movie", "id1", "en", "bs"},
			},
			drift:        api.ConfigDrift{RemovedLanguages: []string{"fr"}, RemovedProviders: []api.ProviderID{"bs"}},
			wantAttempts: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			for _, s := range tt.seed {
				if err := db.RecordNoResult(context.Background(), s.mediaType, s.mediaID, s.lang, api.ProviderID(s.provider), bp); err != nil {
					t.Fatalf("RecordNoResult(%s/%s/%s/%s): %v", s.mediaType, s.mediaID, s.lang, s.provider, err)
				}
			}
			if err := db.CleanupDrift(context.Background(), tt.drift); err != nil {
				t.Fatalf("CleanupDrift() error: %v", err)
			}
			_, attempts, _ := db.Stats(context.Background())
			if attempts != tt.wantAttempts {
				t.Errorf("Stats().attempts = %d, want %d", attempts, tt.wantAttempts)
			}
		})
	}
}
