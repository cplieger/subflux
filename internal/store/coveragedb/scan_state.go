package coveragedb

import (
	"context"
	"fmt"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/txutil"
)

// RecordScanState upserts the scan timestamp and metadata for a media item.
func (d *CoverageDB) RecordScanState(ctx context.Context, rec *api.ScanRecord) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO scan_state
			(media_type, media_id, title, season, episode, audio_lang, scanned_at)
		VALUES (?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT(media_type, media_id) DO UPDATE SET
			title = excluded.title,
			season = excluded.season,
			episode = excluded.episode,
			audio_lang = excluded.audio_lang,
			scanned_at = CURRENT_TIMESTAMP`,
		rec.MediaType, rec.MediaID, rec.Title, rec.Season, rec.Episode, rec.AudioLang)
	if err != nil {
		return fmt.Errorf("upsert scan_state: %w", err)
	}
	return nil
}

// GetScanStates returns scan state rows for a media type and optional prefix.
func (d *CoverageDB) GetScanStates(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.ScanStateRow, error) {
	query, args := txutil.AppendPrefixFilter(
		`SELECT media_id, title, season, episode, audio_lang, scanned_at
		FROM scan_state WHERE media_type = ?`,
		[]any{mediaType}, mediaIDPrefix, "media_id")

	rows, err := d.db.QueryContext(ctx, query+" ORDER BY media_id", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []api.ScanStateRow
	for rows.Next() {
		var r api.ScanStateRow
		if scanErr := rows.Scan(&r.MediaID, &r.Title, &r.Season, &r.Episode, &r.AudioLang, &r.ScannedAt); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentlyScanned returns a set of media IDs that were scanned after the
// given cutoff time.
func (d *CoverageDB) RecentlyScanned(ctx context.Context, cutoff time.Time) (map[string]bool, error) {
	rows, err := d.db.QueryContext(ctx,
		`SELECT media_id FROM scan_state WHERE scanned_at >= ?`,
		cutoff.UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// LastScanTime returns the most recent scanned_at timestamp from scan_state,
// or an empty string if no scans have been recorded.
func (d *CoverageDB) LastScanTime(ctx context.Context) (string, error) {
	var ts *string
	if err := d.db.QueryRowContext(ctx,
		"SELECT MAX(scanned_at) FROM scan_state").Scan(&ts); err != nil {
		return "", err
	}
	if ts == nil {
		return "", nil
	}
	return *ts, nil
}

// TotalSubtitleFiles returns the total number of subtitle files tracked.
func (d *CoverageDB) TotalSubtitleFiles(ctx context.Context) (int, error) {
	var count int
	if err := d.db.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM subtitle_files").Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
