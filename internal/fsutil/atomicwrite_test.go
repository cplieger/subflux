package fsutil

import (
	"context"
	"os"
	"path/filepath"
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
