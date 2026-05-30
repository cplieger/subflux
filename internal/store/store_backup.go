package store

import (
	"context"
	"fmt"
)

// BackupInto writes a consistent snapshot of the database to dest using
// SQLite's VACUUM INTO. Unlike a raw file copy, this produces a single
// standalone, defragmented database file and is safe under WAL mode (it runs
// inside a read transaction, so it never captures a torn or mid-checkpoint
// state). dest must not already exist.
func (d *DB) BackupInto(ctx context.Context, dest string) error {
	if _, err := d.db.ExecContext(ctx, "VACUUM INTO ?", dest); err != nil {
		return fmt.Errorf("backup vacuum into %q: %w", dest, err)
	}
	return nil
}
