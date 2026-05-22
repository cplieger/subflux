package store

import (
	"context"
	"database/sql"
	"log/slog"
	"strings"
)

// cleanOrphanedCoverage removes subtitle_files and scan_state rows for
// media items that no longer have any subtitle_state rows. Processes in
// batches to respect SQLite's 999-parameter limit.
func cleanOrphanedCoverage(ctx context.Context, tx *sql.Tx, keys []MediaKey) {
	// Deduplicate by (typ, id) — lang is irrelevant for coverage ownership.
	type mediaRef struct {
		typ string
		id  string
	}
	seen := make(map[mediaRef]bool, len(keys))
	var items []MediaKey
	for _, k := range keys {
		ref := mediaRef{string(k.typ), k.id}
		if !seen[ref] {
			seen[ref] = true
			items = append(items, k) // reuse MediaKey; lang ignored below
		}
	}
	if len(items) == 0 {
		return
	}

	// Process in batches of sqliteBatchSize/2 (each item uses 2 params).
	const itemBatch = sqliteBatchSize / 2
	var orphans []MediaKey
	for batch := range batchSlice(items, itemBatch) {
		batchOrphans := findOrphans(ctx, tx, batch)
		orphans = append(orphans, batchOrphans...)
	}
	if len(orphans) == 0 {
		return
	}

	batchDeleteByMediaKey(ctx, tx, "subtitle_files", orphans)
	batchDeleteByMediaKey(ctx, tx, "scan_state", orphans)
}

// findOrphans returns items from the batch that have no remaining subtitle_state rows.
func findOrphans(ctx context.Context, tx *sql.Tx, batch []MediaKey) []MediaKey {
	var b strings.Builder
	b.WriteString(`SELECT media_type, media_id FROM subtitle_state WHERE `)
	args := make([]any, 0, len(batch)*2)
	for i, k := range batch {
		if i > 0 {
			b.WriteString(" OR ")
		}
		b.WriteString("(media_type = ? AND media_id = ?)")
		args = append(args, k.typ, k.id)
	}
	b.WriteString(" GROUP BY media_type, media_id")

	rows, err := tx.QueryContext(ctx, b.String(), args...)
	if err != nil {
		slog.Warn("cleanOrphanedCoverage: batch query failed", "error", err)
		return nil
	}
	type mediaRef struct {
		typ string
		id  string
	}
	hasState := make(map[mediaRef]bool)
	for rows.Next() {
		var mt, mid string
		if err := rows.Scan(&mt, &mid); err != nil {
			slog.Warn("cleanOrphanedCoverage: scan failed", "error", err)
			break
		}
		hasState[mediaRef{mt, mid}] = true
	}
	rows.Close()

	var orphans []MediaKey
	for _, k := range batch {
		if !hasState[mediaRef{string(k.typ), k.id}] {
			orphans = append(orphans, k)
		}
	}
	return orphans
}

// batchDeleteByMediaKey deletes rows from the given table where
// (media_type, media_id) matches any of the orphans, processing in
// batches to stay under SQLite's parameter limit.
func batchDeleteByMediaKey(ctx context.Context, tx *sql.Tx, table string, orphans []MediaKey) {
	const deleteBatch = sqliteBatchSize / 2
	for batch := range batchSlice(orphans, deleteBatch) {
		var b strings.Builder
		args := make([]any, 0, len(batch)*2)
		b.WriteString(`DELETE FROM `)
		b.WriteString(table)
		b.WriteString(` WHERE (media_type, media_id) IN (`)
		for i, k := range batch {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString("(?, ?)")
			args = append(args, k.typ, k.id)
		}
		b.WriteString(")")
		if _, err := tx.ExecContext(ctx, b.String(), args...); err != nil {
			slog.Warn("cleanOrphanedCoverage: batch delete failed",
				"table", table, "error", err)
		}
	}
}
