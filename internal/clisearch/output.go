package clisearch

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/api"
)

// writeSubtitleFile atomically writes subtitle data to the given path.
func writeSubtitleFile(ctx context.Context, path string, data []byte) error {
	_, err := atomicfile.WriteFile(ctx, path, data)
	return err
}

// recordDownload persists the download record using the injected recorder.
func recordDownload(ctx context.Context, req *api.SearchRequest,
	item *searchItem, chosen *api.ScoredResult,
	subPath, lang string, pickN int, recorder DownloadRecorder,
) {
	if recorder == nil {
		return
	}
	mediaID := api.BuildMediaID(req)
	recErr := recorder.SaveDownload(ctx, &api.DownloadRecord{
		MediaType:    item.MediaType,
		MediaID:      mediaID,
		Language:     lang,
		ProviderName: chosen.Sub.Provider,
		ReleaseName:  chosen.Sub.ReleaseName,
		Path:         subPath,
		Score:        chosen.Score,
		Meta: &api.DownloadMeta{
			Title: item.Title, ImdbID: item.ImdbID, VideoPath: item.FilePath,
			Season: item.Season, Episode: item.Episode,
			ReleaseTag: item.SceneName, Manual: pickN > 1,
		},
	})
	if recErr != nil {
		slog.Warn("failed to record download", "error", recErr)
		fmt.Println("  Warning: download not recorded (database unavailable)")
	}
}
