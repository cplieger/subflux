package main

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
		t.Error("not a directory")
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("directory mode = %v, want 0700", got)
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
	ctx := t.Context()

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
		t.Errorf("dir mode = %v, want 0700", fi.Mode().Perm())
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
		t.Errorf("status = %d, want 204", resp.StatusCode)
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
	if _, err := adminSocketListener(t.Context(), dir, sock); err == nil {
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

// TestEnsureAdminSocketDir_verifiesTheModeItCreated pins the bug that adopting
// atomicfile.EnsurePrivateDir fixed. Every check in the old implementation ran
// only on the already-exists path: when its own os.Mkdir succeeded it returned
// nil having verified nothing, so on a filesystem that does not store the mode
// asked for, the directory gating the admin socket was born group-accessible
// and no error was raised anywhere.
//
// The widening here is REAL rather than mocked: Linux propagates S_ISGID from a
// setgid parent to a new subdirectory, so os.Mkdir(…, 0o700) genuinely stores a
// mode it was not asked for. The witness assertion below fails the test as
// INVALID rather than passing it vacuously if the kernel ever stops doing that,
// because a create-path test on a filesystem that honours every mode cannot
// distinguish a verified create from an unverified one.
func TestEnsureAdminSocketDir_verifiesTheModeItCreated(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	if err := os.Chmod(parent, 0o700|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}

	witness := filepath.Join(parent, "witness")
	if err := os.Mkdir(witness, 0o700); err != nil {
		t.Fatal(err)
	}
	wfi, err := os.Lstat(witness)
	if err != nil {
		t.Fatal(err)
	}
	if wfi.Mode()&os.ModeSetgid == 0 {
		t.Skipf("kernel did not widen a 0o700 mkdir under a setgid parent (got %v); "+
			"this test cannot distinguish a verified create from an unverified one here", wfi.Mode())
	}

	dir := filepath.Join(parent, "subflux-admin")
	if err := ensureAdminSocketDir(dir); err != nil {
		t.Fatalf("ensureAdminSocketDir: %v", err)
	}
	fi, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode(); got != os.ModeDir|0o700 {
		t.Fatalf("created dir mode = %v, want %v: the mode it created was not verified",
			got, os.ModeDir|0o700)
	}
}

// fakeAdminHost stands in for *server.Server: it exposes the same three-method
// surface startAdminSocket consumes, and its GoBackground mirrors the server's
// background WaitGroup so a test can join what the composition root registered.
// finished flips only inside the wrapper, so a goroutine started outside
// GoBackground can never set it.
type fakeAdminHost struct {
	wg       sync.WaitGroup
	finished atomic.Bool
	alerts   atomic.Int32
}

func (f *fakeAdminHost) AdminHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
}

func (f *fakeAdminHost) RecordPersistentAlert(string, string) { f.alerts.Add(1) }

func (f *fakeAdminHost) GoBackground(fn func()) {
	f.wg.Go(func() {
		fn()
		f.finished.Store(true)
	})
}

// TestStartAdminSocket_joinsItsServerOnTheBackgroundSet pins the observable
// completion: the admin plane's graceful drain runs on the server's background
// goroutine set, so the drain report that set gates cannot be emitted while the
// socket is still serving. A goroutine nothing holds satisfies the round-trip
// assertions below and leaves finished false, because only the GoBackground
// wrapper sets it.
//
// It also pins the ordering the join depends on: the wait is entered only after
// the context is cancelled. webhttp.Run serves until then, so a wait taken any
// earlier would block for the whole process lifetime.
func TestStartAdminSocket_joinsItsServerOnTheBackgroundSet(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "subflux-admin")
	sock := filepath.Join(dir, "admin.sock")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	host := &fakeAdminHost{}
	startAdminSocket(ctx, host, dir, sock)

	if got := host.alerts.Load(); got != 0 {
		t.Fatalf("persistent alerts = %d, want 0: setup should have succeeded", got)
	}

	// Prove it is really serving before shutdown, so the join below is joining
	// a live server rather than an already-dead goroutine.
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
		t.Fatalf("round-trip over admin socket: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if host.finished.Load() {
		t.Fatal("background goroutine already finished while the socket was serving")
	}

	cancel()

	joined := make(chan struct{})
	go func() { host.wg.Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(30 * time.Second):
		t.Fatal("background set did not drain within 30s after cancellation")
	}

	if !host.finished.Load() {
		t.Error("background set drained without the admin socket's shutdown: " +
			"its goroutine is not registered on the server's WaitGroup")
	}
	if _, err := os.Lstat(sock); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("socket file still present after the join (lstat err = %v); "+
			"the drain and its cleanup must complete before the wait returns", err)
	}
}
