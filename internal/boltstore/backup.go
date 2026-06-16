package boltstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cplieger/subflux/internal/fsutil"
	bolt "go.etcd.io/bbolt"
)

// BackupInto writes a single consistent hot snapshot of the entire bbolt file
// (the core domain plus the auth buckets — one engine, one file, one backup) to
// dest. It runs bbolt's tx.WriteTo inside a read (View) transaction, so the
// snapshot is a point-in-time-consistent copy that never captures a torn or
// mid-commit state, even while the live store keeps serving writes (MVCC
// readers do not block the single writer).
//
// dest must be an absolute, traversal-free path (the server builds it under the
// configured backup directory). The server names artifacts
// "subflux-<ts>.bolt"; the matching prune glob in internal/server/backup.go is
// "subflux-*.bolt", and the leading dash excludes the live "subflux.bolt" from
// pruning.
//
// # WriteTo is a copy, not a compaction
//
// tx.WriteTo copies every live AND free page, so the snapshot is the same size
// as the live file and is NOT defragmented. Reclaiming disk after heavy
// churn is a SEPARATE, explicit bbolt.Compact step (see the file-growth
// runbook, spec task 12.4); it is deliberately not done here so a routine
// backup stays a cheap, read-only streaming copy.
//
// # Atomic write
//
// The snapshot streams to a temp file in dest's directory, is fsynced and
// closed, then renamed onto dest with a parent-directory fsync (via
// fsutil.CommitAtomicWrite). A crash mid-backup therefore leaves either a
// complete snapshot at dest or nothing at dest — never a partial
// "subflux-<ts>.bolt" that pruning would later treat as a valid backup.
//
// # Restore procedure (summary; full runbook is spec task 12.4)
//
// Backups are plain bbolt files. To restore: stop the subflux container (so it
// releases the exclusive file lock), copy a chosen snapshot over
// /config/subflux.bolt, then restart the container. Point Kopia (or any
// snapshotting backup agent) at the backup snapshot DIRECTORY, never at the
// live mmap'd /config/subflux.bolt — copying the live file out from under the
// writer can capture a torn page; these WriteTo snapshots are the consistent
// artifact to archive.
func (d *DB) BackupInto(ctx context.Context, dest string) error {
	if d == nil || d.db == nil {
		return errors.New("boltstore: backup: store is not open")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("boltstore: backup: %w", err)
	}

	dir := filepath.Dir(dest)
	tmp, err := os.CreateTemp(dir, ".subflux-backup-*.tmp")
	if err != nil {
		return fmt.Errorf("boltstore: backup: create temp in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup of the temp file on any failure before the rename
	// commits it; after a successful CommitAtomicWrite the temp no longer
	// exists, so the deferred remove is a harmless no-op.
	committed := false
	defer func() {
		if !committed {
			if rmErr := os.Remove(tmpName); rmErr != nil && !errors.Is(rmErr, os.ErrNotExist) {
				// A leaked temp is non-fatal; surface at debug level only.
				_ = rmErr
			}
		}
	}()

	// Stream the snapshot under a short read transaction. WriteTo copies the
	// whole file (live + free pages) at the transaction's consistent view.
	if err := d.db.View(func(tx *bolt.Tx) error {
		if _, werr := tx.WriteTo(tmp); werr != nil {
			return werr
		}
		return nil
	}); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("boltstore: backup: write snapshot: %w", err)
	}

	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("boltstore: backup: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("boltstore: backup: close temp: %w", err)
	}

	// Rename the prepared snapshot onto dest and fsync the parent directory.
	if err := fsutil.CommitAtomicWrite(tmpName, dest); err != nil {
		return fmt.Errorf("boltstore: backup: commit %q: %w", dest, err)
	}
	committed = true
	return nil
}
