// Package coveragedb provides SQLite-backed persistence for subtitle file
// coverage tracking and scan state. It implements api.CoverageStore and is
// embedded by the parent store.DB to satisfy that interface.
package coveragedb

import (
	"context"
	"database/sql"
	"fmt"

	"subflux/internal/api"
)

// Compile-time assertion: *CoverageDB satisfies api.CoverageStore.
var _ api.CoverageStore = (*CoverageDB)(nil)

// CoverageDB wraps a shared *sql.DB for coverage-specific persistence.
type CoverageDB struct {
	db *sql.DB

	// Prepared statements for high-frequency coverage queries.
	stmtLoadSubFiles     *sql.Stmt
	stmtGetSubFilesExact *sql.Stmt
}

// New creates a CoverageDB using the provided database connection.
// It prepares frequently-used statements for coverage queries.
func New(ctx context.Context, db *sql.DB) (*CoverageDB, error) {
	d := &CoverageDB{db: db}
	if err := d.prepareStatements(ctx); err != nil {
		return nil, fmt.Errorf("coveragedb: prepare statements: %w", err)
	}
	return d, nil
}

// Close closes prepared statements. The underlying *sql.DB is NOT closed
// because it is shared with the parent store.
func (d *CoverageDB) Close(ctx context.Context) {
	if d.stmtLoadSubFiles != nil {
		d.stmtLoadSubFiles.Close()
	}
	if d.stmtGetSubFilesExact != nil {
		d.stmtGetSubFilesExact.Close()
	}
}

// prepareStatements prepares frequently-used coverage queries once at startup.
func (d *CoverageDB) prepareStatements(ctx context.Context) error {
	var err error
	d.stmtLoadSubFiles, err = d.db.PrepareContext(ctx, `
		SELECT language, variant, source, codec, path FROM subtitle_files
		WHERE media_type = ? AND media_id = ?`)
	if err != nil {
		return err
	}
	d.stmtGetSubFilesExact, err = d.db.PrepareContext(ctx, `
		SELECT f.media_id, f.language, f.variant, f.source, f.codec, f.path,
			CASE WHEN f.source = 'embedded' THEN 0 ELSE COALESCE(s.score, 0) END,
			f.offset_ms,
			COALESCE(s.video_path, '')
		FROM subtitle_files f
		LEFT JOIN subtitle_state s
			ON s.media_type = f.media_type
			AND s.media_id = f.media_id
			AND s.language = f.language
			AND s.manual = 0
			AND f.source != 'embedded'
		WHERE f.media_type = ? AND f.media_id = ?
		ORDER BY f.media_id, f.language, f.variant, f.source`)
	if err != nil {
		return err
	}
	return nil
}
