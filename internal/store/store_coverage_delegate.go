package store

import (
	"context"
	"time"

	"subflux/internal/api"
)

// --- Coverage file delegation ---

func (d *DB) RecordSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaID string, files []api.SubtitleFile) (bool, error) {
	return d.coverageDB.RecordSubtitleFiles(ctx, mediaType, mediaID, files)
}

func (d *DB) UpsertSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID string, f *api.SubtitleFile) error {
	return d.coverageDB.UpsertSubtitleFile(ctx, mediaType, mediaID, f)
}

func (d *DB) DeleteSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant, source api.SubtitleSource, path string) error {
	return d.coverageDB.DeleteSubtitleFile(ctx, mediaType, mediaID, language, variant, source, path)
}

func (d *DB) GetSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.SubtitleFileRow, error) {
	return d.coverageDB.GetSubtitleFiles(ctx, mediaType, mediaIDPrefix)
}

// --- Scan state delegation ---

func (d *DB) RecordScanState(ctx context.Context, rec *api.ScanRecord) error {
	return d.coverageDB.RecordScanState(ctx, rec)
}

func (d *DB) GetScanStates(ctx context.Context, mediaType api.MediaType, mediaIDPrefix string) ([]api.ScanStateRow, error) {
	return d.coverageDB.GetScanStates(ctx, mediaType, mediaIDPrefix)
}

func (d *DB) RecentlyScanned(ctx context.Context, cutoff time.Time) (map[string]bool, error) {
	return d.coverageDB.RecentlyScanned(ctx, cutoff)
}

func (d *DB) LastScanTime(ctx context.Context) (string, error) {
	return d.coverageDB.LastScanTime(ctx)
}

func (d *DB) TotalSubtitleFiles(ctx context.Context) (int, error) {
	return d.coverageDB.TotalSubtitleFiles(ctx)
}
