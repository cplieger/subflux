package api

import (
	"context"
	"time"
)

// BackoffStore groups the adaptive-backoff persistence methods.
type BackoffStore interface {
	RecordNoResult(ctx context.Context, mediaType MediaType, mediaID, language string, providerName ProviderID, bp BackoffParams) error
	BackedOffProviders(ctx context.Context, mediaType MediaType, mediaID, language string, maxAttempts int) ([]ProviderID, error)
}

// DownloadStore groups subtitle download record persistence.
type DownloadStore interface {
	SaveDownload(ctx context.Context, rec *DownloadRecord) error
	DownloadedRefs(ctx context.Context, mediaType MediaType, mediaID, language string) ([]DownloadedRef, error)
	CurrentScore(ctx context.Context, mediaType MediaType, mediaID, language string) (score int, mediaImported time.Time, found bool, err error)
}

// ManualLockStore groups manual override lock persistence.
type ManualLockStore interface {
	IsManuallyLocked(ctx context.Context, mediaType MediaType, mediaID, language string) (bool, error)
	ClearManualLock(ctx context.Context, mediaType MediaType, mediaID, language string) error
	ManualDownloadCount(ctx context.Context, mediaType MediaType, mediaID, language string) (int, error)
	ManualSubtitlePaths(ctx context.Context, mediaType MediaType, mediaID, language string) ([]string, error)
	NextManualNumber(ctx context.Context, mediaType MediaType, mediaID, language string) int
}

// QueryStore groups read-only state inspection methods.
type QueryStore interface {
	GetState(ctx context.Context, q *StateQuery) ([]StateEntry, error)
	GetBackoffItems(ctx context.Context) ([]BackoffEntry, error)
	GetBackoffByPrefix(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]BackoffEntry, error)
	GetManualLocks(ctx context.Context) ([]ManualLockEntry, error)
	Stats(ctx context.Context) (downloads, attempts int, err error)
}

// HistoryStore groups download-history lookup methods.
type HistoryStore interface {
	HistoryMediaIDs(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]string, error)
}

// CoverageStore groups subtitle file tracking and scan state methods.
type CoverageStore interface {
	RecordSubtitleFiles(ctx context.Context, mediaType MediaType, mediaID string, files []SubtitleFile) (bool, error)
	UpsertSubtitleFile(ctx context.Context, mediaType MediaType, mediaID string, f *SubtitleFile) error
	GetSubtitleFiles(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]SubtitleFileRow, error)
	DeleteSubtitleFile(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant, source SubtitleSource, path string) error
	RecordScanState(ctx context.Context, rec *ScanRecord) error
	GetScanStates(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]ScanStateRow, error)
	RecentlyScanned(ctx context.Context, cutoff time.Time) (map[string]bool, error)
	TotalSubtitleFiles(ctx context.Context) (int, error)
	LastScanTime(ctx context.Context) (string, error)
}

// SyncOffsetStore groups subtitle timing adjustment persistence.
type SyncOffsetStore interface {
	SetSyncOffset(ctx context.Context, path string, offsetMs int64) error
	GetSyncOffset(ctx context.Context, path string) (int64, error)
}

// MaintStore groups maintenance and cleanup methods.
type MaintStore interface {
	DeleteStateByPaths(ctx context.Context, paths []string) (CleanupResult, error)
	CleanupDrift(ctx context.Context, drift ConfigDrift) error
	ReconcileState(ctx context.Context) (ReconcileResult, error)
}

// PollStore groups arr webhook/poll timestamp persistence.
type PollStore interface {
	GetPollTimestamp(ctx context.Context, key PollKey) (time.Time, error)
	SetPollTimestamp(ctx context.Context, key PollKey, t time.Time) error
}

// PollKey identifies an arr-source poll-timestamp row in the store.
// Using a typed string instead of bare string prevents typo-induced
// silent failures (e.g. "Sonarr" capitalization or "sonar" typo would
// silently insert a new row and force history re-fetch).
type PollKey string

// Canonical poll-key values. New arr sources should add a constant here.
const (
	PollKeySonarr PollKey = "sonarr"
	PollKeyRadarr PollKey = "radarr"
)

// Valid returns true if the PollKey is one of the canonical values.
func (k PollKey) Valid() bool {
	switch k {
	case PollKeySonarr, PollKeyRadarr:
		return true
	default:
		return false
	}
}
