package store

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func BenchmarkRecordNoResult(b *testing.B) {
	for _, failures := range []int{0, 5, 20} {
		b.Run(fmt.Sprintf("prior_failures_%d", failures), func(b *testing.B) {
			db := openBenchDB(b)
			ctx := context.Background()

			bp := api.BackoffParams{
				InitialDelay: 10 * time.Second,
				MaxDelay:     time.Hour,
				Multiplier:   2.0,
			}
			// Seed prior failures.
			for range failures {
				if err := db.RecordNoResult(ctx, "episode", "tt888", "en", api.ProviderID("bench-prov"), bp); err != nil {
					b.Fatalf("seed: %v", err)
				}
			}

			b.ResetTimer()
			for i := range b.N {
				prov := api.ProviderID(fmt.Sprintf("bench-prov-%d", i))
				if err := db.RecordNoResult(ctx, "episode", "tt888", "en", prov, bp); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkBackedOffProviders(b *testing.B) {
	for _, n := range []int{0, 10, 100} {
		b.Run(fmt.Sprintf("records_%d", n), func(b *testing.B) {
			db := openBenchDB(b)
			ctx := context.Background()

			bp := api.BackoffParams{
				InitialDelay: 10 * time.Second,
				MaxDelay:     time.Hour,
				Multiplier:   2.0,
			}
			for i := range n {
				prov := api.ProviderID(fmt.Sprintf("provider-%d", i))
				if err := db.RecordNoResult(ctx, "episode", "tt999", "en", prov, bp); err != nil {
					b.Fatalf("seed: %v", err)
				}
			}

			b.ResetTimer()
			for range b.N {
				_, err := db.BackedOffProviders(ctx, "episode", "tt999", "en", 50)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func openBenchDB(b *testing.B) *DB {
	b.Helper()
	path := b.TempDir() + "/bench.db"
	db, err := Open(context.Background(), path)
	if err != nil {
		b.Fatalf("Open: %v", err)
	}
	b.Cleanup(func() { db.Close(context.Background()) })
	return db
}

func BenchmarkBackedOffProviders_MultiProvider(b *testing.B) {
	for _, failures := range []int{0, 5, 20} {
		b.Run(fmt.Sprintf("prior_failures_%d", failures), func(b *testing.B) {
			db := openBenchDB(b)
			ctx := context.Background()

			bp := api.BackoffParams{
				InitialDelay: 10 * time.Second,
				MaxDelay:     time.Hour,
				Multiplier:   2.0,
			}
			// Seed failures across multiple providers.
			provs := []string{"prov-a", "prov-b", "prov-c", "prov-d", "prov-e"}
			for _, p := range provs {
				for range failures {
					if err := db.RecordNoResult(ctx, "episode", "tt999", "en", api.ProviderID(p), bp); err != nil {
						b.Fatalf("seed: %v", err)
					}
				}
			}

			b.ResetTimer()
			for range b.N {
				_, err := db.BackedOffProviders(ctx, "episode", "tt999", "en", 5)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
