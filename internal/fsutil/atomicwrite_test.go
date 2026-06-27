package fsutil

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAtomicWriteFile(t *testing.T) {
	t.Parallel()

	t.Run("basic_write_and_read", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "test.txt")
		data := []byte("hello world")
		if err := AtomicWriteFile(context.Background(), path, data); err != nil {
			t.Fatalf("AtomicWriteFile: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile: %v", err)
		}
		if string(got) != "hello world" {
			t.Errorf("got %q, want %q", got, "hello world")
		}
	})

	t.Run("overwrites_existing_file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "overwrite.txt")
		if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := AtomicWriteFile(context.Background(), path, []byte("new")); err != nil {
			t.Fatalf("AtomicWriteFile: %v", err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != "new" {
			t.Errorf("got %q, want %q", got, "new")
		}
	})

	t.Run("empty_path_returns_error", func(t *testing.T) {
		t.Parallel()
		err := AtomicWriteFile(context.Background(), "", []byte("data"))
		if err == nil {
			t.Fatal("expected error for empty path")
		}
	})

	t.Run("empty_data_creates_empty_file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.txt")
		if err := AtomicWriteFile(context.Background(), path, nil); err != nil {
			t.Fatalf("AtomicWriteFile: %v", err)
		}
		got, _ := os.ReadFile(path)
		if len(got) != 0 {
			t.Errorf("got %d bytes, want 0", len(got))
		}
	})

	t.Run("respects_file_permissions", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "perms.txt")
		if err := AtomicWriteFileMode(context.Background(), path, []byte("x"), 0o600); err != nil {
			t.Fatalf("AtomicWriteFileMode: %v", err)
		}
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("permissions = %o, want 0600", fi.Mode().Perm())
		}
	})
}

func TestReadBounded(t *testing.T) {
	t.Parallel()

	t.Run("reads_file_within_limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "bounded.txt")
		data := []byte("bounded content")
		os.WriteFile(path, data, 0o644)
		got, err := ReadBounded(context.Background(), path, 1024)
		if err != nil {
			t.Fatalf("ReadBounded: %v", err)
		}
		if string(got) != "bounded content" {
			t.Errorf("got %q, want %q", got, "bounded content")
		}
	})

	t.Run("rejects_file_exceeding_limit", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "large.txt")
		os.WriteFile(path, make([]byte, 100), 0o644)
		_, err := ReadBounded(context.Background(), path, 50)
		if err == nil {
			t.Fatal("expected error for file exceeding limit")
		}
	})

	t.Run("returns_error_for_missing_file", func(t *testing.T) {
		t.Parallel()
		_, err := ReadBounded(context.Background(), "/nonexistent/path.txt", 1024)
		if err == nil {
			t.Fatal("expected error for missing file")
		}
	})

	t.Run("reads_empty_file", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "empty.txt")
		os.WriteFile(path, nil, 0o644)
		got, err := ReadBounded(context.Background(), path, 1024)
		if err != nil {
			t.Fatalf("ReadBounded: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("got %d bytes, want 0", len(got))
		}
	})

	t.Run("exact_limit_succeeds", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "exact.txt")
		data := []byte("12345")
		os.WriteFile(path, data, 0o644)
		got, err := ReadBounded(context.Background(), path, 5)
		if err != nil {
			t.Fatalf("ReadBounded: %v", err)
		}
		if string(got) != "12345" {
			t.Errorf("got %q, want %q", got, "12345")
		}
	})
}

func TestPrepareAtomicWrite(t *testing.T) {
	t.Parallel()

	t.Run("creates_temp_file_ready_for_rename", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "final.txt")
		data := []byte("prepared data")
		tmpPath, cleanup, err := PrepareAtomicWrite(context.Background(), path, data)
		if err != nil {
			t.Fatalf("PrepareAtomicWrite: %v", err)
		}
		defer cleanup()
		// Temp file should exist and contain data.
		got, err := os.ReadFile(tmpPath)
		if err != nil {
			t.Fatalf("ReadFile(tmp): %v", err)
		}
		if string(got) != "prepared data" {
			t.Errorf("tmp content = %q, want %q", got, "prepared data")
		}
	})

	t.Run("commit_renames_to_final", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path := filepath.Join(dir, "committed.txt")
		data := []byte("commit me")
		tmpPath, cleanup, err := PrepareAtomicWrite(context.Background(), path, data)
		if err != nil {
			t.Fatalf("PrepareAtomicWrite: %v", err)
		}
		defer cleanup()
		if err := CommitAtomicWrite(tmpPath, path); err != nil {
			t.Fatalf("CommitAtomicWrite: %v", err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != "commit me" {
			t.Errorf("final content = %q, want %q", got, "commit me")
		}
	})

	t.Run("empty_path_returns_error", func(t *testing.T) {
		t.Parallel()
		_, _, err := PrepareAtomicWrite(context.Background(), "", []byte("x"))
		if err == nil {
			t.Fatal("expected error for empty path")
		}
	})
}

