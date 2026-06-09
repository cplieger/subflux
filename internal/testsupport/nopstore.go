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

func (*NopStore) RecordNoResult(context.Context, api.MediaType, string, string, api.ProviderID, api.BackoffParams) error {
	return nil
}

func (*NopStore) BackedOffProviders(context.Context, api.MediaType, string, string, int) ([]api.ProviderID, error) {
	return nil, nil
}
func (*NopStore) GetBackoffItems(context.Context) ([]api.BackoffEntry, error) { return nil, nil }
func (*NopStore) GetBackoffByPrefix(context.Context, api.MediaType, string) ([]api.BackoffEntry, error) {
	return nil, nil
}

// --- State (downloads + history) ---

func (*NopStore) SaveDownload(context.Context, *api.DownloadRecord) error { return nil }

func (*NopStore) DownloadedRefs(context.Context, api.MediaType, string, string) ([]api.DownloadedRef, error) {
	return nil, nil
}

func (*NopStore) CurrentScore(context.Context, api.MediaType, string, string) (score int, at time.Time, found bool, _ error) {
	return 0, time.Time{}, false, nil
}

func (*NopStore) GetState(context.Context, *api.StateQuery) ([]api.StateEntry, error) {
	return nil, nil
}

func (*NopStore) HistoryMediaIDs(context.Context, api.MediaType, string) ([]string, error) {
	return nil, nil
}

// --- Manual locks ---

func (*NopStore) IsManuallyLocked(context.Context, api.MediaType, string, string) (bool, error) {
	return false, nil
}
func (*NopStore) ClearManualLock(context.Context, api.MediaType, string, string) error { return nil }
func (*NopStore) ManualDownloadCount(context.Context, api.MediaType, string, string) (int, error) {
	return 0, nil
}

func (*NopStore) ManualSubtitlePaths(context.Context, api.MediaType, string, string) ([]string, error) {
	return nil, nil
}
func (*NopStore) NextManualNumber(context.Context, api.MediaType, string, string) int { return 1 }
func (*NopStore) GetManualLocks(context.Context) ([]api.ManualLockEntry, error)       { return nil, nil }

// --- Coverage (subtitle_files + scan_state) ---

func (*NopStore) RecordSubtitleFiles(context.Context, api.MediaType, string, []api.SubtitleFile) (bool, error) {
	return false, nil
}

func (*NopStore) UpsertSubtitleFile(context.Context, api.MediaType, string, *api.SubtitleFile) error {
	return nil
}

func (*NopStore) GetSubtitleFiles(context.Context, api.MediaType, string) ([]api.SubtitleEntry, error) {
	return nil, nil
}

func (*NopStore) DeleteSubtitleFile(context.Context, api.MediaType, string, string, api.Variant, api.SubtitleSource, string) error {
	return nil
}

func (*NopStore) RecordScanState(context.Context, *api.ScanRecord) error {
	return nil
}

func (*NopStore) GetScanStates(context.Context, api.MediaType, string) ([]api.ScanStateRow, error) {
	return nil, nil
}

func (*NopStore) RecentlyScanned(context.Context, time.Time) (map[string]bool, error) {
	return nil, nil
}
func (*NopStore) TotalSubtitleFiles(context.Context) (int, error) { return 0, nil }
func (*NopStore) LastScanTime(context.Context) (string, error)    { return "", nil }

// --- Sync offsets ---

func (*NopStore) SetSyncOffset(context.Context, string, int64) error   { return nil }
func (*NopStore) GetSyncOffset(context.Context, string) (int64, error) { return 0, nil }

// --- Poll timestamps ---

func (*NopStore) GetPollTimestamp(context.Context, api.PollKey) (time.Time, error) {
	return time.Time{}, nil
}
func (*NopStore) SetPollTimestamp(context.Context, api.PollKey, time.Time) error { return nil }

// --- Maintenance ---

func (*NopStore) Stats(context.Context) (downloads, backoffs int, _ error) { return 0, 0, nil }

func (*NopStore) DeleteStateByPaths(context.Context, []string) (api.CleanupResult, error) {
	return api.CleanupResult{}, nil
}
func (*NopStore) CleanupDrift(context.Context, api.ConfigDrift) error { return nil }
func (*NopStore) ReconcileState(context.Context) (api.ReconcileResult, error) {
	return api.ReconcileResult{}, nil
}
func (*NopStore) Close(context.Context) error { return nil }
