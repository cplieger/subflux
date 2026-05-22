package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkHashFile(b *testing.B) {
	sizes := []struct {
		name string
		size int
	}{
		{"128KB", 128 * 1024},
		{"1MB", 1024 * 1024},
		{"10MB", 10 * 1024 * 1024},
	}

	for _, sz := range sizes {
		b.Run(sz.name, func(b *testing.B) {
			dir := b.TempDir()
			path := filepath.Join(dir, "test.mkv")
			data := make([]byte, sz.size)
			if err := os.WriteFile(path, data, 0o644); err != nil {
				b.Fatal(err)
			}

			ctx := context.Background()
			b.ResetTimer()
			b.ReportAllocs()
			for range b.N {
				_, _, err := hashFile(ctx, path)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
