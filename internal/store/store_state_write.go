package store

import (
	"context"
	"database/sql"
	"log/slog"

	"github.com/cplieger/subflux/internal/api"
)

// Compile-time assertion: *DB satisfies api.DownloadStore.
var _ api.DownloadStore = (*DB)(nil)

// --- Write operations ---

// saveManualDownload inserts a new manual download row (acts as the lock).
func saveManualDownload(ctx context.Context, tx *sql.Tx,
	rec *api.DownloadRecord, m *api.DownloadMeta,
) error {
	_, err := tx.ExecContext(ctx, `
		INSERT INTO subtitle_state
			(media_type, media_id, language, provider, release_name, score, path,
			 title, imdb_id, season, episode, release_tag, manual, video_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 1, ?)`,
		rec.MediaType, rec.MediaID, rec.Language, rec.ProviderName,
		rec.ReleaseName, rec.Score, rec.Path,
		m.Title, m.ImdbID, m.Season, m.Episode, m.ReleaseTag, m.VideoPath)
	return err
}

// saveAutoDownload updates an existing auto row (preserving media_imported),
// or inserts a new one if no auto row exists.
func saveAutoDownload(ctx context.Context, tx *sql.Tx,
	rec *api.DownloadRecord, m *api.DownloadMeta,
) error {
	res, err := tx.ExecContext(ctx, `
		UPDATE subtitle_state
		SET provider = ?, release_name = ?, score = ?, path = ?,
		    title = ?, imdb_id = ?, season = ?, episode = ?,
		    release_tag = ?, video_path = ?
		WHERE media_type = ? AND media_id = ? AND language = ? AND manual = 0`,
		rec.ProviderName, rec.ReleaseName, rec.Score, rec.Path,
		m.Title, m.ImdbID, m.Season, m.Episode, m.ReleaseTag, m.VideoPath,
		rec.MediaType, rec.MediaID, rec.Language)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO subtitle_state
			(media_type, media_id, language, provider, release_name, score, path,
			 title, imdb_id, season, episode, release_tag, manual, video_path)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, ?)`,
		rec.MediaType, rec.MediaID, rec.Language, rec.ProviderName,
		rec.ReleaseName, rec.Score, rec.Path,
		m.Title, m.ImdbID, m.Season, m.Episode, m.ReleaseTag, m.VideoPath)
	return err
}

// SaveDownload records a subtitle download. For auto downloads, updates the
// existing row if one exists (preserving media_imported), or inserts a new
// one. For manual downloads, always inserts a new row (acts as the lock).
// Clears adaptive backoff on success.
func (d *DB) SaveDownload(ctx context.Context, rec *api.DownloadRecord) error {
	slog.Debug("SaveDownload",
		"media_type", rec.MediaType, "media_id", rec.MediaID,
		"lang", rec.Language, "provider", rec.ProviderName,
		"release", rec.ReleaseName, "score", rec.Score,
		"manual", rec.Meta != nil && rec.Meta.Manual)

	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer deferRollback(tx)

	// Clear adaptive search state for all providers (we got what we needed).
	_, err = tx.ExecContext(ctx, `DELETE FROM search_attempts
		WHERE media_type = ? AND media_id = ? AND language = ?`,
		rec.MediaType, rec.MediaID, rec.Language)
	if err != nil {
		return err
	}

	m := rec.Meta
	if m == nil {
		m = &api.DownloadMeta{}
	}

	if m.Manual {
		if err := saveManualDownload(ctx, tx, rec, m); err != nil {
			return err
		}
	} else {
		if err := saveAutoDownload(ctx, tx, rec, m); err != nil {
			return err
		}
	}
	return tx.Commit()
}
