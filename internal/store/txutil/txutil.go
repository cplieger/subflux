// Package txutil provides shared transaction helpers for store sub-packages.
package txutil

import (
	"database/sql"
	"errors"
	"log/slog"
	"strings"
)

// Rollbacker is any type that supports Rollback (e.g. *sql.Tx or test fakes).
type Rollbacker interface {
	Rollback() error
}

// DeferRollback is a deferred rollback helper that suppresses the expected
// sql.ErrTxDone error (which occurs after a successful Commit). Use as:
//
//	defer txutil.DeferRollback(tx)
func DeferRollback(tx Rollbacker) {
	if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
		slog.Warn("rollback failed", "error", rbErr)
	}
}

// LikeEscaper escapes SQL LIKE wildcards (% and _) and the escape character
// itself (\) in user-provided prefixes.
var LikeEscaper = strings.NewReplacer(`\`, `\\`, "%", `\%`, "_", `\_`)

// TableScanner co-locates a column list with its scan function, making it
// impossible to use mismatched column/scan pairs in queries.
type TableScanner[T any] struct {
	Scan     func(row interface{ Scan(...any) error }) (*T, error)
	ScanInto func(row interface{ Scan(...any) error }, dst *T) error
	Columns  string
}

// AppendPrefixFilter adds a LIKE clause for the given column if prefix is non-empty.
func AppendPrefixFilter(query string, args []any, prefix, column string) (q string, a []any) {
	if prefix == "" {
		return query, args
	}
	return query + " AND " + column + " LIKE ? ESCAPE '\\'",
		append(args, LikeEscaper.Replace(prefix)+"%")
}
