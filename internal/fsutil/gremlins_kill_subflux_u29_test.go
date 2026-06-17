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

// gk_subflux_u29_captureDebugLogs swaps the process default slog logger for a
// Debug-level text handler writing into a buffer, runs fn, restores the
// original logger, and returns everything logged during fn.
//
// The fsutil parent-dir fsync/close blocks and the temp-file cleanup block
// log only at Debug level, and only when the underlying Sync/Close/Remove
// FAILS ("if syncErr != nil { slog.Debug(...) }"). A CONDITIONALS_NEGATION
// mutation of the "!= nil" guard to "== nil" makes the SUCCESS path emit the
// failure log, so asserting the message is ABSENT after a successful
// operation distinguishes original (no log) from mutant (log present).
//
// Callers must NOT run in parallel: this mutates a process global. Go defers
// t.Parallel tests until all sequential tests in the package have finished,
// so a non-parallel test that swaps and restores the default within its own
// body never overlaps a parallel test.
func gk_subflux_u29_captureDebugLogs(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(orig)
	fn()
	return buf.String()
}

// TestGk_subflux_u29_AtomicWriteFileMode_noSpuriousDirSyncLogs kills
// atomicwrite.go:153 and :157 (CONDITIONALS_NEGATION on the parent-dir
// fsync/close error guards). On a successful write the dir Sync and Close
// succeed, so the original emits no debug log; the mutated guards
// ("syncErr == nil" / "closeErr == nil") log the failure message on success.
func TestGk_subflux_u29_AtomicWriteFileMode_noSpuriousDirSyncLogs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.txt")
	logs := gk_subflux_u29_captureDebugLogs(t, func() {
		if err := AtomicWriteFileMode(context.Background(), path, []byte("payload"), 0o644); err != nil {
			t.Fatalf("AtomicWriteFileMode(%q) = %v, want nil", path, err)
		}
	})
	if got, err := os.ReadFile(path); err != nil || string(got) != "payload" {
		t.Fatalf("ReadFile(%q) = %q, %v; want %q, nil", path, got, err, "payload")
	}
	if strings.Contains(logs, "parent dir fsync failed") {
		t.Errorf("AtomicWriteFileMode success emitted %q; dir fsync succeeded so the guard must stay false", "parent dir fsync failed")
	}
	if strings.Contains(logs, "parent dir close failed") {
		t.Errorf("AtomicWriteFileMode success emitted %q; dir close succeeded so the guard must stay false", "parent dir close failed")
	}
}

// TestGk_subflux_u29_CommitAtomicWrite_noSpuriousDirSyncLogs kills
// atomicwrite.go:236 and :240 (the same parent-dir fsync/close guards inside
// CommitAtomicWrite).
func TestGk_subflux_u29_CommitAtomicWrite_noSpuriousDirSyncLogs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "committed.txt")
	tmpPath, cleanup, err := PrepareAtomicWrite(context.Background(), path, []byte("commit-payload"))
	if err != nil {
		t.Fatalf("PrepareAtomicWrite(%q) = %v, want nil", path, err)
	}
	_ = cleanup // CommitAtomicWrite renames the temp away; do not invoke cleanup.
	logs := gk_subflux_u29_captureDebugLogs(t, func() {
		if err := CommitAtomicWrite(tmpPath, path); err != nil {
			t.Fatalf("CommitAtomicWrite(%q, %q) = %v, want nil", tmpPath, path, err)
		}
	})
	if got, err := os.ReadFile(path); err != nil || string(got) != "commit-payload" {
		t.Fatalf("ReadFile(%q) = %q, %v; want %q, nil", path, got, err, "commit-payload")
	}
	if strings.Contains(logs, "parent dir fsync failed") {
		t.Errorf("CommitAtomicWrite success emitted %q; dir fsync succeeded", "parent dir fsync failed")
	}
	if strings.Contains(logs, "parent dir close failed") {
		t.Errorf("CommitAtomicWrite success emitted %q; dir close succeeded", "parent dir close failed")
	}
}

// TestGk_subflux_u29_PrepareAtomicWrite_cleanupNoSpuriousLog kills
// atomicwrite.go:182 (CONDITIONALS_NEGATION on the doCleanup "rmErr != nil"
// guard). cleanup() removes the existing temp file successfully
// (rmErr == nil), so the original emits no log; the mutated guard
// ("rmErr == nil") logs the cleanup-failure message on a successful Remove.
func TestGk_subflux_u29_PrepareAtomicWrite_cleanupNoSpuriousLog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "prepared.txt")
	tmpPath, cleanup, err := PrepareAtomicWrite(context.Background(), path, []byte("x"))
	if err != nil {
		t.Fatalf("PrepareAtomicWrite(%q) = %v, want nil", path, err)
	}
	if _, statErr := os.Stat(tmpPath); statErr != nil {
		t.Fatalf("temp file %q should exist before cleanup: %v", tmpPath, statErr)
	}
	logs := gk_subflux_u29_captureDebugLogs(t, func() { cleanup() })
	if _, statErr := os.Stat(tmpPath); statErr == nil {
		t.Errorf("temp file %q should be removed after cleanup()", tmpPath)
	}
	if strings.Contains(logs, "temp file cleanup failed") {
		t.Errorf("successful cleanup emitted %q; Remove of an existing temp succeeded", "temp file cleanup failed")
	}
}
