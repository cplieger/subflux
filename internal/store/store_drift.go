package store

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
)

// CleanupDrift applies DB cleanup for config changes.
func (d *DB) CleanupDrift(ctx context.Context, drift api.ConfigDrift) error {
	if drift.Empty() {
		slog.Debug("config drift: no cleanup needed")
		return nil
	}

	if drift.AdaptiveDisabled {
		res, err := d.db.ExecContext(ctx,
			`DELETE FROM search_attempts`)
		if err != nil {
			return fmt.Errorf("clear all attempts: %w", err)
		}
		if n, nErr := res.RowsAffected(); nErr == nil && n > 0 {
			slog.Info("config drift: adaptive disabled, cleared all attempts",
				"rows", n)
		}
		return nil // No need to also clean per-language/provider.
	}

	if err := d.deleteAttemptsByLanguage(ctx, drift.RemovedLanguages); err != nil {
		return fmt.Errorf("cleanup removed languages: %w", err)
	}
	return d.deleteAttemptsByProvider(ctx, drift.RemovedProviders)
}

// deleteAttemptsByLanguage deletes search_attempts rows matching any language value.
func (d *DB) deleteAttemptsByLanguage(ctx context.Context, values []string) error {
	if len(values) == 0 {
		return nil
	}
	clause, args := placeholders(values)
	res, err := d.db.ExecContext(ctx,
		`DELETE FROM search_attempts WHERE language IN (`+clause+`)`, args...) //nolint:gosec // G202: placeholders() generates safe ? markers
	if err != nil {
		return fmt.Errorf("delete attempts by language in %v: %w", values, err)
	}
	if n, nErr := res.RowsAffected(); nErr == nil && n > 0 {
		slog.Info("config drift: cleared attempts for removed language",
			"values", values, "rows", n)
	}
	return nil
}

// deleteAttemptsByProvider deletes search_attempts rows matching any provider value.
func (d *DB) deleteAttemptsByProvider(ctx context.Context, values []api.ProviderID) error {
	if len(values) == 0 {
		return nil
	}
	clause, args := placeholders(values)
	res, err := d.db.ExecContext(ctx,
		`DELETE FROM search_attempts WHERE provider IN (`+clause+`)`, args...) //nolint:gosec // G202: placeholders() generates safe ? markers
	if err != nil {
		return fmt.Errorf("delete attempts by provider in %v: %w", values, err)
	}
	if n, nErr := res.RowsAffected(); nErr == nil && n > 0 {
		slog.Info("config drift: cleared attempts for removed provider",
			"values", values, "rows", n)
	}
	return nil
}
