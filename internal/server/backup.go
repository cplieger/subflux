package server

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/config/defaults"
)

// backupStore is the narrow capability the backup runner needs from the store.
type backupStore interface {
	BackupInto(ctx context.Context, dest string) error
}

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
	bs, ok := s.db.(backupStore)
	if !ok {
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
	dest := filepath.Join(dir, "subflux-"+time.Now().Format("20060102-150405")+".db")
	if err := bs.BackupInto(ctx, dest); err != nil {
		slog.Error("backup failed", "dest", dest, "error", err)
		return
	}
	if err := os.Chmod(dest, 0o600); err != nil {
		slog.Warn("backup: chmod failed", "dest", dest, "error", err)
	}
	slog.Info("database backup written", "dest", dest)
	pruneBackups(dir, cfg.BackupRetention())
}

// pruneBackups keeps the newest `keep` timestamped backups in dir and removes
// the rest. Timestamped names sort chronologically, so lexical order is age
// order; the glob excludes the live subflux.db (no dash).
func pruneBackups(dir string, keep int) {
	if keep < 1 {
		keep = 1
	}
	matches, err := filepath.Glob(filepath.Join(dir, "subflux-*.db"))
	if err != nil || len(matches) <= keep {
		return
	}
	sort.Strings(matches)
	for _, old := range matches[:len(matches)-keep] {
		if rmErr := os.Remove(old); rmErr != nil {
			slog.Warn("backup: prune failed", "file", old, "error", rmErr)
		}
	}
}
