package coveragedb

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/store/txutil"
)

// subtitleFileInsertBatch is the maximum number of subtitle file rows to
// insert per SQL statement. SQLite supports up to 999 parameters; each row
// uses 8 columns, so batch at most 124 rows. We use 100 for a round number.
const subtitleFileInsertBatch = 100

// subtitleFileDeleteBatch is the maximum number of subtitle file rows to
// delete per batched DELETE statement. Each row uses 4 placeholders
// (language, variant, source, path) plus 2 shared (media_type, media_id).
const subtitleFileDeleteBatch = 50

// subtitleFileUpdateBatch is the maximum number of subtitle file rows to
// update per batched UPDATE statement using CASE expressions.
const subtitleFileUpdateBatch = 50

// RecordSubtitleFiles syncs the subtitle_files table for a media item with
// the provided set from disk. Uses a diff-based approach: only inserts new
// rows, updates changed rows, and deletes rows no longer on disk.
func (d *CoverageDB) RecordSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaID string, files []api.SubtitleFile) (bool, error) {
	want := make(map[subFileKey]subFileVal, len(files))
	for _, f := range files {
		want[subFileKey{f.Language, string(f.Variant), string(f.Source), f.Path}] = subFileVal{f.Codec}
	}

	have, err := d.loadSubtitleFiles(ctx, mediaType, mediaID)
	if err != nil {
		return false, err
	}

	toDelete, toInsert, toUpdate := diffSubtitleFiles(have, want)
	if len(toDelete) == 0 && len(toInsert) == 0 && len(toUpdate) == 0 {
		return false, nil
	}

	slog.Debug("subtitle files diff",
		"media_type", mediaType, "media_id", mediaID,
		"insert", len(toInsert), "update", len(toUpdate), "delete", len(toDelete))

	if err := d.applySubtitleFileDiff(ctx, mediaType, mediaID,
		want, toDelete, toInsert, toUpdate); err != nil {
		return false, err
	}
	return true, nil
}

// UpsertSubtitleFile inserts or updates a single subtitle file record.
func (d *CoverageDB) UpsertSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID string, f *api.SubtitleFile) error {
	_, err := d.db.ExecContext(ctx,
		`INSERT INTO subtitle_files
			(media_type, media_id, language, variant, source, codec, path,
			 updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)
		ON CONFLICT (media_type, media_id, language, variant, source, path)
		DO UPDATE SET codec = excluded.codec,
			updated_at = CURRENT_TIMESTAMP`,
		mediaType, mediaID, f.Language, f.Variant, f.Source,
		f.Codec, f.Path)
	if err != nil {
		return fmt.Errorf("upsert subtitle_file: %w", err)
	}
	return nil
}

// DeleteSubtitleFile removes a single subtitle file record from the DB.
func (d *CoverageDB) DeleteSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant, source api.SubtitleSource, path string) error {
	_, err := d.db.ExecContext(ctx,
		`DELETE FROM subtitle_files
		WHERE media_type = ? AND media_id = ? AND language = ?
			AND variant = ? AND source = ? AND path = ?`,
		mediaType, mediaID, language, string(variant), source, path)
	if err != nil {
		return fmt.Errorf("delete subtitle_file: %w", err)
	}
	return nil
}

