package search

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

// Store is the store surface the search engine consumes, and the whole of it:
// the engine calls all nine of these and nothing else.
//
// It was three interfaces — FlowStore and CoverageRecorder embedded into this
// composite — and the two halves were deleted because nothing depended on them.
// Repo-wide, neither name appeared as a parameter, a field, or a type assertion;
// both existed only to be embedded here, while their doc comments claimed
// consumers ("Consumed by orchestrate.go and search_download.go", "Consumed by
// SearchTargets and downloadAndSave") that took this composite instead. A
// grouping worth stating is worth stating as a comment; an interface nothing
// names is mechanism with no consumer, and one whose doc names consumers it does
// not have is worse than none.
//
// The concrete boltstore.DB satisfies this structurally; that package declares no
// interface of its own by design.
type Store interface {
	// Search flow: backoff tracking, download recording, score queries, and
	// manual locks.
	RecordNoResult(ctx context.Context, mediaType subflux.MediaType, mediaID, language string, providerName subflux.ProviderID, bp subflux.BackoffParams) error
	BackedOffProviders(ctx context.Context, mediaType subflux.MediaType, mediaID, language string, maxAttempts int) ([]subflux.ProviderID, error)
	SaveDownload(ctx context.Context, rec *subflux.DownloadRecord) error
	CurrentScore(ctx context.Context, mediaType subflux.MediaType, mediaID, language string, variant subflux.Variant) (score int, mediaImported time.Time, found bool, err error)
	IsManuallyLocked(ctx context.Context, key subflux.ManualLockKey) (bool, error)

	// Coverage: subtitle-file recording, sync offsets, and scan state.
	RecordSubtitleFiles(ctx context.Context, mediaType subflux.MediaType, mediaID string, files []subflux.SubtitleFile) (bool, error)
	UpsertSubtitleFile(ctx context.Context, mediaType subflux.MediaType, mediaID string, f *subflux.SubtitleFile) error
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
	RecordScanState(ctx context.Context, rec *subflux.ScanRecord) error
}
