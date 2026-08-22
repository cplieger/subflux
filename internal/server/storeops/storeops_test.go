package storeops

import (
	"bytes"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/atomicfile/v3"
)

func TestPruneBackups_keepsNewestAndSkipsLiveDB(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The live bbolt file has no dash, so the glob must never match/prune it.
	live := filepath.Join(dir, "subflux.bolt")
	if err := os.WriteFile(live, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	names := []string{ // oldest -> newest
		"subflux-20260101-000000.bolt",
		"subflux-20260102-000000.bolt",
		"subflux-20260103-000000.bolt",
		"subflux-20260104-000000.bolt",
	}
	for _, n := range names {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	pruneBackups(dir, 2)

	for _, gone := range names[:2] {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("%s should have been pruned", gone)
		}
	}
	for _, kept := range names[2:] {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s should have been kept: %v", kept, err)
		}
	}
	if _, err := os.Stat(live); err != nil {
		t.Errorf("live subflux.bolt must not be pruned: %v", err)
	}
}

// TestPruneBackups_successful_prune_is_silent: the prune-failed warning names
// a backup the operator still has to deal with, so a prune that removed
// everything it meant to must say nothing. Serial (default logger).
func TestPruneBackups_successful_prune_is_silent(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	dir := t.TempDir()
	for _, n := range []string{
		"subflux-20260101-000000.bolt",
		"subflux-20260102-000000.bolt",
	} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	pruneBackups(dir, 1)

	if strings.Contains(buf.String(), `msg="backup: prune failed"`) {
		t.Errorf("a successful prune reported a failure; log: %s", buf.String())
	}
}

// TestEnforceBackupMode_bringsAWiderStoredModeBackToOwnerOnly pins that the
// finished snapshot's mode is what the filesystem STORED, not what the backup
// requested. Every snapshot is the whole bbolt file, so it carries the auth
// buckets — password hashes, passkey credentials, API keys — and a stored 0644
// publishes all of it.
//
// The drift is REAL rather than mocked: the file is genuinely group- and
// world-readable on disk before the call, which the witness asserts, so
// enforcement is what brings it back.
func TestEnforceBackupMode_bringsAWiderStoredModeBackToOwnerOnly(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "subflux-20260101-000000.bolt")
	if err := os.WriteFile(dest, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dest, 0o644); err != nil {
		t.Fatal(err)
	}
	wfi, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if wfi.Mode().Perm() != 0o644 {
		t.Fatalf("INVALID: snapshot stored %v for a 0644 request; the drift this test "+
			"exists to correct is not present on this filesystem", wfi.Mode().Perm())
	}

	if err := enforceBackupMode(dest); err != nil {
		t.Fatalf("enforceBackupMode: %v", err)
	}
	fi, err := os.Lstat(dest)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("snapshot mode = %v, want 0600: a readable copy of the auth store was left "+
			"on disk", got)
	}
}

// TestEnforceBackupMode_refusesASymlinkInsteadOfChmodingItsTarget pins the half
// of the change a mode assertion cannot see. os.Chmod resolves the pathname, so
// a symlink left at the snapshot name made the old code chmod whatever it
// pointed at — someone else's file, tightened to 0600 by the backup goroutine.
// The handle is opened O_NOFOLLOW, so the kernel refuses the name outright and
// the victim is untouched.
func TestEnforceBackupMode_refusesASymlinkInsteadOfChmodingItsTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(victim, 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "subflux-20260101-000000.bolt")
	if err := os.Symlink(victim, dest); err != nil {
		t.Fatal(err)
	}

	err := enforceBackupMode(dest)
	if err == nil {
		t.Fatal("a symlink at the snapshot name was accepted; want the kernel refusal")
	}
	if !errors.Is(err, atomicfile.ErrSymlinkTarget) {
		t.Errorf("error = %v, want atomicfile.ErrSymlinkTarget", err)
	}
	fi, err := os.Lstat(victim)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o644 {
		t.Errorf("victim mode = %v, want 0644 untouched: the enforcement followed the symlink", got)
	}
}

// TestAtomicfileIsTheV3Module pins which major every atomicfile call site
// resolves to. /v2 and /v3 are distinct modules that can coexist in one build,
// so a file left behind on /v2 during the major migration compiles silently
// while missing v3's unconditional symlink refusal and temp-side fsync — the
// guarantees the test above and the config writes rely on.
func TestAtomicfileIsTheV3Module(t *testing.T) {
	t.Parallel()
	const want = "github.com/cplieger/atomicfile/v3"
	if got := reflect.TypeFor[atomicfile.PendingFile]().PkgPath(); got != want {
		t.Errorf("atomicfile package path = %q, want %q", got, want)
	}
}
