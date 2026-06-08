package store

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
)

// Compile-time assertion: *DB satisfies api.ManualLockStore.
var _ api.ManualLockStore = (*DB)(nil)

// IsManuallyLocked checks if a media+language has any manual override,
// meaning it should be excluded from all automated actions.
func (d *DB) IsManuallyLocked(ctx context.Context, mediaType api.MediaType, mediaID, language string) (bool, error) {
	var exists bool
	if err := d.db.QueryRowContext(ctx, `
		SELECT EXISTS(
			SELECT 1 FROM subtitle_state
			WHERE media_type = ? AND media_id = ? AND language = ? AND manual = 1
		)`,
		mediaType, mediaID, language).Scan(&exists); err != nil {
		return false, fmt.Errorf("IsManuallyLocked query: %w", err)
	}
	return exists, nil
}

// ClearManualLock removes the manual lock for a media+language,
// allowing automated scans and upgrades to resume.
func (d *DB) ClearManualLock(ctx context.Context, mediaType api.MediaType, mediaID, language string) error {
	slog.Debug("ClearManualLock",
		"media_type", mediaType, "media_id", mediaID, "lang", language)
	_, err := d.db.ExecContext(ctx, `
		UPDATE subtitle_state SET manual = 0
		WHERE media_type = ? AND media_id = ? AND language = ? AND manual = 1`,
		mediaType, mediaID, language)
	return err
}

// ManualDownloadCount returns how many manual downloads exist for a media+language.
func (d *DB) ManualDownloadCount(ctx context.Context, mediaType api.MediaType, mediaID, language string) (int, error) {
	var count int
	if err := d.stmtManualDownloadCount.QueryRowContext(ctx,
		mediaType, mediaID, language).Scan(&count); err != nil {
		return 0, fmt.Errorf("ManualDownloadCount query: %w", err)
	}
	if count > 0 {
		slog.Debug("ManualDownloadCount",
			"media_id", mediaID, "lang", language, "count", count)
	}
	return count, nil
}

// ManualSubtitlePaths returns the subtitle file paths from all manual
// download rows for a media+language. Used by maybeRevertManualLock to
// check which manual files still exist on disk.
func (d *DB) ManualSubtitlePaths(ctx context.Context, mediaType api.MediaType, mediaID, language string) ([]string, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT path FROM subtitle_state
		WHERE media_type = ? AND media_id = ? AND language = ? AND manual = 1
		  AND path != ''`,
		mediaType, mediaID, language)
	if err != nil {
		return nil, fmt.Errorf("ManualSubtitlePaths query: %w", err)
	}
	defer rows.Close()
	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, fmt.Errorf("ManualSubtitlePaths scan: %w", err)
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ManualSubtitlePaths rows: %w", err)
	}
	return paths, nil
}

// NextManualNumber atomically returns the next manual subtitle file number
// for a media+language. Uses MAX+1 in a single query so concurrent callers
// cannot get the same number (SQLite serializes writes via WAL).
//
// Supports manual paths in both forms produced by ManualSubtitlePath:
//
//   - movie.fr.N.srt        (standard variant)
//   - movie.fr.hi.N.srt     (HI variant)
//   - movie.fr.forced.N.srt (forced variant)
//
// The index N is always the last component before .srt. Using
// rtrim(...,'0123456789') + substr gives us the suffix starting just
// before the number, from which we extract the integer. This is
// portable SQL, avoids fragile INSTR/SUBSTR arithmetic against a
// language token, and handles the variant forms uniformly.
func (d *DB) NextManualNumber(ctx context.Context, mediaType api.MediaType, mediaID, language string) int {
	var n int
	if err := d.db.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(
			CAST(
				REPLACE(
					SUBSTR(path, LENGTH(RTRIM(REPLACE(path, '.srt', ''), '0123456789')) + 1),
					'.srt', ''
				) AS INTEGER
			)
		), 0) + 1
		FROM subtitle_state
		WHERE media_type = ? AND media_id = ? AND language = ? AND manual = 1
		  AND path LIKE '%.'||?||'.%'`,
		mediaType, mediaID, language, language).Scan(&n); err != nil {
		slog.Warn("NextManualNumber query failed, falling back to count",
			"error", err)
		count, countErr := d.ManualDownloadCount(ctx, mediaType, mediaID, language)
		if countErr != nil {
			return 1
		}
		return count + 1
	}
	return n
}

// GetManualLocks returns all media+language pairs with manual overrides.
func (d *DB) GetManualLocks(ctx context.Context) ([]api.ManualLockEntry, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT media_type, media_id, language, COUNT(*) as cnt
		FROM subtitle_state
		WHERE manual = 1
		GROUP BY media_type, media_id, language
		ORDER BY media_type, media_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []api.ManualLockEntry
	for rows.Next() {
		var e api.ManualLockEntry
		if err := rows.Scan(&e.MediaType, &e.MediaID,
			&e.Language, &e.Count); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
