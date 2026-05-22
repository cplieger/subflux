package reconcile

import (
	"context"
	"fmt"
	"log/slog"

	"golang.org/x/sync/errgroup"

	"subflux/internal/api"
)

// DBTX is the narrow database interface required by the reconcile package.
type DBTX interface {
	QueryContext(ctx context.Context, query string, args ...any) (Rows, error)
	BeginTx(ctx context.Context) (Tx, error)
	// DeleteStateByPaths delegates bulk video-path deletion to the parent store.
	DeleteStateByPaths(ctx context.Context, videoPaths []string) (api.CleanupResult, error)
}

// Rows abstracts *sql.Rows.
type Rows interface {
	Next() bool
	Scan(dest ...any) error
	Close() error
	Err() error
}

// Tx abstracts *sql.Tx.
type Tx interface {
	ExecContext(ctx context.Context, query string, args ...any) (Result, error)
	Commit() error
	Rollback() error
}

// Result abstracts sql.Result.
type Result interface {
	RowsAffected() (int64, error)
}

// Classified holds the result of classifying all entries.
type Classified struct {
	SubMissing map[GroupKey][]Entry
	SubPresent map[GroupKey]bool
	ToDelete   []Entry
}

// Classify classifies entries into action groups using bounded concurrency.
// Returns (Classified, error); a non-nil error indicates context cancellation.
func Classify(ctx context.Context, entries []Entry, statFn StatFunc) (Classified, error) {
	type classifiedEntry struct {
		action Action
		entry  Entry
	}
	classified := make([]classifiedEntry, len(entries))

	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(8)

	for idx := range entries {
		g.Go(func() error {
			if ctx.Err() != nil {
				classified[idx] = classifiedEntry{entry: entries[idx], action: ActionSkip}
				return ctx.Err()
			}
			classified[idx] = classifiedEntry{
				entry:  entries[idx],
				action: ClassifyEntry(&entries[idx], statFn),
			}
			return nil
		})
	}

	err := g.Wait()

	result := Classified{
		SubMissing: make(map[GroupKey][]Entry),
		SubPresent: make(map[GroupKey]bool),
	}
	for _, ce := range classified {
		k := GroupKey{Typ: ce.entry.MediaType, ID: ce.entry.MediaID, Lang: ce.entry.Language}
		switch ce.action {
		case ActionDelete:
			result.ToDelete = append(result.ToDelete, ce.entry)
		case ActionSubMissing:
			result.SubMissing[k] = append(result.SubMissing[k], ce.entry)
		case ActionSubPresent:
			result.SubPresent[k] = true
		}
	}
	return result, err
}

// Run checks subtitle_state entries against the filesystem and cleans up
// stale records. Returns paths removed from DB and reset count.
func Run(ctx context.Context, db DBTX, statFn StatFunc) (api.ReconcileResult, error) {
	entries, err := loadEntries(ctx, db)
	if err != nil {
		return api.ReconcileResult{}, err
	}
	if len(entries) == 0 {
		return api.ReconcileResult{}, nil
	}

	c, err := Classify(ctx, entries, statFn)
	if err != nil {
		return api.ReconcileResult{}, err
	}

	slog.Info("reconcile: classified entries",
		"total_delete", len(c.ToDelete),
		"groups_sub_missing", len(c.SubMissing),
		"groups_sub_present", len(c.SubPresent))

	deleted, err := batchDelete(ctx, db, c.ToDelete)
	if err != nil {
		return api.ReconcileResult{Deleted: deleted}, err
	}

	resetCount, err := batchReset(ctx, db, c.SubMissing, c.SubPresent)
	return api.ReconcileResult{Deleted: deleted, ResetCount: resetCount}, err
}

// loadEntries loads all subtitle_state rows with a non-empty video_path.
func loadEntries(ctx context.Context, db DBTX) ([]Entry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, path, video_path, media_type, media_id, language, manual
		 FROM subtitle_state
		 WHERE video_path != ''`)
	if err != nil {
		return nil, err
	}

	var entries []Entry
	for rows.Next() {
		if len(entries)%100 == 0 && ctx.Err() != nil {
			rows.Close()
			return nil, ctx.Err()
		}
		var e Entry
		var manualInt int
		if scanErr := rows.Scan(&e.ID, &e.SubPath, &e.VideoPath,
			&e.MediaType, &e.MediaID, &e.Language, &manualInt); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("reconcile: scan row: %w", scanErr)
		}
		e.Manual = manualInt == 1
		entries = append(entries, e)
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, fmt.Errorf("reconcile: close rows: %w", closeErr)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}
	return entries, nil
}
