// Package fsutil provides filesystem utility functions.
package fsutil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ErrEmptyPath is returned when an atomic write is called with an empty path.
var ErrEmptyPath = errors.New("atomic write: empty path")

// ErrUnsafePath is returned when a path argument fails the local
// safety check (non-absolute, contains "..", or otherwise invalid).
// Callers are expected to pass paths derived from validated identifiers
// joined to a base directory; this guard makes the property local to
// fsutil so CodeQL's go/path-injection analyzer can prove safety
// without tracking validation across package boundaries.
var ErrUnsafePath = errors.New("atomic write: unsafe path")

// validateAbsClean checks that path is absolute, free of ".." traversal
// segments, and returns the cleaned form. Each entry point in this
// package calls validateAbsClean before any FS mutation to give CodeQL
// a sanitiser it recognises and to catch refactor regressions.
func validateAbsClean(path string) (string, error) {
	if path == "" {
		return "", ErrEmptyPath
	}
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) {
		return "", fmt.Errorf("%w: not absolute: %q", ErrUnsafePath, path)
	}
	// filepath.Clean collapses ".." but on some platforms a leading
	// drive-relative ".." can remain; reject defensively.
	if strings.Contains(clean, ".."+string(filepath.Separator)) ||
		strings.HasSuffix(clean, string(filepath.Separator)+"..") ||
		clean == ".." {
		return "", fmt.Errorf("%w: contains traversal: %q", ErrUnsafePath, path)
	}
	return clean, nil
}

// WritePhase identifies which step of an atomic write failed.
type WritePhase int

// WritePhase constants identify which step of an atomic write failed.
const (
	PhaseTempCreate WritePhase = iota + 1
	PhaseTempWrite
	PhaseTempChmod
	PhaseTempSync
	PhaseTempClose
	PhaseRename
)

func (p WritePhase) String() string {
	switch p {
	case PhaseTempCreate:
		return "create temp file"
	case PhaseTempWrite:
		return "write temp file"
	case PhaseTempChmod:
		return "chmod temp file"
	case PhaseTempSync:
		return "sync temp file"
	case PhaseTempClose:
		return "close temp file"
	case PhaseRename:
		return "rename to final path"
	default:
		return "unknown phase"
	}
}

// WriteError wraps an atomic-write failure with the phase that failed.
// Callers can use errors.As to inspect the phase without string matching.
type WriteError struct {
	Err   error
	Phase WritePhase
}

func (e *WriteError) Error() string { return e.Phase.String() + ": " + e.Err.Error() }
func (e *WriteError) Unwrap() error { return e.Err }

// AtomicWriteFile writes data to a temp file and renames it to path,
// preventing corruption on crash. The context allows early bailout between
// expensive I/O steps when the caller has been cancelled.
func AtomicWriteFile(ctx context.Context, path string, data []byte) error {
	return AtomicWriteFileMode(ctx, path, data, 0o644)
}

// AtomicWriteFileMode writes data to path atomically with the specified permissions.
// Checks ctx.Err() between expensive I/O steps to allow cancellation.
func AtomicWriteFileMode(ctx context.Context, path string, data []byte, mode os.FileMode) error {
	cleanPath, err := validateAbsClean(path)
	if err != nil {
		return err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("atomic write: %w", ctxErr)
	}
	dir := filepath.Dir(cleanPath)
	tmp, err := os.CreateTemp(dir, ".subflux-*.tmp")
	if err != nil {
		return &WriteError{Phase: PhaseTempCreate, Err: err}
	}
	tmpName := tmp.Name()
	cleanup := func() {
		if rmErr := os.Remove(tmpName); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			slog.Debug("atomic write: temp file cleanup failed",
				"path", tmpName, "error", rmErr)
		}
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		cleanup()
		return &WriteError{Phase: PhaseTempWrite, Err: err}
	}
	if err := ctx.Err(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("atomic write: %w", err)
	}
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		cleanup()
		return &WriteError{Phase: PhaseTempChmod, Err: err}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		cleanup()
		return &WriteError{Phase: PhaseTempSync, Err: err}
	}
	if err := ctx.Err(); err != nil {
		tmp.Close()
		cleanup()
		return fmt.Errorf("atomic write: %w", err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return &WriteError{Phase: PhaseTempClose, Err: err}
	}
	if err := os.Rename(tmpName, cleanPath); err != nil {
		cleanup()
		return &WriteError{Phase: PhaseRename, Err: err}
	}
	if d, err := os.Open(dir); err == nil {
		if syncErr := d.Sync(); syncErr != nil {
			slog.Debug("atomic write: parent dir fsync failed",
				"dir", dir, "error", syncErr)
		}
		if closeErr := d.Close(); closeErr != nil {
			slog.Debug("atomic write: parent dir close failed",
				"dir", dir, "error", closeErr)
		}
	}
	return nil
}

