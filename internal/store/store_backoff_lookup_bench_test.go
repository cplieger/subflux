package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func BenchmarkBackedOffProviders_empty(b *testing.B) {
	db := openBenchDB(b)
	ctx := context.Background()
	b.ResetTimer()
	for range b.N {
		_, err := db.BackedOffProviders(ctx, "episode", "tt000", "en", 50)
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetBackoffItems(b *testing.B) {
	for _, n := range []int{0, 50, 200} {
		b.Run(fmt.Sprintf("items_%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			ctx := context.Background()

			bp := api.BackoffParams{
				InitialDelay: 10 * time.Second,
				MaxDelay:     time.Hour,
				Multiplier:   2.0,
			}
			for i := range n {
				prov := fmt.Sprintf("prov-%d", i)
				mediaID := fmt.Sprintf("tt%04d", i)
				if err := db.RecordNoResult(ctx, "episode", mediaID, "en", api.ProviderID(prov), bp); err != nil {
					b.Fatalf("seed: %v", err)
				}
			}

			b.ResetTimer()
			for range b.N {
				_, err := db.GetBackoffItems(ctx)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
