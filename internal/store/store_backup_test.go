package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestBackupInto_producesConsistentSnapshot(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	ctx := context.Background()
	if _, err := db.db.ExecContext(ctx,
		`INSERT INTO subtitle_state (media_type, media_id, language, provider, path)
		 VALUES ('movie','tmdb-1','en','os','/x.srt')`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "backup.db")
	if err := db.BackupInto(ctx, dest); err != nil {
		t.Fatalf("BackupInto: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("backup file missing: %v", err)
	}

	// Open the snapshot independently; it must be a valid, standalone DB
	// containing the row.
	snap, err := Open(ctx, dest)
	if err != nil {
		t.Fatalf("open snapshot: %v", err)
	}
	t.Cleanup(func() { snap.Close(ctx) })
	var n int
	if err := snap.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM subtitle_state`).Scan(&n); err != nil {
		t.Fatalf("query snapshot: %v", err)
	}
	if n != 1 {
		t.Errorf("snapshot row count = %d, want 1", n)
	}
}