// GetSubtitleFiles returns all subtitle_files rows matching the given type
// and optional media_id prefix.
func (d *CoverageDB) GetSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) (out []api.SubtitleEntry, err error) {
	if mediaIDPrefix != "" && !strings.HasSuffix(mediaIDPrefix, "-") {
		rows, queryErr := d.stmtGetSubFilesExact.QueryContext(ctx, mediaType, mediaIDPrefix)
		if queryErr != nil {
			return nil, queryErr
		}
		defer rows.Close()
		for rows.Next() {
			var r api.SubtitleEntry
			if scanErr := rows.Scan(&r.MediaID, &r.Language, &r.Variant, &r.Source, &r.Codec, &r.Path, &r.Score, &r.OffsetMs, &r.VideoPath); scanErr != nil {
				return nil, scanErr
			}
			out = append(out, r)
		}
		return out, rows.Err()
	}

	baseQuery := `SELECT f.media_id, f.language, f.variant, f.source, f.codec, f.path,
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
		WHERE f.media_type = ?`
	args := []any{mediaType}
	baseQuery, args = txutil.AppendPrefixFilter(baseQuery, args, mediaIDPrefix, "f.media_id")

	rows, err := d.db.QueryContext(ctx, baseQuery+" ORDER BY f.media_id, f.language, f.variant, f.source", args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r api.SubtitleEntry
		if scanErr := rows.Scan(&r.MediaID, &r.Language, &r.Variant, &r.Source, &r.Codec, &r.Path, &r.Score, &r.OffsetMs, &r.VideoPath); scanErr != nil {
			return nil, scanErr
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- Unexported helpers ---

// subFileKey is the primary key tuple for subtitle_files within a media item.
type subFileKey struct{ lang, variant, source, path string }

// subFileVal holds the mutable columns for a subtitle_files row.
type subFileVal struct{ codec string }

// loadSubtitleFiles reads the current subtitle_files rows for a media item.
func (d *CoverageDB) loadSubtitleFiles(ctx context.Context,
	mediaType api.MediaType, mediaID string,
) (map[subFileKey]subFileVal, error) {
	rows, err := d.stmtLoadSubFiles.QueryContext(ctx, mediaType, mediaID)
	if err != nil {
		return nil, fmt.Errorf("load existing subtitle_files: %w", err)
	}
	have := make(map[subFileKey]subFileVal)
	for rows.Next() {
		var k subFileKey
		var v subFileVal
		if scanErr := rows.Scan(&k.lang, &k.variant, &k.source,
			&v.codec, &k.path); scanErr != nil {
			rows.Close()
			return nil, fmt.Errorf("scan subtitle_file: %w", scanErr)
		}
		have[k] = v
	}
	if closeErr := rows.Close(); closeErr != nil {
		return nil, fmt.Errorf("close subtitle_files rows: %w", closeErr)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("subtitle_files rows: %w", rowsErr)
	}
	return have, nil
}

// diffSubtitleFiles computes the diff between current DB state and desired state.
func diffSubtitleFiles(have, want map[subFileKey]subFileVal) (
	toDelete, toInsert, toUpdate []subFileKey,
) {
	toDelete = make([]subFileKey, 0, len(have))
	toInsert = make([]subFileKey, 0, len(want))
	toUpdate = make([]subFileKey, 0, min(len(have), len(want)))

	for k := range have {
		if _, ok := want[k]; !ok {
			toDelete = append(toDelete, k)
		}
	}
	for k, wv := range want {
		hv, exists := have[k]
		if !exists {
			toInsert = append(toInsert, k)
		} else if hv.codec != wv.codec {
			toUpdate = append(toUpdate, k)
		}
	}
	return toDelete, toInsert, toUpdate
}

// applySubtitleFileDiff executes the diff operations in a single transaction.
func (d *CoverageDB) applySubtitleFileDiff(ctx context.Context,
	mediaType api.MediaType, mediaID string, want map[subFileKey]subFileVal,
	toDelete, toInsert, toUpdate []subFileKey,
) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer txutil.DeferRollback(tx)

	for i := 0; i < len(toDelete); i += subtitleFileDeleteBatch {
		end := min(i+subtitleFileDeleteBatch, len(toDelete))
		batch := toDelete[i:end]
		var qb strings.Builder
		qb.WriteString(`DELETE FROM subtitle_files
			 WHERE media_type = ? AND media_id = ? AND (`)
		args := make([]any, 0, 2+len(batch)*4)
		args = append(args, mediaType, mediaID)
		for j, k := range batch {
			if j > 0 {
				qb.WriteString(" OR ")
			}
			qb.WriteString("(language = ? AND variant = ? AND source = ? AND path = ?)")
			args = append(args, k.lang, k.variant, k.source, k.path)
		}
		qb.WriteString(")")
		if _, err := tx.ExecContext(ctx, qb.String(), args...); err != nil {
			return fmt.Errorf("batch delete subtitle_files: %w", err)
		}
	}

	for i := 0; i < len(toInsert); i += subtitleFileInsertBatch {
		end := min(i+subtitleFileInsertBatch, len(toInsert))
		batch := toInsert[i:end]
		var qb strings.Builder
		qb.WriteString(`INSERT INTO subtitle_files
			(media_type, media_id, language, variant, source, codec, path, updated_at) VALUES `)
		args := make([]any, 0, len(batch)*8)
		for j, k := range batch {
			if j > 0 {
				qb.WriteString(",")
			}
			qb.WriteString("(?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)")
			v := want[k]
			args = append(args, mediaType, mediaID, k.lang, k.variant, k.source, v.codec, k.path)
		}
		if _, err := tx.ExecContext(ctx, qb.String(), args...); err != nil {
			return fmt.Errorf("batch insert subtitle_files: %w", err)
		}
	}

	for i := 0; i < len(toUpdate); i += subtitleFileUpdateBatch {
		end := min(i+subtitleFileUpdateBatch, len(toUpdate))
		batch := toUpdate[i:end]
		var qb strings.Builder
		qb.WriteString(`UPDATE subtitle_files SET codec = CASE`)
		// 2 shared (media_type, media_id) + per-row: 4 in CASE WHEN + 1 THEN + 4 in WHERE IN
		args := make([]any, 0, 2+len(batch)*9)
		for _, k := range batch {
			qb.WriteString(" WHEN (language = ? AND variant = ? AND source = ? AND path = ?) THEN ?")
			v := want[k]
			args = append(args, k.lang, k.variant, k.source, k.path, v.codec)
		}
		qb.WriteString(` END, updated_at = CURRENT_TIMESTAMP WHERE media_type = ? AND media_id = ? AND (`)
		args = append(args, mediaType, mediaID)
		for j, k := range batch {
			if j > 0 {
				qb.WriteString(" OR ")
			}
			qb.WriteString("(language = ? AND variant = ? AND source = ? AND path = ?)")
			args = append(args, k.lang, k.variant, k.source, k.path)
		}
		qb.WriteString(")")
		if _, err := tx.ExecContext(ctx, qb.String(), args...); err != nil {
			return fmt.Errorf("batch update subtitle_files: %w", err)
		}
	}
	return tx.Commit()
}
