package testsupport

import (
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/server/coverage"
	"github.com/cplieger/subflux/internal/server/coveragehandlers"
	"github.com/cplieger/subflux/internal/server/filehandlers"
	"github.com/cplieger/subflux/internal/server/manualops"
	"github.com/cplieger/subflux/internal/server/polling"
	"github.com/cplieger/subflux/internal/server/queryhandlers"
	"github.com/cplieger/subflux/internal/server/resolve"
	"github.com/cplieger/subflux/internal/server/scanning"
	"github.com/cplieger/subflux/internal/server/scheduler"
	"github.com/cplieger/subflux/internal/server/synchandlers"
)

// TestNopStoreContract verifies that NopStore satisfies the same basic
// behavioral guarantees as the real store.DB: no panics, no errors on
// standard operations, and consistent zero-value returns.
func TestNopStoreContract(t *testing.T) {
	t.Parallel()
	s := &NopStore{}
	ctx := t.Context()

	t.Run("RecordNoResult_does_not_error", func(t *testing.T) {
		t.Parallel()
		err := s.RecordNoResult(ctx, api.MediaTypeMovie, "tmdb-1", "eng", "os",
			api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2})
		if err != nil {
			t.Fatalf("RecordNoResult: %v", err)
		}
	})

	t.Run("BackedOffProviders_returns_nil_no_error", func(t *testing.T) {
		t.Parallel()
		got, err := s.BackedOffProviders(ctx, api.MediaTypeMovie, "tmdb-2", "eng", 5)
		if err != nil {
			t.Fatalf("BackedOffProviders: %v", err)
		}
		if got != nil {
			t.Errorf("BackedOffProviders = %v, want nil", got)
		}
	})

	t.Run("SaveDownload_does_not_error", func(t *testing.T) {
		t.Parallel()
		err := s.SaveDownload(ctx, &api.DownloadRecord{
			MediaType: api.MediaTypeMovie, MediaID: "tmdb-3",
			Language: "eng", ProviderName: "os", ReleaseName: "R",
			Path: "/sub.srt", Score: 80,
		})
		if err != nil {
			t.Fatalf("SaveDownload: %v", err)
		}
	})

	t.Run("GetBackoffItems_returns_nil_no_error", func(t *testing.T) {
		t.Parallel()
		got, err := s.GetBackoffItems(ctx)
		if err != nil {
			t.Fatalf("GetBackoffItems: %v", err)
		}
		if got != nil {
			t.Errorf("GetBackoffItems = %v, want nil", got)
		}
	})

	t.Run("GetState_returns_nil_no_error", func(t *testing.T) {
		t.Parallel()
		got, err := s.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeMovie, Language: "eng", Limit: 50})
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
		if got != nil {
			t.Errorf("GetState = %v, want nil", got)
		}
	})

	t.Run("IsManuallyLocked_returns_false_no_error", func(t *testing.T) {
		t.Parallel()
		locked, err := s.IsManuallyLocked(ctx, api.ManualLockKey{MediaType: api.MediaTypeMovie, MediaID: "tmdb-4", Language: "eng", Variant: api.VariantStandard})
		if err != nil {
			t.Fatalf("IsManuallyLocked: %v", err)
		}
		if locked {
			t.Error("IsManuallyLocked = true, want false")
		}
	})

	t.Run("SetPollTimestamp_does_not_error", func(t *testing.T) {
		t.Parallel()
		err := s.SetPollTimestamp(ctx, api.PollKeySonarr, time.Now())
		if err != nil {
			t.Fatalf("SetPollTimestamp: %v", err)
		}
	})

	t.Run("Stats_returns_zero_no_error", func(t *testing.T) {
		t.Parallel()
		dl, att, err := s.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
		if dl != 0 || att != 0 {
			t.Errorf("Stats = (%d, %d), want (0, 0)", dl, att)
		}
	})
}

// nopStoreConsumers is the union of every narrow store interface NopStore is
// installed as across the app. It lives in this file, not beside NopStore,
// because a production file in testsupport must not pull eleven packages into
// the build graph to hold one assertion — a _test.go file's imports are
// test-only, so the check costs nothing.
//
// This is what replaced `var _ api.Store = (*NopStore)(nil)`. The old
// assertion pinned the fake to a hand-written 36-method list; this one pins it
// to what consumers actually ask for, so a package that adds a method to its
// own interface breaks HERE with one readable error instead of at each of the
// dozen sites that install the fake. server.Store is deliberately absent: it
// embeds most of these and internal/server's own New(&NopStore{}) call is its
// compile-time check.
type nopStoreConsumers interface {
	search.SearchStore
	scanning.ScanStore
	scanning.BackoffPrefixReader
	scheduler.Store
	queryhandlers.QueryStore
	synchandlers.SyncStore
	filehandlers.FileStore
	coveragehandlers.CoverageStore
	manualops.DownloadStore
	polling.PollerStore
	resolve.FileStore
	coverage.FileReader
	queryhandlers.StatsStore
}

var _ nopStoreConsumers = (*NopStore)(nil)
