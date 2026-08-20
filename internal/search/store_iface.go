package search

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

// FlowStore is the narrow store interface for search flow operations:
// backoff tracking, download recording, score queries, and manual locks.
// Consumed by orchestrate.go and search_download.go.
type FlowStore interface {
	RecordNoResult(ctx context.Context, mediaType subflux.MediaType, mediaID, language string, providerName subflux.ProviderID, bp subflux.BackoffParams) error
	BackedOffProviders(ctx context.Context, mediaType subflux.MediaType, mediaID, language string, maxAttempts int) ([]subflux.ProviderID, error)
	SaveDownload(ctx context.Context, rec *subflux.DownloadRecord) error
	CurrentScore(ctx context.Context, mediaType subflux.MediaType, mediaID, language string, variant subflux.Variant) (score int, mediaImported time.Time, found bool, err error)
	IsManuallyLocked(ctx context.Context, key subflux.ManualLockKey) (bool, error)
}

// CoverageRecorder is the narrow store interface for coverage tracking:
// subtitle file recording, sync offsets, and scan state.
// Consumed by SearchTargets and downloadAndSave.
type CoverageRecorder interface {
	RecordSubtitleFiles(ctx context.Context, mediaType subflux.MediaType, mediaID string, files []subflux.SubtitleFile) (bool, error)
	UpsertSubtitleFile(ctx context.Context, mediaType subflux.MediaType, mediaID string, f *subflux.SubtitleFile) error
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
	RecordScanState(ctx context.Context, rec *subflux.ScanRecord) error
}

// Store is the composite store interface consumed by the search engine.
// It combines FlowStore (backoff + download + lock) and CoverageRecorder
// (file tracking + scan state). The concrete store.DB satisfies this via
// structural typing.
type Store interface {
	FlowStore
	CoverageRecorder
}
