// Package store provides SQLite-backed state for adaptive search and subtitle state.
//
// The package is split into focused files by concern:
//   - store.go          — shared helpers (appendPrefixFilter, queryRows, placeholders, deferRollback)
//   - store_db.go       — DB struct, Open/Close lifecycle
//   - store_backoff.go  — adaptive search backoff (search_attempts table)
//   - store_state.go    — subtitle_state (download history), stats, queries
//   - store_manual.go   — manual download locks
//   - store_coverage.go — subtitle_files + scan_state (coverage tracking)
//   - store_poll.go     — poll_state (Sonarr/Radarr timestamp tracking)
//   - store_maint.go    — cleanup, reconciliation, config-drift handling
//   - auth.go           — auth_users / sessions / passkeys / api keys / totp / oidc
package store

import (
	"database/sql"
	"iter"
	"os"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/txutil"
	_ "modernc.org/sqlite" // register sqlite3 database/sql driver
)

// sqliteBatchSize is the maximum number of SQL parameters per statement,
// chosen to stay well under SQLite's SQLITE_MAX_VARIABLE_NUMBER (999).
const sqliteBatchSize = 400

// likeEscaper delegates to the shared txutil implementation.
var likeEscaper = txutil.LikeEscaper

// Compile-time assertion: *DB implements api.Store.
var _ api.Store = (*DB)(nil)

// appendPrefixFilter delegates to the shared txutil implementation.
func appendPrefixFilter(query string, args []any, prefix string) (q string, a []any) {
	return txutil.AppendPrefixFilter(query, args, prefix, "media_id")
}

// StatFunc checks file existence. Defaults to os.Stat.
// Override in tests to avoid filesystem dependency.
type StatFunc func(path string) (os.FileInfo, error)

// deferRollback is a deferred rollback helper that suppresses the expected
// sql.ErrTxDone error (which occurs after a successful Commit). Use as:
//
//	defer deferRollback(tx)
func deferRollback(tx *sql.Tx) {
	txutil.DeferRollback(tx)
}

// placeholders builds a parameterized SQL IN clause from a slice of string-like values.
// Returns the comma-separated "?,?,?" string and the corresponding []any args.
//
// SQL-injection safe: the returned clause contains ONLY literal '?' and ','
// characters — no user-supplied data is interpolated into the SQL string.
// Callers embed the clause in a query (e.g. "WHERE col IN ("+clause+")")
// and pass args as bind parameters, ensuring the database driver handles
// escaping. The function is intentionally unexported to keep its usage
// within the store package where all queries are parameterized.
func placeholders[T ~string](values []T) (clause string, args []any) {
	args = make([]any, len(values))
	for i, v := range values {
		args[i] = v
	}
	if len(values) == 0 {
		return "", args
	}
	// Pre-allocate builder: each placeholder is "?," (2 bytes), minus trailing comma.
	var b strings.Builder
	b.Grow(len(values)*2 - 1)
	for i := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('?')
	}
	return b.String(), args
}

// batchSlice returns an iterator that yields sub-slices of s with at most
// batchSize elements each. Used to chunk SQL parameters under SQLite limits.
func batchSlice[T any](s []T, batchSize int) iter.Seq[[]T] {
	return func(yield func([]T) bool) {
		for i := 0; i < len(s); i += batchSize {
			end := min(i+batchSize, len(s))
			if !yield(s[i:end]) {
				return
			}
		}
	}
}
