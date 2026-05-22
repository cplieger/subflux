package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"subflux/internal/api"
	"subflux/internal/store/txutil"
)

// defaultQueryLimit is the safety cap applied when callers pass Limit=0
// (meaning "no explicit limit"). Prevents unbounded allocation.
const defaultQueryLimit = 1000

// preallocCap is the maximum pre-allocation hint for the result slice.
// Avoids over-allocating when the requested limit is large.
const preallocCap = 256

// Compile-time assertion: *DB satisfies api.QueryStore.
var _ api.QueryStore = (*DB)(nil)

// Compile-time assertion: *DB satisfies api.HistoryStore.
var _ api.HistoryStore = (*DB)(nil)

// stateScanner co-locates the subtitle_state column list with its scan logic,
// preventing column-list drift between SELECT and scan.
var stateScanner = txutil.TableScanner[api.StateEntry]{
	Columns: `id, media_type, media_id, language, provider,
		release_name, score, path, title, imdb_id, season, episode,
		manual, media_imported`,
	ScanInto: func(row interface{ Scan(...any) error }, e *api.StateEntry) error {
		var manual int
		if err := row.Scan(&e.ID, &e.MediaType, &e.MediaID,
			&e.Language, &e.Provider, &e.ReleaseName, &e.Score,
			&e.Path, &e.Title, &e.ImdbID, &e.Season, &e.Episode,
			&manual, &e.MediaImported); err != nil {
			return err
		}
		e.Manual = manual == 1
		return nil
	},
}

// DownloadedRefs returns every distinct (release_name, provider) pair
// from history for this media+language. Empty release names are skipped
// (legacy rows from providers that did not expose a release name have
// release_name NULL or "", and an empty string can never match a search
// result's non-empty ReleaseName anyway).
//
// Used by the manual search popup to mark every previously-saved subtitle,
// not just the most recent one.
func (d *DB) DownloadedRefs(ctx context.Context, mediaType api.MediaType, mediaID, language string) ([]api.DownloadedRef, error) {
	rows, err := d.db.QueryContext(ctx, `
		SELECT DISTINCT release_name, provider FROM subtitle_state
		WHERE media_type = ? AND media_id = ? AND language = ?
		  AND release_name IS NOT NULL AND release_name <> ''`,
		mediaType, mediaID, language)
	if err != nil {
		return nil, fmt.Errorf("DownloadedRefs query: %w", err)
	}
	defer rows.Close()
	var out []api.DownloadedRef
	for rows.Next() {
		var ref api.DownloadedRef
		if err := rows.Scan(&ref.ReleaseName, &ref.Provider); err != nil {
			return nil, fmt.Errorf("DownloadedRefs scan: %w", err)
		}
		out = append(out, ref)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("DownloadedRefs rows: %w", err)
	}
	return out, nil
}

// CurrentScore returns the best auto-download score and media import time for a
// media+language pair. Returns found=false if no auto-download exists.
func (d *DB) CurrentScore(ctx context.Context, mediaType api.MediaType, mediaID, language string) (score int, mediaImported time.Time, found bool, err error) {
	err = d.stmtCurrentScore.QueryRowContext(ctx,
		mediaType, mediaID, language).Scan(&score, &mediaImported)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return 0, time.Time{}, false, nil
		}
		return 0, time.Time{}, false, err
	}
	return score, mediaImported, true, nil
}

// Stats returns basic DB statistics for monitoring.
func (d *DB) Stats(ctx context.Context) (downloads, attempts int, err error) {
	if err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM subtitle_state").Scan(&downloads); err != nil {
		return 0, 0, err
	}
	if err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM search_attempts").Scan(&attempts); err != nil {
		return 0, 0, err
	}
	return downloads, attempts, nil
}

// GetState returns subtitle state, most recent first.
// Accepts optional filters; zero-value fields mean no filter.
func (d *DB) GetState(ctx context.Context, q *api.StateQuery) ([]api.StateEntry, error) {

	slog.Debug("GetState",
		"media_type", q.MediaType, "lang", q.Language,
		"provider", q.Provider, "search", q.Search,
		"limit", q.Limit, "offset", q.Offset)

	query := `SELECT ` + stateScanner.Columns + ` FROM subtitle_state WHERE 1=1` //nolint:gosec // G202: compile-time columns
	var args []any
	if q.MediaType != "" {
		query += " AND media_type = ?"
		args = append(args, q.MediaType)
	}
	if q.Language != "" {
		query += " AND language = ?"
		args = append(args, q.Language)
	}
	if q.Provider != "" {
		query += " AND provider = ?"
		args = append(args, q.Provider)
	}
	if q.Search != "" {
		// Contains-match: escape LIKE wildcards in user input.
		query += " AND title LIKE ? ESCAPE '\\'"
		args = append(args, "%"+likeEscaper.Replace(q.Search)+"%")
	}
	query += " ORDER BY media_imported DESC"
	limit := q.Limit
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	} else {
		// Hard cap to prevent unbounded allocation when limit is unset.
		limit = defaultQueryLimit
		query += " LIMIT ?"
		args = append(args, limit)
	}
	if q.Offset > 0 {
		query += " OFFSET ?"
		args = append(args, q.Offset)
	}

	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cp := min(limit, preallocCap)
	out := make([]api.StateEntry, 0, cp)
	for rows.Next() {
		var e api.StateEntry
		if err := stateScanner.ScanInto(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	slog.Debug("GetState result", "count", len(out))
	return out, rows.Err()
}

// HistoryMediaIDs returns distinct media IDs that have download history
// matching the given type and optional prefix.
func (d *DB) HistoryMediaIDs(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]string, error) {
	q := `SELECT DISTINCT media_id FROM subtitle_state WHERE media_type = ?`
	args := []any{mediaType}
	q, args = appendPrefixFilter(q, args, mediaIDPrefix)
	rows, err := d.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
