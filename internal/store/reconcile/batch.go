package reconcile

import (
	"context"
	"fmt"
	"log/slog"

	"subflux/internal/api"
	"subflux/internal/store/txutil"
)

// batchDelete collects video paths from entries with missing videos
// and delegates to DBTX.DeleteStateByPaths for atomic DB cleanup.
func batchDelete(ctx context.Context, db DBTX, entries []Entry) (api.CleanupResult, error) {
	videoPaths := make([]string, 0, len(entries))
	for _, e := range entries {
		videoPaths = append(videoPaths, e.VideoPath)
	}
	return db.DeleteStateByPaths(ctx, videoPaths)
}

// batchReset handles groups where the video exists but some or all
// subtitle files are missing. Uses a single transaction for all groups
// to avoid N+1 transaction overhead.
func batchReset(ctx context.Context, db DBTX,
	missing map[GroupKey][]Entry, present map[GroupKey]bool) (int64, error) {

	if len(missing) == 0 {
		return 0, nil
	}

	tx, err := db.BeginTx(ctx)
	if err != nil {
		return 0, fmt.Errorf("reconcile: begin tx for batch reset: %w", err)
	}
	defer txutil.DeferRollback(tx)

	var resetCount int64
	for k, entries := range missing {
		if present[k] {
			// Video exists and some subs present: delete only missing sub rows.
			for _, e := range entries {
				if _, err := tx.ExecContext(ctx,
					`DELETE FROM subtitle_state WHERE id = ?`, e.ID); err != nil {
					return resetCount, fmt.Errorf("reconcile: delete missing sub id %d: %w", e.ID, err)
				}
				slog.Info("reconcile: deleted row for missing subtitle (lock preserved)",
					"media_id", k.ID, "lang", k.Lang, "path", e.SubPath)
			}
		} else {
			// All subs missing: reset auto rows, delete manual rows.
			for _, e := range entries {
				if e.Manual {
					if _, err := tx.ExecContext(ctx,
						`DELETE FROM subtitle_state WHERE id = ?`, e.ID); err != nil {
						return resetCount, fmt.Errorf("reconcile: delete manual row id %d: %w", e.ID, err)
					}
				} else {
					if _, err := tx.ExecContext(ctx, `
						UPDATE subtitle_state
						SET path = '', score = 0, provider = '', release_name = '',
						    media_imported = CURRENT_TIMESTAMP
						WHERE id = ?`, e.ID); err != nil {
						return resetCount, fmt.Errorf("reconcile: reset id %d: %w", e.ID, err)
					}
				}
			}
			if _, err := tx.ExecContext(ctx, `
				DELETE FROM search_attempts
				WHERE media_type = ? AND media_id = ? AND language = ?`,
				k.Typ, k.ID, k.Lang); err != nil {
				slog.Warn("reconcile: failed to clear attempts",
					"media_id", k.ID, "error", err)
			}
			resetCount++
			slog.Info("reconcile: all subtitles missing, reset for re-search",
				"media_id", k.ID, "lang", k.Lang)
		}
	}

	if err := tx.Commit(); err != nil {
		return resetCount, fmt.Errorf("reconcile: commit batch reset: %w", err)
	}
	return resetCount, nil
}
