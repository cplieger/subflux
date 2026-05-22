package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"subflux/internal/api"
)

// Compile-time assertion: *DB satisfies api.PollStore.
var _ api.PollStore = (*DB)(nil)

// GetPollTimestamp returns the last poll timestamp for a given key (e.g. "sonarr", "radarr").
// Returns zero time and nil error if no timestamp has been stored yet.
func (d *DB) GetPollTimestamp(ctx context.Context, key api.PollKey) (time.Time, error) {
	if !key.Valid() {
		return time.Time{}, fmt.Errorf("get poll timestamp: invalid key %q", key)
	}
	var val string
	err := d.stmtGetPollTimestamp.QueryRowContext(ctx, string(key)).Scan(&val)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return time.Time{}, nil
		}
		return time.Time{}, fmt.Errorf("get poll timestamp %q: %w", key, err)
	}
	t, err := time.Parse(time.RFC3339Nano, val)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse poll timestamp %q: %w", key, err)
	}
	return t, nil
}

// SetPollTimestamp stores the last poll timestamp for a given key.
func (d *DB) SetPollTimestamp(ctx context.Context, key api.PollKey, t time.Time) error {
	if !key.Valid() {
		return fmt.Errorf("set poll timestamp: invalid key %q", key)
	}
	_, err := d.stmtSetPollTimestamp.ExecContext(ctx,
		string(key), t.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("set poll timestamp %q: %w", key, err)
	}
	return nil
}
