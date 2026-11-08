package statedb

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
)

func TestBackupRemovesStrandedTempOnly(t *testing.T) {
	s := covLib(t, tempPath(t), policyOf(t, "en"))
	k, _ := showEpisode(t, s, "sonarr", epPath)
	dir := t.TempDir()
	stranded := []string{".subflux-20261101.bolt.bolttable-tmp", ".x.bolttable-tmp"}
	kept := []string{"keep.bolttable-tmp", ".bolttable-tmp", ".x.bolttable-tmp.old", "subflux-20261031.bolt"}
	for _, name := range slices.Concat(stranded, kept) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("partial"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, ".d.bolttable-tmp"), 0o700); err != nil {
		t.Fatal(err)
	}
	kept = append(kept, ".d.bolttable-tmp")

	dst := filepath.Join(dir, "subflux-20261102.bolt")
	if err := s.Backup(t.Context(), dst); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Name())
	}
	want := slices.Sorted(slices.Values(append(slices.Clone(kept), filepath.Base(dst))))
	if !slices.Equal(got, want) {
		t.Errorf("after Backup the directory holds %q, want %q", got, want)
	}

	copied := mustOpen(t, dst, productionConfig())
	if _, found := getRow(t, copied, copied.t.items, k); !found {
		t.Errorf("the backup holds no item record for %s", k)
	}
	if err := s.Backup(t.Context(), dst); !errors.Is(err, fs.ErrExist) {
		t.Errorf("Backup over an existing file = %v, want fs.ErrExist", err)
	}
}

func TestBackupMissingDirectory(t *testing.T) {
	s := newStore(t)
	dst := filepath.Join(t.TempDir(), "gone", "subflux.bolt")
	if err := s.Backup(t.Context(), dst); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Backup into a missing directory = %v, want fs.ErrNotExist", err)
	}
}

// TestBackupConcurrentAllPublish runs backups into one directory at once:
// none may remove another's temporary file mid-walk.
func TestBackupConcurrentAllPublish(t *testing.T) {
	s := newStore(t)
	reason := strings.Repeat("r", 2000)
	update(t, s, func(tx *bolttable.Tx) error {
		for i := range 5000 {
			if err := s.t.credentials.Put(tx, fmt.Sprintf("provider-%05d", i), &credentialRow{At: time.Unix(1, 0).UTC(), Reason: reason}); err != nil {
				return err
			}
		}
		return nil
	})
	dir := t.TempDir()
	var wg sync.WaitGroup
	errs := make([]error, 6)
	for i := range errs {
		wg.Go(func() { errs[i] = s.Backup(t.Context(), filepath.Join(dir, fmt.Sprintf("b%d.bolt", i))) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("backup %d: %v", i, err)
		}
	}
}
