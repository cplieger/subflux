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
//
// subtitle_state rows are keyed by the (media_type, media_id, language,
// variant) quad, so CurrentScore is answered per variant: an fr/forced
// download never shadows the fr/standard score. DownloadedRefs deliberately
// stays language-scoped (all variants): it feeds the manual-search popup's
// "on disk" markers, and the popup lists every variant of the language.
type DownloadStore interface {
	SaveDownload(ctx context.Context, rec *DownloadRecord) error
	DownloadedRefs(ctx context.Context, mediaType MediaType, mediaID, language string) ([]DownloadedRef, error)
	CurrentScore(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant) (score int, mediaImported time.Time, found bool, err error)
}

// ManualLockStore groups manual override lock persistence. Locks live on the
// (media_type, media_id, language, variant) quad: a manual forced download
// locks only the forced target, leaving standard/hi automation untouched.
//
// Methods documented as accepting an empty variant treat "" as "any/all
// variants of the language"; the rest require an exact variant.
type ManualLockStore interface {
	// IsManuallyLocked reports whether the quad has a manual row. An empty
	// variant asks whether ANY variant of the language is locked.
	IsManuallyLocked(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant) (bool, error)
	// ClearManualLock clears the quad's lock. An empty variant clears the
	// locks of ALL variants of the language.
	ClearManualLock(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant) error
	// ManualDownloadCount counts the quad's manual rows (exact variant).
	ManualDownloadCount(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant) (int, error)
	// ManualSubtitlePaths returns the manual rows' file paths. An empty
	// variant returns the paths of ALL variants of the language.
	ManualSubtitlePaths(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant) ([]string, error)
	// NextManualNumber returns the next manual ordinal for the quad (exact
	// variant): movie.fr.1.srt and movie.fr.forced.1.srt count independently.
	NextManualNumber(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant) int
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
	GetSubtitleFiles(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]SubtitleEntry, error)
	DeleteSubtitleFile(ctx context.Context, mediaType MediaType, mediaID, language string, variant Variant, source SubtitleSource, path string) error
	RecordScanState(ctx context.Context, rec *ScanRecord) error
	GetScanStates(ctx context.Context, mediaType MediaType, mediaIDPrefix string) ([]ScanStateRow, error)
	RecentlyScanned(ctx context.Context, cutoff time.Time) (map[string]bool, error)
	TotalSubtitleFiles(ctx context.Context) (int, error)
	LastScanTime(ctx context.Context) (string, error)
	// Scan-cycle mark (duration-aware resume): set when a full scan begins,
	// cleared on normal completion. A dangling mark means the previous cycle
	// was interrupted; ScanCycleStart returns the zero time when absent.
	ScanCycleStart(ctx context.Context) (time.Time, error)
	SetScanCycleStart(ctx context.Context, t time.Time) error
	ClearScanCycleStart(ctx context.Context) error
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
