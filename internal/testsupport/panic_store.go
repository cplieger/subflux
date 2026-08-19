package testsupport

import (
	"context"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// Compile-time assertion that panicStore implements api.Store.
var _ api.Store = (*panicStore)(nil)

// panicStore satisfies api.Store with panic("not implemented") bodies.
// When a new method is added to api.Store, only panicStore needs updating
// (the compiler tells you). NopStore embeds panicStore and overrides with
// no-op returns; test-specific mocks embed NopStore and override only the
// methods they exercise.
//
// It exists because api.Store is 36 methods wide, and it is the wrong tool for
// most tests: a test that exercises three of them should accept the narrow
// interface its subject declares (manualops.SearchStore, resolve.FileStore,
// queryhandlers.QueryStore) and stub only those. The sub-interfaces this
// comment used to point at as the migration target never had a consumer and
// have been deleted; the consumer-declared ones are the target.
type panicStore struct{}

func (*panicStore) RecordNoResult(context.Context, api.MediaType, string, string, api.ProviderID, api.BackoffParams) error {
	panic("not implemented")
}

func (*panicStore) BackedOffProviders(context.Context, api.MediaType, string, string, int) ([]api.ProviderID, error) {
	panic("not implemented")
}

func (*panicStore) GetBackoffItems(context.Context) ([]api.BackoffEntry, error) {
	panic("not implemented")
}

func (*panicStore) GetBackoffByPrefix(context.Context, api.MediaType, string) ([]api.BackoffEntry, error) {
	panic("not implemented")
}

func (*panicStore) ScanCycleStart(context.Context) (time.Time, error) {
	panic("not implemented")
}

func (*panicStore) SetScanCycleStart(context.Context, time.Time) error {
	panic("not implemented")
}

func (*panicStore) ClearScanCycleStart(context.Context) error {
	panic("not implemented")
}

func (*panicStore) SaveDownload(context.Context, *api.DownloadRecord) error {
	panic("not implemented")
}

func (*panicStore) DownloadedRefs(context.Context, api.MediaType, string, string) ([]api.DownloadedRef, error) {
	panic("not implemented")
}

func (*panicStore) CurrentScore(context.Context, api.MediaType, string, string, api.Variant) (score int, at time.Time, found bool, _ error) {
	panic("not implemented")
}

func (*panicStore) GetState(context.Context, *api.StateQuery) ([]api.StateEntry, error) {
	panic("not implemented")
}

func (*panicStore) HistoryMediaIDs(context.Context, api.MediaType, string) ([]string, error) {
	panic("not implemented")
}

func (*panicStore) IsManuallyLocked(context.Context, api.ManualLockKey) (bool, error) {
	panic("not implemented")
}

func (*panicStore) ClearManualLock(context.Context, api.ManualLockKey) error {
	panic("not implemented")
}

func (*panicStore) ManualDownloadCount(context.Context, api.ManualLockKey) (int, error) {
	panic("not implemented")
}

func (*panicStore) ManualSubtitlePaths(context.Context, api.ManualLockKey) ([]string, error) {
	panic("not implemented")
}

func (*panicStore) NextManualNumber(context.Context, api.ManualLockKey) int {
	panic("not implemented")
}

func (*panicStore) GetManualLocks(context.Context) ([]api.ManualLockEntry, error) {
	panic("not implemented")
}

func (*panicStore) RecordSubtitleFiles(context.Context, api.MediaType, string, []api.SubtitleFile) (bool, error) {
	panic("not implemented")
}

func (*panicStore) UpsertSubtitleFile(context.Context, api.MediaType, string, *api.SubtitleFile) error {
	panic("not implemented")
}

func (*panicStore) GetSubtitleFiles(context.Context, api.MediaType, string) ([]api.SubtitleEntry, error) {
	panic("not implemented")
}

func (*panicStore) DeleteSubtitleFile(context.Context, api.MediaType, string, string, api.Variant, api.SubtitleSource, string) error {
	panic("not implemented")
}

func (*panicStore) RecordScanState(context.Context, *api.ScanRecord) error {
	panic("not implemented")
}

func (*panicStore) GetScanStates(context.Context, api.MediaType, string) ([]api.ScanStateRow, error) {
	panic("not implemented")
}

func (*panicStore) RecentlyScanned(context.Context, time.Time) (map[string]bool, error) {
	panic("not implemented")
}
func (*panicStore) TotalSubtitleFiles(context.Context) (int, error) { panic("not implemented") }
func (*panicStore) LastScanTime(context.Context) (string, error)    { panic("not implemented") }
func (*panicStore) SetSyncOffset(context.Context, string, int64) error {
	panic("not implemented")
}

func (*panicStore) GetSyncOffset(context.Context, string) (int64, error) {
	panic("not implemented")
}

func (*panicStore) GetPollTimestamp(context.Context, api.PollKey) (time.Time, error) {
	panic("not implemented")
}

func (*panicStore) SetPollTimestamp(context.Context, api.PollKey, time.Time) error {
	panic("not implemented")
}

func (*panicStore) Stats(context.Context) (downloads, backoffs int, _ error) {
	panic("not implemented")
}

func (*panicStore) DeleteStateByPaths(context.Context, []string) (api.CleanupResult, error) {
	panic("not implemented")
}

func (*panicStore) CleanupDrift(context.Context, api.ConfigDrift) error {
	panic("not implemented")
}

func (*panicStore) ReconcileState(context.Context) (api.ReconcileResult, error) {
	panic("not implemented")
}
func (*panicStore) Close(context.Context) error { panic("not implemented") }
