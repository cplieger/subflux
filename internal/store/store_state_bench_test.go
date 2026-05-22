package store

import (
	"context"
	"fmt"
	"testing"

	"subflux/internal/api"
)

func BenchmarkGetState(b *testing.B) {
	for _, rows := range []int{100, 1000} {
		b.Run(fmt.Sprintf("rows_%d", rows), func(b *testing.B) {
			db := openBenchDB(b)
			ctx := context.Background()
			seedStateRows(b, db, rows)

			b.Run("no_filter", func(b *testing.B) {
				b.ResetTimer()
				for range b.N {
					if _, err := db.GetState(ctx, &api.StateQuery{Limit: 50}); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("single_filter_mediatype", func(b *testing.B) {
				b.ResetTimer()
				for range b.N {
					if _, err := db.GetState(ctx, &api.StateQuery{MediaType: "episode", Limit: 50}); err != nil {
						b.Fatal(err)
					}
				}
			})

			b.Run("all_filters", func(b *testing.B) {
				b.ResetTimer()
				for range b.N {
					if _, err := db.GetState(ctx, &api.StateQuery{MediaType: "episode", Language: "en", Provider: "opensubtitles", Search: "test", Limit: 50}); err != nil {
						b.Fatal(err)
					}
				}
			})
		})
	}
}

// seedStateRows inserts n subtitle_state rows for benchmarking.
func seedStateRows(b *testing.B, db *DB, n int) {
	b.Helper()
	ctx := context.Background()
	providers := []string{"opensubtitles", "gestdown", "subdl", "yifysubtitles"}
	languages := []string{"en", "fr", "de", "es"}
	mediaTypes := []api.MediaType{"episode", "movie"}

	for i := range n {
		mt := mediaTypes[i%len(mediaTypes)]
		lang := languages[i%len(languages)]
		prov := providers[i%len(providers)]
		mediaID := fmt.Sprintf("tt%04d", i/4)
		rec := &api.DownloadRecord{
			MediaType:    mt,
			MediaID:      mediaID,
			Language:     lang,
			ProviderName: api.ProviderID(prov),
			ReleaseName:  fmt.Sprintf("test.release.%d", i),
			Score:        80 + i%20,
			Path:         fmt.Sprintf("/media/%s/%s.srt", mediaID, lang),
			Meta: &api.DownloadMeta{
				Title:   fmt.Sprintf("Test Title %d", i),
				ImdbID:  mediaID,
				Season:  (i / 10) + 1,
				Episode: (i % 10) + 1,
			},
		}
		if err := db.SaveDownload(ctx, rec); err != nil {
			b.Fatalf("seed row %d: %v", i, err)
		}
	}
}
