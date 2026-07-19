package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/config"
)

// Custody tests for the admin socket directory (R1.5c): the 0700 directory
// created by the atomic Mkdir is the security boundary — pre-existing
// non-compliant entries (wrong mode, symlink, non-directory) are refused,
// never chmod'd into compliance.

func TestEnsureAdminSocketDir_creates0700(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "subflux-admin")
	if err := ensureAdminSocketDir(dir); err != nil {
		t.Fatalf("ensureAdminSocketDir: %v", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !fi.IsDir() {
		t.Fatal("not a directory")
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Fatalf("directory mode = %v, want 0700", got)
	}
}

func TestEnsureAdminSocketDir_acceptsCompliantExisting(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "subflux-admin")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := ensureAdminSocketDir(dir); err != nil {
		t.Fatalf("compliant pre-existing dir refused: %v", err)
	}
}

func TestEnsureAdminSocketDir_refusesWrongMode(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "subflux-admin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Mkdir applies umask; force the wide mode explicitly.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	err := ensureAdminSocketDir(dir)
	if err == nil {
		t.Fatal("0755 pre-existing dir accepted; custody requires refusal, not chmod")
	}
	// The dir must NOT have been chmod'd into compliance.
	fi, statErr := os.Lstat(dir)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode changed to %v; refusal must not mutate the foreign dir", fi.Mode().Perm())
	}
}

func TestEnsureAdminSocketDir_refusesSymlink(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	target := filepath.Join(base, "target")
	if err := os.Mkdir(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "subflux-admin")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := ensureAdminSocketDir(link); err == nil {
		t.Fatal("symlinked admin dir accepted; want refusal")
	}
}

func TestEnsureAdminSocketDir_refusesNonDirectory(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "subflux-admin")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ensureAdminSocketDir(dir); err == nil {
		t.Fatal("plain file at admin dir path accepted; want refusal")
	}
}

// TestAdminSocketListener_bindsAndClearsStaleSocket verifies the listener
// setup end-to-end: the 0700 dir is created, a stale socket file left by an
// unclean exit is removed before bind, and the live socket answers an HTTP
// round-trip.
func TestAdminSocketListener_bindsAndClearsStaleSocket(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "subflux-admin")
	sock := filepath.Join(dir, "admin.sock")
	ctx := context.Background()

	// First bind, then abandon WITHOUT unlink by closing only after copying
	// the file into place is impossible for a unix socket — simulate the
	// stale file directly instead: net.Listen refuses an existing path.
	if err := ensureAdminSocketDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sock, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	ln, err := adminSocketListener(ctx, dir, sock)
	if err != nil {
		t.Fatalf("adminSocketListener with stale socket file: %v", err)
	}
	t.Cleanup(func() { ln.Close() })

	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", fi.Mode().Perm())
	}

	srv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() { _ = srv.Serve(ln) }() // returns on Close
	t.Cleanup(func() { srv.Close() })

	client := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return (&net.Dialer{}).DialContext(ctx, "unix", sock)
			},
		},
	}
	resp, err := client.Get("http://admin.sock/ping")
	if err != nil {
		t.Fatalf("round-trip over fresh socket: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
}

// TestAdminSocketListener_refusedDirLeavesNoListener pins degraded mode: a
// non-compliant directory yields an error and no socket file.
func TestAdminSocketListener_refusedDirLeavesNoListener(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	dir := filepath.Join(base, "subflux-admin")
	if err := os.WriteFile(dir, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(dir, "admin.sock")
	if _, err := adminSocketListener(context.Background(), dir, sock); err == nil {
		t.Fatal("want error for non-directory custody path")
	}
	// The parent is a plain file, so the lstat fails with ENOTDIR (or
	// ENOENT on some platforms) — either way the socket cannot exist.
	if _, err := os.Lstat(sock); err == nil {
		t.Fatal("socket file exists after refusal")
	}
}

// TestAdminSocketConstants pins the wire-facing constants the CLI and server
// share (the generated apipaths constant is gone with the wirespec entry).
func TestAdminSocketConstants(t *testing.T) {
	t.Parallel()
	if config.AdminSocketDir != "/tmp/subflux-admin" {
		t.Errorf("AdminSocketDir = %q", config.AdminSocketDir)
	}
	if config.AdminSocketPath != "/tmp/subflux-admin/admin.sock" {
		t.Errorf("AdminSocketPath = %q", config.AdminSocketPath)
	}
	if config.AdminBootstrapURLPath != "/api/admin/bootstrap" {
		t.Errorf("AdminBootstrapURLPath = %q", config.AdminBootstrapURLPath)
	}
	if strings.HasPrefix(config.AdminSocketPath, "/config") {
		t.Error("admin socket must not live under the /config volume")
	}
}
