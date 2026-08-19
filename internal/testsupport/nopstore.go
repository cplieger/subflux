// Package testsupport provides shared test helpers used across multiple
// packages. It avoids duplicating mock implementations in each test file.
package testsupport

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// NopStore is the shared store fake: every method returns a zero value and
// nothing is persisted. Embed it in a test-specific mock and override only the
// methods that test exercises.
//
// Its WIDTH is now derived, not declared. There is no store interface for it to
// be exhaustive against any more, so what it must implement is exactly the
// union of the narrow interfaces the eight packages that use it install it as
// (search.SearchStore, scanning.ScanStore, scheduler.Store,
// queryhandlers.QueryStore, synchandlers.SyncStore, filehandlers.FileStore,
// coveragehandlers.CoverageStore, manualops.DownloadStore, polling.PollerStore,
// resolve.FileStore, api.CoverageStore, and server.Store, which is the union of
// most of those). nopstore_test.go states that union in one anonymous interface,
// so a consumer that adds a method fails there with a readable message rather
// than at a dozen install sites.
//
// Two methods went with the composite because nothing reaches them through an
// interface: Close (main.go calls it on the concrete *boltstore.DB) and
// ManualDownloadCount (boltstore's own NextManualNumber fallback and its tests
// are the only callers). A sibling panicStore used to shadow all 36 methods so
// that a new one would announce itself; every one of them was overridden here,
// so it was 159 lines that changed no behaviour, and it is gone.
type NopStore struct{}

// BackupInto reports success without writing a snapshot.
func (*NopStore) BackupInto(context.Context, string) error { return nil }

// StoreFileStats reports an empty store.
func (*NopStore) StoreFileStats() (fileBytes, freelistBytes int64) { return 0, 0 }

// --- Backoff (search_attempts) ---

// RecordNoResult records a provider returning no result for a media item, updating backoff state.
func (*NopStore) RecordNoResult(context.Context, api.MediaType, string, string, api.ProviderID, api.BackoffParams) error {
	return nil
}

// BackedOffProviders returns providers currently in backoff for the given media item.
func (*NopStore) BackedOffProviders(context.Context, api.MediaType, string, string, int) ([]api.ProviderID, error) {
	return nil, nil
}

// GetBackoffItems returns all items currently in adaptive search backoff.
func (*NopStore) GetBackoffItems(context.Context) ([]api.BackoffEntry, error) { return nil, nil }

// GetBackoffByPrefix returns backoff entries matching the given media type and ID prefix.
func (*NopStore) GetBackoffByPrefix(context.Context, api.MediaType, string) ([]api.BackoffEntry, error) {
	return nil, nil
}

// --- State (downloads + history) ---

// SaveDownload records or updates a subtitle download record.
func (*NopStore) SaveDownload(context.Context, *api.DownloadRecord) error { return nil }

// DownloadedRefs returns the previously downloaded subtitle references for a media item.
func (*NopStore) DownloadedRefs(context.Context, api.MediaType, string, string) ([]api.DownloadedRef, error) {
	return nil, nil
}

// CurrentScore returns the current subtitle score for a media item and language.
func (*NopStore) CurrentScore(context.Context, api.MediaType, string, string, api.Variant) (score int, at time.Time, found bool, _ error) {
	return 0, time.Time{}, false, nil
}

// GetState returns download state entries matching the query.
func (*NopStore) GetState(context.Context, *api.StateQuery) ([]api.StateEntry, error) {
	return nil, nil
}

// HistoryMediaIDs returns distinct media IDs with download history for the given type and language.
func (*NopStore) HistoryMediaIDs(context.Context, api.MediaType, string) ([]string, error) {
	return nil, nil
}

// --- Manual locks ---

// IsManuallyLocked reports whether the key's quad has a manual download lock.
func (*NopStore) IsManuallyLocked(context.Context, api.ManualLockKey) (bool, error) {
	return false, nil
}

// ClearManualLock removes the manual download lock on the key's quad.
func (*NopStore) ClearManualLock(context.Context, api.ManualLockKey) error {
	return nil
}

// ManualSubtitlePaths returns paths of manually downloaded subtitle files on the key's quad.
func (*NopStore) ManualSubtitlePaths(context.Context, api.ManualLockKey) ([]string, error) {
	return nil, nil
}

