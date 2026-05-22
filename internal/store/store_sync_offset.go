package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// SetSyncOffset updates the offset_ms for a subtitle file by path.
func (d *DB) SetSyncOffset(ctx context.Context, path string, offsetMs int64) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE subtitle_files SET offset_ms = ? WHERE path = ?`,
		offsetMs, path)
	if err != nil {
		return fmt.Errorf("set sync offset: %w", err)
	}
	return nil
}

// GetSyncOffset returns the current offset_ms for a subtitle file.
// Returns 0 if the path is not found.
func (d *DB) GetSyncOffset(ctx context.Context, path string) (int64, error) {
	var offset int64
	err := d.db.QueryRowContext(ctx,
		`SELECT offset_ms FROM subtitle_files WHERE path = ?`, path).Scan(&offset)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, nil
		}
		return 0, fmt.Errorf("get sync offset: %w", err)
	}
	return offset, nil
}
