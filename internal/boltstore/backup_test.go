package boltstore

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// This file covers the task-7.1 backup behaviour: BackupInto produces a single
// consistent bbolt snapshot of the whole file (core plus auth buckets) that
// reopens via boltstore.Open and round-trips the same data, and the
// server-side artifact name "subflux-<ts>.bolt" matches the prune glob
// "subflux-*.bolt" (Requirement 11.1).

// backupArtifactName mirrors the name internal/server/backup.go builds for each
// snapshot: "subflux-<ts>.bolt" with the same timestamp layout. Keeping the
// format here (rather than importing the heavy server package into a store
// test) lets the test assert the naming contract and that it matches the prune
// glob.
func backupArtifactName(t time.Time) string {
	return "subflux-" + t.Format("20060102-150405") + ".bolt"
}

// TestBackupInto_roundTripsThroughOpen seeds a download, backs the store up to
// a "subflux-<ts>.bolt" artifact, reopens the snapshot independently via Open
// while the original is still open (hot backup), and asserts the data
// round-trips.
func TestBackupInto_roundTripsThroughOpen(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()

	// Seed one auto download row so the snapshot has observable content.
	if err := db.SaveDownload(ctx, autoRec(testProv, "Release.A", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	name := backupArtifactName(time.Now())
	dest := filepath.Join(t.TempDir(), name)

	if err := db.BackupInto(ctx, dest); err != nil {
		t.Fatalf("BackupInto: %v", err)
	}

	// The artifact exists at the expected path.
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("backup artifact missing: %v", err)
	}

	// The artifact name matches the prune glob the server uses, and the
	// "subflux-<ts>.bolt" shape exactly.
	if ok, _ := filepath.Match("subflux-*.bolt", name); !ok {
		t.Errorf("artifact %q does not match prune glob subflux-*.bolt", name)
	}
	if !regexp.MustCompile(`^subflux-\d{8}-\d{6}\.bolt$`).MatchString(name) {
		t.Errorf("artifact %q does not match subflux-<ts>.bolt", name)
	}

	// The snapshot opens as an independent, valid bbolt store while the
	// original handle is still open (hot backup), and contains the same row.
	snap, err := Open(dest)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	t.Cleanup(func() { _ = snap.Close(context.Background()) })

	score, _, found, err := snap.CurrentScore(ctx, testMT, testMID, testLang)
	if err != nil {
		t.Fatalf("CurrentScore on snapshot: %v", err)
	}
	if !found {
		t.Fatal("snapshot CurrentScore: found = false, want true (row should round-trip)")
	}
	if score != 80 {
		t.Errorf("snapshot CurrentScore = %d, want 80", score)
	}
}

// TestBackupInto_overwritesExistingDest asserts the temp-then-rename write
// commits onto an existing dest path (the rename replaces it), so a re-run to
// the same name does not fail the way the old dest-must-not-exist VACUUM INTO
// did.
func TestBackupInto_overwritesExistingDest(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	if err := db.SaveDownload(ctx, autoRec(testProv, "Release.A", "/media/test.fr.srt", 80)); err != nil {
		t.Fatalf("SaveDownload: %v", err)
	}

	dest := filepath.Join(t.TempDir(), backupArtifactName(time.Now()))
	// Pre-create a stale file at dest; the atomic rename must replace it.
	if err := os.WriteFile(dest, []byte("stale"), 0o600); err != nil {
		t.Fatalf("seed stale dest: %v", err)
	}

	if err := db.BackupInto(ctx, dest); err != nil {
		t.Fatalf("BackupInto over existing dest: %v", err)
	}

	snap, err := Open(dest)
	if err != nil {
		t.Fatalf("open snapshot after overwrite: %v", err)
	}
	t.Cleanup(func() { _ = snap.Close(context.Background()) })
	if _, _, found, err := snap.CurrentScore(ctx, testMT, testMID, testLang); err != nil || !found {
		t.Fatalf("snapshot after overwrite: found=%v err=%v, want found=true", found, err)
	}
}

// TestBackupInto_cancelledContext asserts a pre-cancelled context is honoured
// before any file is created.
func TestBackupInto_cancelledContext(t *testing.T) {
	db, _ := openTemp(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dest := filepath.Join(t.TempDir(), backupArtifactName(time.Now()))
	if err := db.BackupInto(ctx, dest); err == nil {
		t.Error("BackupInto with cancelled ctx: err = nil, want context error")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("dest exists after cancelled backup: stat err = %v, want not-exist", err)
	}
}
