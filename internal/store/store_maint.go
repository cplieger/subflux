package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"

	"subflux/internal/api"
)

// Compile-time assertion: *DB satisfies api.MaintStore.
var _ api.MaintStore = (*DB)(nil)

// DeleteStateByPaths finds subtitle_state rows where video_path matches any
// of the given paths. Deletes those rows and their search_attempts entries.
// Returns the subtitle file paths from deleted rows so the caller can clean
// up files from disk as a fallback (the arr usually deletes them already).
func (d *DB) DeleteStateByPaths(ctx context.Context, videoPaths []string) (api.CleanupResult, error) {
	if len(videoPaths) == 0 {
		return api.CleanupResult{}, nil
	}

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return api.CleanupResult{}, fmt.Errorf("begin tx: %w", err)
	}
	defer deferRollback(tx)

	// Process videoPaths in batches to respect SQLite's 999-parameter limit.
	var subPaths []string
	var keys []MediaKey
	for batch := range batchSlice(videoPaths, sqliteBatchSize) {
		inClause, args := placeholders(batch)

		batchSubs, batchKeys, err := collectAffectedState(ctx, tx, inClause, args)
		if err != nil {
			return api.CleanupResult{}, err
		}
		subPaths = append(subPaths, batchSubs...)
		keys = append(keys, batchKeys...)

		if _, err := tx.ExecContext(ctx,
			"DELETE FROM subtitle_state WHERE video_path IN ("+inClause+")",
			args...); err != nil {
			return api.CleanupResult{}, fmt.Errorf("delete state: %w", err)
		}
	}

	// Clear search_attempts for each affected media+language in batches.
	for batch := range batchSlice(keys, sqliteBatchSize/3) {
		var b strings.Builder
		b.WriteString("DELETE FROM search_attempts WHERE ")
		attArgs := make([]any, 0, len(batch)*3)
		for i, k := range batch {
			if i > 0 {
				b.WriteString(" OR ")
			}
			b.WriteString("(media_type = ? AND media_id = ? AND language = ?)")
			attArgs = append(attArgs, k.typ, k.id, k.lang)
		}
		if _, err := tx.ExecContext(ctx, b.String(), attArgs...); err != nil {
			slog.Warn("failed to clear attempts batch", "error", err)
		}
	}

	// Clean up subtitle_files and scan_state for media items that no
	// longer have any subtitle_state rows.
	cleanOrphanedCoverage(ctx, tx, keys)

	if err := tx.Commit(); err != nil {
		return api.CleanupResult{}, fmt.Errorf("commit cleanup: %w", err)
	}

	if len(subPaths) > 0 {
		slog.Info("deleted state by video paths",
			"video_paths", len(videoPaths), "subtitle_paths", len(subPaths))
	}

	return api.CleanupResult{Paths: subPaths}, nil
}

// collectAffectedState queries subtitle paths and media keys for rows
// matching the given video paths within the transaction.
func collectAffectedState(ctx context.Context, tx *sql.Tx,
	inClause string, args []any) ([]string, []MediaKey, error) {

	var subPaths []string
	var keys []MediaKey
	rows, err := tx.QueryContext(ctx,
		"SELECT path, media_type, media_id, language FROM subtitle_state"+
			" WHERE video_path IN ("+inClause+") AND path != ''", args...)
	if err != nil {
		return nil, nil, fmt.Errorf("query paths: %w", err)
	}
	for rows.Next() {
		var p, mt, mid, lang string
		if scanErr := rows.Scan(&p, &mt, &mid, &lang); scanErr != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("scan path: %w", scanErr)
		}
		subPaths = append(subPaths, p)
		mk, err := NewMediaKey(api.MediaType(mt), mid, lang)
		if err != nil {
			slog.Warn("collectAffectedState: skipping row with invalid media type", "media_type", mt, "error", err)
			continue
		}
		keys = append(keys, mk)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, nil, fmt.Errorf("close rows: %w", closeErr)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, nil, rowsErr
	}
	return subPaths, keys, nil
}
