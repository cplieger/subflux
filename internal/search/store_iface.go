package search

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// SearchFlowStore is the narrow store interface for search flow operations:
// backoff tracking, download recording, score queries, and manual locks.
// Consumed by orchestrate.go and search_download.go.
//
//nolint:revive // name is established API; renaming would break consumers
type SearchFlowStore interface {
	RecordNoResult(ctx context.Context, mediaType api.MediaType, mediaID, language string, providerName api.ProviderID, bp api.BackoffParams) error
	BackedOffProviders(ctx context.Context, mediaType api.MediaType, mediaID, language string, maxAttempts int) ([]api.ProviderID, error)
	SaveDownload(ctx context.Context, rec *api.DownloadRecord) error
	CurrentScore(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) (score int, mediaImported time.Time, found bool, err error)
	IsManuallyLocked(ctx context.Context, mediaType api.MediaType, mediaID, language string, variant api.Variant) (bool, error)
}

// CoverageRecorder is the narrow store interface for coverage tracking:
// subtitle file recording, sync offsets, and scan state.
// Consumed by SearchTargets and downloadAndSave.
type CoverageRecorder interface {
	RecordSubtitleFiles(ctx context.Context, mediaType api.MediaType, mediaID string, files []api.SubtitleFile) (bool, error)
	UpsertSubtitleFile(ctx context.Context, mediaType api.MediaType, mediaID string, f *api.SubtitleFile) error
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
	RecordScanState(ctx context.Context, rec *api.ScanRecord) error
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
