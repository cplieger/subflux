package search

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func FuzzHashFile(f *testing.F) {
	// Seed with various sizes: empty, small, exactly 2*hashBlockSize, larger.
	f.Add(make([]byte, 0))
	f.Add(make([]byte, 100))
	f.Add(make([]byte, hashBlockSize*2))
	f.Add(make([]byte, hashBlockSize*2+1))

	f.Fuzz(func(t *testing.T, data []byte) {
		tmp := filepath.Join(t.TempDir(), "fuzz.bin")
		if err := os.WriteFile(tmp, data, 0o644); err != nil {
			t.Fatal(err)
		}

		h, size, err := hashFile(context.Background(), tmp)

		// Files smaller than 2*hashBlockSize are expected to error.
		if int64(len(data)) < hashBlockSize*2 {
			if err == nil {
				t.Fatal("expected error for small file")
			}
			return
		}

		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Hash must be 16 hex characters.
		if len(h) != 16 {
			t.Fatalf("hash length = %d, want 16", len(h))
		}
		for _, c := range h {
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
				t.Fatalf("hash contains non-hex char: %c", c)
			}
		}

		// Size must match written bytes.
		if size != int64(len(data)) {
			t.Fatalf("size = %d, want %d", size, len(data))
		}
	})
}