// NextManualNumber returns the next sequential number for a manual subtitle file.
func (*NopStore) NextManualNumber(context.Context, api.ManualLockKey) int {
	return 1
}

// GetManualLocks returns all active manual download locks.
func (*NopStore) GetManualLocks(context.Context) ([]api.ManualLockEntry, error) { return nil, nil }

// --- Coverage (subtitle_files + scan_state) ---

// RecordSubtitleFiles records the full set of subtitle files for a media item.
func (*NopStore) RecordSubtitleFiles(context.Context, api.MediaType, string, []api.SubtitleFile) (bool, error) {
	return false, nil
}

// UpsertSubtitleFile inserts or updates a single subtitle file record.
func (*NopStore) UpsertSubtitleFile(context.Context, api.MediaType, string, *api.SubtitleFile) error {
	return nil
}

// GetSubtitleFiles returns subtitle file records for a media item.
func (*NopStore) GetSubtitleFiles(context.Context, api.MediaType, string) ([]api.SubtitleEntry, error) {
	return nil, nil
}

// DeleteSubtitleFile removes a subtitle file record for a media item.
func (*NopStore) DeleteSubtitleFile(context.Context, api.MediaType, string, string, api.Variant, api.SubtitleSource, string) error {
	return nil
}

// RecordScanState records the scan timestamp and metadata for a media item.
func (*NopStore) RecordScanState(context.Context, *api.ScanRecord) error {
	return nil
}

// GetScanStates returns scan state records for a media item prefix.
func (*NopStore) GetScanStates(context.Context, api.MediaType, string) ([]api.ScanStateRow, error) {
	return nil, nil
}

// ScanCycleStart returns the zero time (no cycle mark stored).
func (*NopStore) ScanCycleStart(context.Context) (time.Time, error) { return time.Time{}, nil }

// SetScanCycleStart is a no-op.
func (*NopStore) SetScanCycleStart(context.Context, time.Time) error { return nil }

// ClearScanCycleStart is a no-op.
func (*NopStore) ClearScanCycleStart(context.Context) error { return nil }

// RecentlyScanned returns the set of media IDs scanned after the given cutoff time.
func (*NopStore) RecentlyScanned(context.Context, time.Time) (map[string]bool, error) {
	return nil, nil
}

// TotalSubtitleFiles returns the total number of tracked subtitle file records.
func (*NopStore) TotalSubtitleFiles(context.Context) (int, error) { return 0, nil }

// LastScanTime returns the formatted timestamp of the most recent scan completion.
func (*NopStore) LastScanTime(context.Context) (string, error) { return "", nil }

// --- Sync offsets ---

// SetSyncOffset stores the subtitle timing offset in milliseconds for a video path.
func (*NopStore) SetSyncOffset(context.Context, string, int64) error { return nil }

// GetSyncOffset returns the stored timing offset in milliseconds for a video path.
func (*NopStore) GetSyncOffset(context.Context, string) (int64, error) { return 0, nil }

// --- Poll timestamps ---

// GetPollTimestamp returns the last poll timestamp for the given poll key.
func (*NopStore) GetPollTimestamp(context.Context, api.PollKey) (time.Time, error) {
	return time.Time{}, nil
}

// SetPollTimestamp stores the poll timestamp for the given poll key.
func (*NopStore) SetPollTimestamp(context.Context, api.PollKey, time.Time) error { return nil }

// --- Maintenance ---

// Stats returns aggregate counts for downloads and active backoff entries.
func (*NopStore) Stats(context.Context) (downloads, backoffs int, _ error) { return 0, 0, nil }

// DeleteStateByPaths removes all state records associated with the given video paths.
func (*NopStore) DeleteStateByPaths(context.Context, []string) (api.CleanupResult, error) {
	return api.CleanupResult{}, nil
}

// CleanupDrift removes search_attempts entries for providers/languages that are
// no longer in the active configuration.
func (*NopStore) CleanupDrift(context.Context, api.ConfigDrift) error { return nil }

// ReconcileState performs the three-way filesystem reconciliation pass.
func (*NopStore) ReconcileState(context.Context) (api.ReconcileResult, error) {
	return api.ReconcileResult{}, nil
}
