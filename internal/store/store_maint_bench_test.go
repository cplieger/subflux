package store

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"subflux/internal/api"
)

func BenchmarkReconcileState(b *testing.B) {
	for _, n := range []int{10, 50, 100} {
		b.Run(fmt.Sprintf("entries_%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			ctx := context.Background()

			// Use an in-memory stat function: all video files "exist",
			// all subtitle files "exist". This isolates DB + classification
			// overhead from real filesystem I/O.
			db.statFn = func(path string) (os.FileInfo, error) {
				return fakeFileInfo{}, nil
			}

			seedReconcileRows(b, db, n)

			b.ResetTimer()
			for range b.N {
				if _, err := db.ReconcileState(ctx); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// seedReconcileRows inserts n subtitle_state rows via SaveDownload.
func seedReconcileRows(b *testing.B, db *DB, n int) {
	b.Helper()
	ctx := context.Background()
	for i := range n {
		if err := db.SaveDownload(ctx, &api.DownloadRecord{
			MediaType:    "episode",
			MediaID:      fmt.Sprintf("tt%04d", i),
			Language:     "en",
			ProviderName: "opensubtitles",
			ReleaseName:  fmt.Sprintf("release-%d", i),
			Path:         fmt.Sprintf("/media/subs/ep%d.en.srt", i),
			Score:        100,
			Meta:         &api.DownloadMeta{VideoPath: fmt.Sprintf("/media/video/ep%d.mkv", i)},
		}); err != nil {
			b.Fatalf("SaveDownload: %v", err)
		}
	}
}

// fakeFileInfo satisfies os.FileInfo for benchmarking without real I/O.
type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "fake" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0 }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (fakeFileInfo) IsDir() bool        { return false }
func (fakeFileInfo) Sys() any           { return nil }
