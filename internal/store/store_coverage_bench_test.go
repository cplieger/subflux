package store

import (
	"context"
	"fmt"
	"testing"

	"github.com/cplieger/subflux/internal/api"
)

func BenchmarkGetSubtitleFiles(b *testing.B) {
	db := benchDB(b)
	ctx := context.Background()
	// Seed with 100 entries.
	for i := range 100 {
		files := []api.SubtitleFile{{
			Language: "eng", Variant: "full", Source: "os",
			Path: fmt.Sprintf("/media/movie%d/sub.srt", i), Codec: "srt",
		}}
		if _, err := db.RecordSubtitleFiles(ctx, api.MediaTypeMovie, fmt.Sprintf("tmdb-%d", i), files); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = db.GetSubtitleFiles(ctx, api.MediaTypeMovie, "tmdb-")
	}
}

func BenchmarkGetScanStates(b *testing.B) {
	db := benchDB(b)
	ctx := context.Background()
	// Seed with 100 entries.
	for i := range 100 {
		if err := db.RecordScanState(ctx, &api.ScanRecord{
			MediaType: api.MediaTypeEpisode,
			MediaID:   fmt.Sprintf("tvdb-1-s01e%02d", i),
			Title:     "Show", AudioLang: "eng",
			Season: 1, Episode: i,
		}); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_, _ = db.GetScanStates(ctx, api.MediaTypeEpisode, "tvdb-1")
	}
}

func benchDB(b *testing.B) *DB {
	b.Helper()
	path := b.TempDir() + "/bench.db"
	db, err := Open(context.Background(), path)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { db.Close(context.Background()) })
	return db
}