func TestAtomicWriteFileMode_context_cancelled(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "cancelled.txt")
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	err := AtomicWriteFileMode(ctx, path, []byte("data"), 0o644)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
	// File should not exist.
	if _, statErr := os.Stat(path); statErr == nil {
		t.Error("file should not exist after cancelled write")
	}
}

// captureDebugLogs swaps the process default slog logger for a Debug-level
// text handler writing into a buffer, runs fn, restores the original logger,
// and returns everything logged during fn.
//
// Callers must NOT run in parallel: this mutates a process global. Go defers
// t.Parallel tests until the package's sequential tests have finished, so a
// non-parallel test that swaps and restores the default within its own body
// never overlaps a parallel test.
func captureDebugLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(orig)
	fn()
	return buf.String()
}

// TestAtomicWriteFileMode_successEmitsNoDirSyncLog pins the polarity of the
// parent-dir fsync/close error guards: on a successful write the dir Sync and
// Close succeed, so no failure log must be emitted. Negating either guard would
// make the success path log a failure.
func TestAtomicWriteFileMode_successEmitsNoDirSyncLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.txt")
	logs := captureDebugLogs(t, func() {
		if err := AtomicWriteFileMode(context.Background(), path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("AtomicWriteFileMode(%q) = %v, want nil", path, err)
		}
	})
	if got, err := os.ReadFile(path); err != nil || string(got) != "payload" {
		t.Fatalf("ReadFile(%q) = %q, %v; want %q, nil", path, got, err, "payload")
	}
	if strings.Contains(logs, "parent dir fsync failed") {
		t.Errorf("success emitted %q; the dir fsync succeeded so the guard must stay false", "parent dir fsync failed")
	}
	if strings.Contains(logs, "parent dir close failed") {
		t.Errorf("success emitted %q; the dir close succeeded so the guard must stay false", "parent dir close failed")
	}
}

// TestCommitAtomicWrite_successEmitsNoDirSyncLog pins the same parent-dir
// fsync/close guard polarity inside CommitAtomicWrite.
func TestCommitAtomicWrite_successEmitsNoDirSyncLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "committed.txt")
	tmpPath, cleanup, err := PrepareAtomicWrite(context.Background(), path, []byte("commit-payload"))
	if err != nil {
		t.Fatalf("PrepareAtomicWrite(%q) = %v, want nil", path, err)
	}
	_ = cleanup // CommitAtomicWrite renames the temp away; do not invoke cleanup.
	logs := captureDebugLogs(t, func() {
		if err := CommitAtomicWrite(tmpPath, path); err != nil {
			t.Fatalf("CommitAtomicWrite(%q, %q) = %v, want nil", tmpPath, path, err)
		}
	})
	if got, err := os.ReadFile(path); err != nil || string(got) != "commit-payload" {
		t.Fatalf("ReadFile(%q) = %q, %v; want %q, nil", path, got, err, "commit-payload")
	}
	if strings.Contains(logs, "parent dir fsync failed") {
		t.Errorf("success emitted %q; the dir fsync succeeded", "parent dir fsync failed")
	}
	if strings.Contains(logs, "parent dir close failed") {
		t.Errorf("success emitted %q; the dir close succeeded", "parent dir close failed")
	}
}

// TestPrepareAtomicWrite_cleanupEmitsNoLogOnSuccess pins the polarity of the
// temp-file cleanup guard: removing an existing temp file succeeds, so cleanup
// must emit no failure log.
func TestPrepareAtomicWrite_cleanupEmitsNoLogOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prepared.txt")
	tmpPath, cleanup, err := PrepareAtomicWrite(context.Background(), path, []byte("x"))
	if err != nil {
		t.Fatalf("PrepareAtomicWrite(%q) = %v, want nil", path, err)
	}
	if _, statErr := os.Stat(tmpPath); statErr != nil {
		t.Fatalf("temp file %q should exist before cleanup: %v", tmpPath, statErr)
	}
	logs := captureDebugLogs(t, func() { cleanup() })
	if _, statErr := os.Stat(tmpPath); statErr == nil {
		t.Errorf("temp file %q should be removed after cleanup()", tmpPath)
	}
	if strings.Contains(logs, "temp file cleanup failed") {
		t.Errorf("successful cleanup emitted %q; the Remove of an existing temp succeeded", "temp file cleanup failed")
	}
}
