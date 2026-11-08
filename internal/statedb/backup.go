package statedb

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Backup writes a compacted copy of the store to path, mode 0600, while the
// store stays open. It first removes every .*.bolttable-tmp file a crashed
// backup stranded in path's directory, and nothing else; backups run one at
// a time, so none of those is in use. An existing path fails it with an
// error matching fs.ErrExist; after the copy is published, a failed cleanup
// wraps bolttable.ErrPublished and path is complete.
func (s *Store) Backup(ctx context.Context, path string) error {
	s.backupMu.Lock()
	defer s.backupMu.Unlock()
	if err := removeStrandedTemps(filepath.Dir(path)); err != nil {
		return fmt.Errorf("statedb: backup %s: %w", path, err)
	}
	if err := s.db.Backup(ctx, path); err != nil {
		return fmt.Errorf("statedb: backup %s: %w", path, err)
	}
	return nil
}

// removeStrandedTemps removes the regular files in dir named as bolttable
// names a backup's temporary, .<base>.bolttable-tmp.
func removeStrandedTemps(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if stranded, _ := filepath.Match(".?*.bolttable-tmp", e.Name()); !stranded || !e.Type().IsRegular() {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}
