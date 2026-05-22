package store

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"strings"

	sqlite "modernc.org/sqlite"
	sqlitelib "modernc.org/sqlite/lib"

	"subflux/internal/store/migrations"
)

// applySchema executes the DDL and runs any necessary migrations.
func applySchema(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, migrations.Schema); err != nil {
		return err
	}
	return migratePasskeys(ctx, db)
}

// migratePasskeys adds columns introduced in webauthn v0.16.
// ALTER TABLE ADD COLUMN is a no-op if the column already exists in SQLite
// when wrapped in a try-ignore pattern via separate statements.
func migratePasskeys(ctx context.Context, db *sql.DB) error {
	for _, col := range migrations.Migrations {
		if _, err := db.ExecContext(ctx, col); err != nil &&
			!isDuplicateColumn(err) {
			return err
		}
	}
	slog.Debug("auth_passkeys migration complete")
	return nil
}

// errMsgDuplicateColumn is the SQLite error message substring for duplicate
// column errors. Extracted as a named constant to document the coupling to
// SQLite's error message format.
const errMsgDuplicateColumn = "duplicate column"

// isDuplicateColumn reports whether err is a SQLite "duplicate column name"
// error, returned when ALTER TABLE ADD COLUMN targets an existing column.
// Checks both the error code (SQLITE_ERROR = 1) and the message substring
// to avoid false positives from unrelated errors with similar messages.
func isDuplicateColumn(err error) bool {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code() == sqlitelib.SQLITE_ERROR &&
			strings.Contains(sqliteErr.Error(), errMsgDuplicateColumn)
	}
	return false
}
