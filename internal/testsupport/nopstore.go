// Package testsupport provides shared test helpers used across multiple
// packages. It avoids duplicating mock implementations in each test file.
package testsupport

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// Compile-time assertion that NopStore implements api.Store.
var _ api.Store = (*NopStore)(nil)

// NopStore implements api.Store with all methods returning zero/nil values.
// Embed it in test-specific mock structs and override only the methods
// relevant to each test case.
//
// NopStore embeds panicStore (which panics on every method) and overrides
// all methods with no-op returns. When a new method is added to api.Store,
// only panicStore needs updating (the compiler tells you); NopStore then
// inherits the panic until the no-op override is added here.
type NopStore struct{ panicStore } //nolint:unused // embedded for compile-time interface safety

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

// IsManuallyLocked reports whether the item+language has a manual download lock.
func (*NopStore) IsManuallyLocked(context.Context, api.MediaType, string, string, api.Variant) (bool, error) {
	return false, nil
}

// ClearManualLock removes the manual download lock for the given item and language.
func (*NopStore) ClearManualLock(context.Context, api.MediaType, string, string, api.Variant) error {
	return nil
}

// ManualDownloadCount returns the number of manual subtitle downloads for the item+language.
func (*NopStore) ManualDownloadCount(context.Context, api.MediaType, string, string, api.Variant) (int, error) {
	return 0, nil
}

// ManualSubtitlePaths returns paths of manually downloaded subtitle files for the item+language.
func (*NopStore) ManualSubtitlePaths(context.Context, api.MediaType, string, string, api.Variant) ([]string, error) {
	return nil, nil
}

// NextManualNumber returns the next sequential number for a manual subtitle file.
func (*NopStore) NextManualNumber(context.Context, api.MediaType, string, string, api.Variant) int {
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

// Close releases any resources held by the store.
func (*NopStore) Close(context.Context) error { return nil }
