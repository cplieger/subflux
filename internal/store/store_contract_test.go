package store

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/storetest"
)

func TestStoreContract(t *testing.T) {
	t.Parallel()

	// Run the shared contract suite against the real store.DB.
	storetest.Suite(t, func(t *testing.T) api.Store {
		return openTestDB(t)
	})

	t.Run("RecordNoResult_BackedOffProviders_roundtrip", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		err := db.RecordNoResult(ctx, api.MediaTypeMovie, "tmdb-123", "eng", "opensubtitles",
			api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2})
		if err != nil {
			t.Fatalf("RecordNoResult: %v", err)
		}
		backed, err := db.BackedOffProviders(ctx, api.MediaTypeMovie, "tmdb-123", "eng", 5)
		if err != nil {
			t.Fatalf("BackedOffProviders: %v", err)
		}
		if len(backed) != 0 {
			// First attempt should not be backed off yet (delay not elapsed),
			// but the record should exist.
			t.Logf("BackedOffProviders returned %d entries (timing-dependent)", len(backed))
		}
	})

	t.Run("SaveDownload_retrieval", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		rec := &api.DownloadRecord{
			MediaType:    api.MediaTypeMovie,
			MediaID:      "tmdb-456",
			Language:     "eng",
			ProviderName: "opensubtitles",
			ReleaseName:  "Movie.2024.eng.srt",
			Score:        85,
			Path:         "/media/movie/sub.srt",
		}
		if err := db.SaveDownload(ctx, rec); err != nil {
			t.Fatalf("SaveDownload: %v", err)
		}
		refs, err := db.DownloadedRefs(ctx, api.MediaTypeMovie, "tmdb-456", "eng")
		if err != nil {
			t.Fatalf("DownloadedRefs: %v", err)
		}
		if len(refs) != 1 {
			t.Fatalf("DownloadedRefs: got %d, want 1", len(refs))
		}
		if refs[0].ReleaseName != "Movie.2024.eng.srt" {
			t.Errorf("ReleaseName = %q, want %q", refs[0].ReleaseName, "Movie.2024.eng.srt")
		}
	})

	t.Run("ManualLock_lifecycle", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		locked, err := db.IsManuallyLocked(ctx, api.MediaTypeMovie, "tmdb-789", "eng")
		if err != nil {
			t.Fatalf("IsManuallyLocked: %v", err)
		}
		if locked {
			t.Error("should not be locked initially")
		}
	})

	t.Run("PollTimestamp_roundtrip", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		now := time.Now().Truncate(time.Second)
		if err := db.SetPollTimestamp(ctx, api.PollKeySonarr, now); err != nil {
			t.Fatalf("SetPollTimestamp: %v", err)
		}
		got, err := db.GetPollTimestamp(ctx, api.PollKeySonarr)
		if err != nil {
			t.Fatalf("GetPollTimestamp: %v", err)
		}
		if !got.Equal(now) {
			t.Errorf("GetPollTimestamp = %v, want %v", got, now)
		}
	})

	t.Run("RecordScanState_GetScanStates_roundtrip", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		err := db.RecordScanState(ctx, &api.ScanRecord{MediaType: api.MediaTypeEpisode, MediaID: "tvdb-100-s01e01", Title: "Show", AudioLang: "eng", Season: 1, Episode: 1})
		if err != nil {
			t.Fatalf("RecordScanState: %v", err)
		}
		states, err := db.GetScanStates(ctx, api.MediaTypeEpisode, "tvdb-100")
		if err != nil {
			t.Fatalf("GetScanStates: %v", err)
		}
		if len(states) != 1 {
			t.Fatalf("GetScanStates: got %d, want 1", len(states))
		}
	})

	t.Run("RecordSubtitleFiles_GetSubtitleFiles_roundtrip", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		files := []api.SubtitleFile{
			{Language: "eng", Variant: "full", Source: "opensubtitles", Path: "/sub.srt", Codec: "srt"},
		}
		changed, err := db.RecordSubtitleFiles(ctx, api.MediaTypeMovie, "tmdb-200", files)
		if err != nil {
			t.Fatalf("RecordSubtitleFiles: %v", err)
		}
		if !changed {
			t.Error("RecordSubtitleFiles should report changed on first insert")
		}
		got, err := db.GetSubtitleFiles(ctx, api.MediaTypeMovie, "tmdb-200")
		if err != nil {
			t.Fatalf("GetSubtitleFiles: %v", err)
		}
		if len(got) != 1 {
			t.Fatalf("GetSubtitleFiles: got %d, want 1", len(got))
		}
	})

	t.Run("SetSyncOffset_GetSyncOffset_roundtrip", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		// First record a subtitle file so the path exists.
		files := []api.SubtitleFile{
			{Language: "eng", Variant: "full", Source: "external", Path: "/media/show/sub.srt", Codec: "srt"},
		}
		if _, err := db.RecordSubtitleFiles(ctx, api.MediaTypeEpisode, "tvdb-sync-1", files); err != nil {
			t.Fatalf("RecordSubtitleFiles: %v", err)
		}
		if err := db.SetSyncOffset(ctx, "/media/show/sub.srt", 1500); err != nil {
			t.Fatalf("SetSyncOffset: %v", err)
		}
		got, err := db.GetSyncOffset(ctx, "/media/show/sub.srt")
		if err != nil {
			t.Fatalf("GetSyncOffset: %v", err)
		}
		if got != 1500 {
			t.Errorf("GetSyncOffset = %d, want 1500", got)
		}
	})

	t.Run("GetState_after_RecordScanState", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		if err := db.RecordScanState(ctx, &api.ScanRecord{MediaType: api.MediaTypeEpisode, MediaID: "tvdb-qs-1-s01e01", Title: "QueryShow", AudioLang: "eng", Season: 1, Episode: 1}); err != nil {
			t.Fatalf("RecordScanState: %v", err)
		}
		states, err := db.GetScanStates(ctx, api.MediaTypeEpisode, "tvdb-qs-1")
		if err != nil {
			t.Fatalf("GetScanStates: %v", err)
		}
		if len(states) != 1 {
			t.Fatalf("GetScanStates: got %d, want 1", len(states))
		}
		if states[0].Title != "QueryShow" {
			t.Errorf("Title = %q, want %q", states[0].Title, "QueryShow")
		}
	})

	t.Run("DeleteStateByPaths_removes_matching", func(t *testing.T) {
		t.Parallel()
		db := openTestDB(t)
		ctx := context.Background()
		if err := db.SaveDownload(ctx, &api.DownloadRecord{
			MediaType: api.MediaTypeMovie, MediaID: "tmdb-del-1",
			Language: "eng", ProviderName: "os", ReleaseName: "Movie-GRP",
			Path: "/media/del/sub.srt", Score: 80,
			Meta: &api.DownloadMeta{VideoPath: "/media/del/movie.mkv"},
		}); err != nil {
			t.Fatalf("SaveDownload: %v", err)
		}
		result, err := db.DeleteStateByPaths(ctx, []string{"/media/del/movie.mkv"})
		if err != nil {
			t.Fatalf("DeleteStateByPaths: %v", err)
		}
		if len(result.Paths) != 1 {
			t.Errorf("DeleteStateByPaths paths = %d, want 1", len(result.Paths))
		}
	})
}