// PrepareAtomicWrite creates a temp file with data written, synced, and
// closed — ready for a final rename. Checks ctx.Err() between I/O steps.
func PrepareAtomicWrite(ctx context.Context, path string, data []byte) (tmpPath string, cleanup func(), err error) {
	cleanPath, err := validateAbsClean(path)
	if err != nil {
		return "", nil, err
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", nil, fmt.Errorf("atomic write: %w", ctxErr)
	}
	dir := filepath.Dir(cleanPath)
	tmp, err := os.CreateTemp(dir, ".subflux-*.tmp")
	if err != nil {
		return "", nil, &WriteError{Phase: PhaseTempCreate, Err: err}
	}
	tmpName := tmp.Name()
	doCleanup := func() {
		if rmErr := os.Remove(tmpName); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			slog.Debug("atomic write: temp file cleanup failed",
				"path", tmpName, "error", rmErr)
		}
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		doCleanup()
		return "", nil, &WriteError{Phase: PhaseTempWrite, Err: err}
	}
	if err := ctx.Err(); err != nil {
		tmp.Close()
		doCleanup()
		return "", nil, fmt.Errorf("atomic write: %w", err)
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		doCleanup()
		return "", nil, &WriteError{Phase: PhaseTempChmod, Err: err}
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		doCleanup()
		return "", nil, &WriteError{Phase: PhaseTempSync, Err: err}
	}
	if err := tmp.Close(); err != nil {
		doCleanup()
		return "", nil, &WriteError{Phase: PhaseTempClose, Err: err}
	}
	return tmpName, doCleanup, nil
}

// CommitAtomicWrite renames the prepared temp file to the final path and
// fsyncs the parent directory.
func CommitAtomicWrite(tmpPath, finalPath string) error {
	cleanFinal, err := validateAbsClean(finalPath)
	if err != nil {
		// Clean up the orphan temp file before returning so a malformed
		// finalPath doesn't leak an on-disk *.tmp.
		if rmErr := os.Remove(tmpPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			slog.Debug("atomic write: temp file cleanup failed after path validation error",
				"path", tmpPath, "error", rmErr)
		}
		return err
	}
	if err := os.Rename(tmpPath, cleanFinal); err != nil {
		if rmErr := os.Remove(tmpPath); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			slog.Debug("atomic write: temp file cleanup failed after rename error",
				"path", tmpPath, "error", rmErr)
		}
		return &WriteError{Phase: PhaseRename, Err: err}
	}
	dir := filepath.Dir(cleanFinal)
	if d, err := os.Open(dir); err == nil {
		if syncErr := d.Sync(); syncErr != nil {
			slog.Debug("atomic write: parent dir fsync failed",
				"dir", dir, "error", syncErr)
		}
		if closeErr := d.Close(); closeErr != nil {
			slog.Debug("atomic write: parent dir close failed",
				"dir", dir, "error", closeErr)
		}
	}
	return nil
}

// ReadBounded opens a file, checks its size against maxBytes, and reads it
// with a LimitReader. Returns an error if the file exceeds the size limit.
// Checks ctx.Err() after Open and after Stat to allow cancellation before
// the potentially large ReadAll.
func ReadBounded(ctx context.Context, path string, maxBytes int64) ([]byte, error) {
	cleanPath, err := validateAbsClean(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(cleanPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err2 := ctx.Err(); err2 != nil {
		return nil, err2
	}
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.Size() > maxBytes {
		return nil, fmt.Errorf("%w: %d bytes (max %d)", ErrFileTooLarge, fi.Size(), maxBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return io.ReadAll(io.LimitReader(f, maxBytes))
}

// ErrFileTooLarge is a sentinel error for file-size-exceeded conditions in fsutil.
// Wrap it with fmt.Errorf("%w: ...") so callers can use errors.Is.
var ErrFileTooLarge = errors.New("file too large")
