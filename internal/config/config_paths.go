package config

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Container-internal path constants (single source of truth).
const (
	// DefaultConfigPath is the container-internal config file path.
	DefaultConfigPath = "/config/config.yaml"
	// DefaultDBPath is the container-internal bbolt database file path.
	// Core and auth buckets live in the same file; no separate auth path.
	DefaultDBPath = "/config/subflux.bolt"
)

// ErrPathNotAllowed is returned when a path is not under any configured media_roots.
var ErrPathNotAllowed = errors.New("path not under any configured media_roots")

// MediaRoots returns the configured media root directories.
func (c *Config) MediaRoots() []string { return c.MediaRootDirs }

// ValidatePath checks that a file path is under one of the configured
// media roots using pre-opened os.Root handles for symlink-safe containment.
// Returns an error if the path escapes all roots.
// If no media roots are configured, all paths are allowed.
func (c *Config) ValidatePath(ctx context.Context, path string) error {
	if len(c.MediaRootDirs) == 0 {
		slog.Debug("media_roots not configured, all paths allowed",
			"path", path)
		return nil
	}
	for i, root := range c.MediaRootDirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if i < len(c.cachedRoots) {
			if pathUnderCachedRoot(c.cachedRoots[i], root, path) {
				return nil
			}
		} else {
			if pathUnderRoot(root, path) {
				return nil
			}
		}
	}
	return fmt.Errorf("path %q: %w", path, ErrPathNotAllowed)
}

// relEscapesRoot reports whether a filepath.Rel result escapes its root. A
// cleaned relative path escapes exactly when it IS ".." or starts with
// "../" (OS separator); a plain ".." string prefix would also reject
// legitimate names that merely BEGIN with two dots, like "..extras/movie.mkv".
// os.Root independently enforces containment at the syscall layer; this check
// is the cheap first gate.
func relEscapesRoot(rel string) bool {
	return rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// pathUnderCachedRoot checks whether path is contained within root using
// a pre-opened *os.Root handle. Returns false if the path escapes it.
func pathUnderCachedRoot(rootDir *os.Root, root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || relEscapesRoot(rel) {
		return false
	}
	_, err = rootDir.Stat(rel)
	return err == nil
}

// pathUnderRoot checks whether path is contained within root using
// os.OpenRoot for symlink-safe containment. Returns false if the root
// is inaccessible or the path escapes it. Used as fallback when cached
// roots are unavailable.
func pathUnderRoot(root, path string) bool {
	rootDir, err := os.OpenRoot(root)
	if err != nil {
		slog.Warn("media root inaccessible", "root", root, "error", err)
		return false
	}
	defer rootDir.Close()

	rel, err := filepath.Rel(root, path)
	if err != nil || relEscapesRoot(rel) {
		return false
	}
	_, err = rootDir.Stat(rel)
	return err == nil
}

// RemoveUnderRoot deletes a file atomically through an os.Root handle,
// eliminating the TOCTOU window between path validation and removal.
// Returns nil if the file was removed or did not exist.
//
// If no media_roots are configured the operation is refused — earlier
// versions fell back to a bare os.Remove(path), but that gave callers
// implicit superuser-style FS access that bypassed the os.Root
// containment guarantee. The fallback also tripped CodeQL's
// go/path-injection rule for code paths where row.Path originates from
// the database (and ultimately from a filesystem scan that itself ran
// outside any safety envelope). Refusing is the safe default; admins
// who want subtitle deletion to work must configure media_roots.
func (c *Config) RemoveUnderRoot(ctx context.Context, path string) error {
	if len(c.MediaRootDirs) == 0 {
		slog.Warn("RemoveUnderRoot: refused, no media_roots configured", "path", path)
		return fmt.Errorf("path %q: %w (no media_roots configured)", path, ErrPathNotAllowed)
	}
	for i, root := range c.MediaRootDirs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if done, err := c.removeUnderSingleRoot(i, root, path); done {
			return err
		}
	}
	return fmt.Errorf("path %q: %w", path, ErrPathNotAllowed)
}

// removeUnderSingleRoot attempts to remove path through the i-th media root,
// using the pre-opened cached handle when available or opening one for this
// call. It returns done=true when iteration should stop (the path was under
// this root and was removed, was already gone, or the removal errored — in
// which case err is non-nil); done=false means path is not under this root,
// so the caller should try the next one. Containment is enforced by os.Root
// plus the ".." relative-path check, exactly as the per-request path checks.
func (c *Config) removeUnderSingleRoot(i int, root, path string) (bool, error) {
	var rootDir *os.Root
	if i < len(c.cachedRoots) {
		rootDir = c.cachedRoots[i]
	} else {
		rd, err := os.OpenRoot(root)
		if err != nil {
			return false, nil
		}
		defer rd.Close()
		rootDir = rd
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || relEscapesRoot(rel) {
		return false, nil
	}
	if err := rootDir.Remove(rel); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return true, fmt.Errorf("remove %q: %w", path, err)
	}
	return true, nil
}
