package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/cplieger/atomicfile/v2"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/config/defaults"
)

// runBackup periodically writes a consistent database snapshot and prunes old
// backups until ctx is cancelled. It re-reads the live config each cycle, so
// enable/frequency/retention/path changes take effect on the next iteration
// without a restart.
func (s *Server) runBackup(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.backupFrequency()):
		}
		s.runOneBackup(ctx)
	}
}

// backupFrequency returns the configured interval, clamped to the minimum, or
// the default when unset/unconfigured.
func (s *Server) backupFrequency() time.Duration {
	if cfg, ok := s.state().cfg.(*config.Config); ok {
		if f := cfg.BackupFrequency(); f >= defaults.MinBackupFrequency {
			return f
		}
	}
	return defaults.DefaultBackupFrequency
}

// runOneBackup writes a single timestamped snapshot, then prunes old ones.
func (s *Server) runOneBackup(ctx context.Context) {
	cfg, ok := s.state().cfg.(*config.Config)
	if !ok || !cfg.BackupEnabled() {
		return
	}
	dir := cfg.BackupPath()
	if dir == "" {
		dir = filepath.Dir(config.DefaultDBPath)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		slog.Warn("backup: create directory failed", "dir", dir, "error", err)
		return
	}
	dest := filepath.Join(dir, "subflux-"+time.Now().UTC().Format("20060102-150405")+".bolt")
	start := time.Now()
	if err := s.db.BackupInto(ctx, dest); err != nil {
		slog.Error("backup failed", "dest", dest, "error", err)
		// A failed snapshot is another early disk-full signal; classify it so
		// the persistent operator alert fires between maintenance windows.
		s.recordStoreWriteError(err)
		return
	}
	dur := time.Since(start)
	if err := enforceBackupMode(dest); err != nil {
		slog.Warn("backup: mode enforcement failed", "dest", dest, "error", err)
	}
	slog.Info("database backup written", "dest", dest, "duration", dur.Round(time.Millisecond).String())
	s.metrics.RecordBackupSuccess(dur)
	pruneBackups(dir, cfg.BackupRetention())
}

// enforceBackupMode pins the finished snapshot to owner-only and PROVES the
// filesystem stored that, which the os.Chmod it replaces could not.
//
// It matters here more than at an ordinary file because of what the artifact
// holds: a bbolt snapshot is the whole file, so every backup carries the auth
// buckets — users, password hashes, passkey credentials, API keys. A snapshot
// stored 0640 instead of 0600 hands that to anyone in the file's group, and a
// mode argument is a REQUEST: atomicfile's WithMode(0o600) create and a plain
// os.Chmod both hand the mode to the kernel, and on a filesystem carrying an
// inheritable group ACE the kernel stores something wider (measured on a ZFS
// nfs4acl dataset, a 0o600 create comes back 0770). atomicfile.EnforceMode
// fchmods the OPEN HANDLE, fstats that same handle, and reports
// ErrModeNotStored rather than nil when the two disagree — so the warning below
// now means "the snapshot is not 0600" instead of "the request errored".
//
// OpenRegular supplies the handle rather than an os.Open because the pathname is
// the weak part of the old sequence: chmod-the-name-then-stat-the-name can pin
// one file and certify another if the name is swapped in between, and it also
// refuses a symlink or a non-regular occupant at the final component
// (O_NOFOLLOW in the kernel, O_NONBLOCK so a planted FIFO cannot stall the
// backup goroutine) instead of chmod'ing whatever the name happens to reach.
// dest is absolute by BackupInto's documented contract, which atomicfile's
// write path already enforces, so a relative path never reaches here.
//
// The WARN posture is deliberately UNCHANGED: a snapshot whose mode cannot be
// pinned is still a valid, restorable backup, and destroying it would trade a
// confidentiality problem for a durability one on the very path the operator
// relies on. What changes is that the warning is now true.
func enforceBackupMode(dest string) error {
	f, _, err := atomicfile.OpenRegular(dest)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = atomicfile.EnforceMode(f, 0o600)
	return err
}

// pruneBackups keeps the newest `keep` timestamped backups in dir and removes
// the rest. Timestamped names sort chronologically, so lexical order is age
// order; the glob excludes the live subflux.bolt (no dash).
func pruneBackups(dir string, keep int) {
	if keep < 1 {
		keep = 1
	}
	matches, err := filepath.Glob(filepath.Join(dir, "subflux-*.bolt"))
	if err != nil || len(matches) <= keep {
		return
	}
	slices.Sort(matches)
	for _, old := range matches[:len(matches)-keep] {
		if rmErr := os.Remove(old); rmErr != nil {
			slog.Warn("backup: prune failed", "file", old, "error", rmErr)
		}
	}
}
