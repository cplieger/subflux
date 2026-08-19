package search

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

// SearchFlowStore is the narrow store interface for search flow operations:
// backoff tracking, download recording, score queries, and manual locks.
// Consumed by orchestrate.go and search_download.go.
//
//nolint:revive // name is established API; renaming would break consumers
type SearchFlowStore interface {
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

// SearchStore is the composite store interface consumed by the search engine.
// It combines SearchFlowStore (backoff + download + lock) and CoverageRecorder
// (file tracking + scan state). The concrete store.DB satisfies this via
// structural typing.
//
//nolint:revive // name is established API; renaming would break consumers
type SearchStore interface {
	SearchFlowStore
	CoverageRecorder
}
