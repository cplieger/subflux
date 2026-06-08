package store

import (
	"context"
	"log/slog"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/txutil"
)

// Compile-time assertion: *DB satisfies api.BackoffStore.
var _ api.BackoffStore = (*DB)(nil)

// backoffScanner co-locates the search_attempts column list with its scan logic,
// preventing column-list drift between SELECT and scan.
var backoffScanner = txutil.TableScanner[api.BackoffEntry]{
	Columns: `media_type, media_id, language, provider, failures, last_tried, next_retry`,
	ScanInto: func(row interface{ Scan(...any) error }, e *api.BackoffEntry) error {
		return row.Scan(&e.MediaType, &e.MediaID, &e.Language,
			&e.Provider, &e.Failures, &e.LastTried, &e.NextRetry)
	},
}

// RecordNoResult records a no-result search for a specific provider with exponential backoff.
// Uses a single upsert with SQLite POWER() to compute next_retry inline,
// eliminating the round-trip and race window of the former two-query approach.
func (d *DB) RecordNoResult(ctx context.Context, mediaType api.MediaType, mediaID, language string, providerName api.ProviderID,
	bp api.BackoffParams,
) error {
	now := time.Now()
	nextRetry := now.Add(bp.InitialDelay)
	initialDelaySec := bp.InitialDelay.Seconds()
	maxDelaySec := bp.MaxDelay.Seconds()

	_, err := d.stmtRecordNoRes.ExecContext(ctx,
		mediaType, mediaID, language, providerName, now,
		nextRetry, int64(maxDelaySec), int64(initialDelaySec), bp.Multiplier)
	if err != nil {
		return err
	}

	slog.Debug("recorded no-result backoff",
		"media_id", mediaID, "lang", language, "provider", providerName)
	return nil
}

// BackedOffProviders returns the names of providers that should be skipped
// for a given media+language due to adaptive backoff. A provider is backed
// off if its next_retry is in the future, or if it has reached maxAttempts.
// Providers not in the table are never backed off (new providers auto-eligible).
// maxAttempts must be non-negative; negative values are treated as 0 (disabled).
func (d *DB) BackedOffProviders(ctx context.Context, mediaType api.MediaType, mediaID, language string, maxAttempts int) ([]api.ProviderID, error) {
	if maxAttempts < 0 {
		maxAttempts = 0
	}
	rows, err := d.stmtBackedOff.QueryContext(ctx, mediaType, mediaID, language)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	now := time.Now()
	var backed []api.ProviderID
	for rows.Next() {
		var prov api.ProviderID
		var failures int
		var nextRetry time.Time
		if err := rows.Scan(&prov, &failures, &nextRetry); err != nil {
			return nil, err
		}
		if maxAttempts > 0 && failures >= maxAttempts {
			backed = append(backed, prov)
			continue
		}
		if now.Before(nextRetry) {
			backed = append(backed, prov)
		}
	}
	return backed, rows.Err()
}

// GetBackoffItems returns all items currently in adaptive search backoff.
func (d *DB) GetBackoffItems(ctx context.Context) ([]api.BackoffEntry, error) {
	rows, err := d.stmtGetBackoffItems.QueryContext(ctx)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []api.BackoffEntry
	for rows.Next() {
		var e api.BackoffEntry
		if err := backoffScanner.ScanInto(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// GetBackoffByPrefix returns backoff entries for media IDs matching a prefix.
// Used to show next retry times in the coverage UI.
func (d *DB) GetBackoffByPrefix(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.BackoffEntry, error) {
	//nolint:gosec // G202: Columns is a compile-time constant
	baseQuery := `SELECT ` + backoffScanner.Columns + `
		FROM search_attempts WHERE media_type = ? AND provider != ''`
	args := make([]any, 1, 2)
	args[0] = mediaType
	baseQuery, args = appendPrefixFilter(baseQuery, args, mediaIDPrefix)
	baseQuery += " ORDER BY media_id, next_retry ASC"

	rows, err := d.db.QueryContext(ctx, baseQuery, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []api.BackoffEntry
	for rows.Next() {
		var e api.BackoffEntry
		if err := backoffScanner.ScanInto(rows, &e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
