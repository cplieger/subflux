package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/authdb"
	"github.com/cplieger/subflux/internal/store/coveragedb"
)

// DB wraps the SQLite database.
type DB struct {
	// Auth sub-store: all auth persistence is delegated to authdb.AuthDB.
	authDB *authdb.AuthDB

	// Coverage sub-store: subtitle file tracking and scan state.
	coverageDB *coveragedb.CoverageDB

	db *sql.DB

	// statFn checks file existence for reconciliation. Defaults to os.Stat.
	statFn StatFunc

	// Prepared statements for high-frequency queries. Prepared once at Open
	// time and reused across calls to avoid repeated query parsing overhead.

	// --- Backoff (store_backoff.go) ---
	stmtBackedOff       *sql.Stmt
	stmtRecordNoRes     *sql.Stmt
	stmtGetBackoffItems *sql.Stmt

	// --- State (store_state.go) ---
	stmtCurrentScore *sql.Stmt

	// --- Manual (store_manual.go) ---
	stmtManualDownloadCount *sql.Stmt

	// --- Poll (store_poll.go) ---
	stmtGetPollTimestamp *sql.Stmt
	stmtSetPollTimestamp *sql.Stmt
}

// MediaKey identifies a media+language combination. Used by maintenance
// routines (store_maint.go) to propagate affected rows across tables.
type MediaKey struct {
	typ  api.MediaType
	id   string
	lang string
}

// NewMediaKey constructs a MediaKey from its components. It returns an error
// if typ is not a valid MediaType, allowing callers to handle corrupted data
// gracefully rather than crashing the process.
func NewMediaKey(typ api.MediaType, id, lang string) (MediaKey, error) {
	if !typ.Valid() {
		return MediaKey{}, fmt.Errorf("store.NewMediaKey: invalid MediaType %q", typ)
	}
	return MediaKey{typ: typ, id: id, lang: lang}, nil
}

// Open creates or opens the SQLite database and applies the schema.
// Tables use IF NOT EXISTS so the schema is safe to run on every startup.
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("open db: path must not be empty")
	}
	if strings.ContainsRune(path, 0) {
		return nil, errors.New("open db: path contains null byte")
	}
	db, err := sql.Open("sqlite",
		path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)&_pragma=secure_delete(ON)&_time_format=sqlite&_texttotime=1")
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	// SQLite supports only one writer at a time. Limiting to a single
	// connection avoids "database is locked" errors under concurrency.
	db.SetMaxOpenConns(1)
	if schemaErr := applySchema(ctx, db); schemaErr != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", schemaErr)
	}
	// The DB holds password hashes, emails, and client IPs — restrict to owner.
	// SQLite recreates the -wal/-shm sidecars with the main file's mode, so this
	// is durable; the loop also fixes any sidecars applySchema already created.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if cErr := os.Chmod(p, 0o600); cErr != nil && !errors.Is(cErr, fs.ErrNotExist) {
			slog.Warn("chmod db file", "path", p, "error", cErr)
		}
	}
	var journalMode string
	if scanErr := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); scanErr != nil {
		slog.Warn("failed to verify journal_mode", "error", scanErr)
	} else {
		slog.Debug("database opened", "path", path, "journal_mode", journalMode)
	}

	d := &DB{db: db, statFn: os.Stat}
	authDB, err := authdb.New(ctx, db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init authdb: %w", err)
	}
	d.authDB = authDB
	covDB, err := coveragedb.New(ctx, db)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("init coveragedb: %w", err)
	}
	d.coverageDB = covDB
	if err := d.prepareStatements(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("prepare statements: %w", err)
	}
	return d, nil
}

// Close closes prepared statements and the database. The context allows
// the caller to bound shutdown time (e.g. SIGTERM grace period).
func (d *DB) Close(ctx context.Context) error {
	if d.authDB != nil {
		d.authDB.Close(ctx)
	}
	if d.coverageDB != nil {
		d.coverageDB.Close(ctx)
	}
	for _, s := range d.stmts() {
		if s != nil {
			s.Close()
		}
	}
	return d.db.Close()
}

// --- Prepared statements ---

// prepareStatements prepares frequently-used queries once at startup.
func (d *DB) prepareStatements(ctx context.Context) error {
	var err error
	d.stmtBackedOff, err = d.db.PrepareContext(ctx, `
		SELECT provider, failures, next_retry FROM search_attempts
		WHERE media_type = ? AND media_id = ? AND language = ? AND provider != ''`)
	if err != nil {
		return err
	}
	d.stmtRecordNoRes, err = d.db.PrepareContext(ctx, `
		INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry)
		VALUES (?1, ?2, ?3, ?4, ?5, 1, ?6)
		ON CONFLICT (media_type, media_id, language, provider) DO UPDATE SET
			last_tried = ?5,
			failures = search_attempts.failures + 1,
			next_retry = datetime(?5, '+' || CAST(
				MIN(?7, ?8 * POWER(?9, search_attempts.failures))
			AS INTEGER) || ' seconds')`)
	if err != nil {
		return err
	}
	d.stmtCurrentScore, err = d.db.PrepareContext(ctx, `
		SELECT score, media_imported FROM subtitle_state
		WHERE media_type = ? AND media_id = ? AND language = ? AND manual = 0
		ORDER BY score DESC LIMIT 1`)
	if err != nil {
		return err
	}
	d.stmtGetBackoffItems, err = d.db.PrepareContext(ctx,
		`SELECT `+backoffScanner.Columns+` FROM search_attempts WHERE provider != '' ORDER BY next_retry ASC`) //nolint:gosec // G202: compile-time columns
	if err != nil {
		return err
	}
	d.stmtManualDownloadCount, err = d.db.PrepareContext(ctx, `
		SELECT COUNT(*) FROM subtitle_state
		WHERE media_type = ? AND media_id = ? AND language = ? AND manual = 1`)
	if err != nil {
		return err
	}
	d.stmtGetPollTimestamp, err = d.db.PrepareContext(ctx,
		`SELECT value FROM poll_state WHERE key = ?`)
	if err != nil {
		return err
	}
	d.stmtSetPollTimestamp, err = d.db.PrepareContext(ctx, `
		INSERT INTO poll_state (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
	if err != nil {
		return err
	}
	return nil
}

// stmts returns all prepared statements for data-driven close/iteration.
func (d *DB) stmts() []*sql.Stmt {
	return []*sql.Stmt{
		d.stmtBackedOff,
		d.stmtRecordNoRes,
		d.stmtCurrentScore,
		d.stmtGetBackoffItems,
		d.stmtManualDownloadCount,
		d.stmtGetPollTimestamp,
		d.stmtSetPollTimestamp,
	}
}
