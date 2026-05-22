// store_reconcile.go — thin delegation to internal/store/reconcile subpackage.
// The reconciliation subsystem has been extracted; see reconcile/ for implementation.

package store

import (
	"context"
	"database/sql"

	"subflux/internal/api"
	"subflux/internal/store/reconcile"
)

// reconcileAdapter adapts *DB to the reconcile.DBTX interface.
type reconcileAdapter struct {
	d *DB
}

func (a *reconcileAdapter) QueryContext(ctx context.Context, query string, args ...any) (reconcile.Rows, error) {
	return a.d.db.QueryContext(ctx, query, args...)
}

func (a *reconcileAdapter) BeginTx(ctx context.Context) (reconcile.Tx, error) {
	tx, err := a.d.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &txAdapter{tx}, nil
}

func (a *reconcileAdapter) DeleteStateByPaths(ctx context.Context, videoPaths []string) (api.CleanupResult, error) {
	return a.d.DeleteStateByPaths(ctx, videoPaths)
}

// txAdapter wraps *sql.Tx to satisfy reconcile.Tx.
type txAdapter struct {
	tx *sql.Tx
}

func (t *txAdapter) ExecContext(ctx context.Context, query string, args ...any) (reconcile.Result, error) {
	return t.tx.ExecContext(ctx, query, args...)
}

func (t *txAdapter) Commit() error   { return t.tx.Commit() }
func (t *txAdapter) Rollback() error { return t.tx.Rollback() }

// ReconcileState checks subtitle_state entries against the filesystem and
// cleans up stale records. Delegates to the reconcile subpackage.
func (d *DB) ReconcileState(ctx context.Context) (api.ReconcileResult, error) {
	return reconcile.Run(ctx, &reconcileAdapter{d}, reconcile.StatFunc(d.statFn))
}
