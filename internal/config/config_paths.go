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

// pathUnderCachedRoot checks whether path is contained within root using
// a pre-opened *os.Root handle. Returns false if the path escapes it.
func pathUnderCachedRoot(rootDir *os.Root, root, path string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
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
	if err != nil || strings.HasPrefix(rel, "..") {
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
		var rootDir *os.Root
		var needClose bool
		if i < len(c.cachedRoots) {
			rootDir = c.cachedRoots[i]
		} else {
			rd, err := os.OpenRoot(root)
			if err != nil {
				continue
			}
			rootDir = rd
			needClose = true
		}
		rel, err := filepath.Rel(root, path)
		if err != nil || strings.HasPrefix(rel, "..") {
			if needClose {
				rootDir.Close()
			}
			continue
		}
		err = rootDir.Remove(rel)
		if needClose {
			rootDir.Close()
		}
		if err == nil || errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("remove %q: %w", path, err)
	}
	return fmt.Errorf("path %q: %w", path, ErrPathNotAllowed)
}
