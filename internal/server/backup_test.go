package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPruneBackups_keepsNewestAndSkipsLiveDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The live bbolt file has no dash, so the glob must never match/prune it.
	live := filepath.Join(dir, "subflux.bolt")
	if err := os.WriteFile(live, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := []string{ // oldest -> newest
		"subflux-20260101-000000.bolt",
		"subflux-20260102-000000.bolt",
		"subflux-20260103-000000.bolt",
		"subflux-20260104-000000.bolt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	pruneBackups(dir, 2)

	for _, gone := range names[:2] {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned", gone)
		}
	}
	for _, kept := range names[2:] {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s should have been kept: %v", kept, err)
		}
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("live subflux.bolt must not be pruned: %v", err)
	}
}
