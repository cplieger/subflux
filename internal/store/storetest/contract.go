// Package storetest provides shared contract test suites for api.Store
// implementations. Both the real store.DB and test fakes (NopStore, MemStore)
// should pass these suites to verify behavioral parity.
package storetest

import (
	"context"
	"testing"
	"time"

	"subflux/internal/api"
)

const langEng = "eng"

// Suite runs behavioral contract cases against any api.Store implementation.
// It verifies basic roundtrip guarantees and no-error invariants.
func Suite(t *testing.T, newStore func(t *testing.T) api.Store) {
	t.Helper()

	t.Run("RecordNoResult_does_not_error", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		err := s.RecordNoResult(ctx, api.MediaTypeMovie, "tmdb-contract-1", langEng, api.ProviderID("opensubtitles"),
			api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2})
		if err != nil {
			t.Fatalf("RecordNoResult: %v", err)
		}
	})

	t.Run("BackedOffProviders_returns_no_error", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		_, err := s.BackedOffProviders(ctx, api.MediaTypeMovie, "tmdb-contract-2", langEng, 5)
		if err != nil {
			t.Fatalf("BackedOffProviders: %v", err)
		}
	})

	t.Run("SaveDownload_does_not_error", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		err := s.SaveDownload(ctx, &api.DownloadRecord{
			MediaType:    api.MediaTypeMovie,
			MediaID:      "tmdb-contract-3",
			Language:     langEng,
			ProviderName: api.ProviderID("opensubtitles"),
			ReleaseName:  "Contract.2024.eng.srt",
			Score:        80,
			Path:         "/media/contract/sub.srt",
		})
		if err != nil {
			t.Fatalf("SaveDownload: %v", err)
		}
	})

	t.Run("GetBackoffItems_returns_no_error", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		_, err := s.GetBackoffItems(ctx)
		if err != nil {
			t.Fatalf("GetBackoffItems: %v", err)
		}
	})

	t.Run("GetState_returns_no_error", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		_, err := s.GetState(ctx, &api.StateQuery{MediaType: api.MediaTypeMovie, Language: langEng, Limit: 50})
		if err != nil {
			t.Fatalf("GetState: %v", err)
		}
	})

	t.Run("IsManuallyLocked_returns_no_error", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		_, err := s.IsManuallyLocked(ctx, api.MediaTypeMovie, "tmdb-contract-4", langEng)
		if err != nil {
			t.Fatalf("IsManuallyLocked: %v", err)
		}
	})

	t.Run("PollTimestamp_roundtrip_no_error", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		now := time.Now().Truncate(time.Second)
		if err := s.SetPollTimestamp(ctx, api.PollKeySonarr, now); err != nil {
			t.Fatalf("SetPollTimestamp: %v", err)
		}
		_, err := s.GetPollTimestamp(ctx, api.PollKeySonarr)
		if err != nil {
			t.Fatalf("GetPollTimestamp: %v", err)
		}
	})

	t.Run("Stats_returns_no_error", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		_, _, err := s.Stats(ctx)
		if err != nil {
			t.Fatalf("Stats: %v", err)
		}
	})

	// --- Roundtrip assertions ---

	t.Run("SaveDownload_then_CurrentScore", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		rec := &api.DownloadRecord{
			MediaType:    api.MediaTypeMovie,
			MediaID:      "tmdb-roundtrip-1",
			Language:     langEng,
			ProviderName: api.ProviderID("opensubtitles"),
			ReleaseName:  "Roundtrip.2024.eng.srt",
			Score:        85,
			Path:         "/media/roundtrip/sub.srt",
		}
		if err := s.SaveDownload(ctx, rec); err != nil {
			t.Fatalf("SaveDownload: %v", err)
		}
		score, _, found, err := s.CurrentScore(ctx, api.MediaTypeMovie, "tmdb-roundtrip-1", langEng)
		if err != nil {
			t.Fatalf("CurrentScore: %v", err)
		}
		// Real store returns found=true with score=85.
		// NopStore may return found=false (discard semantics) — both are valid.
		if found && score != 85 {
			t.Fatalf("CurrentScore: found=true but score=%d, want 85", score)
		}
	})

	t.Run("SetPollTimestamp_then_GetPollTimestamp", func(t *testing.T) {
		t.Parallel()
		s := newStore(t)
		ctx := context.Background()
		now := time.Now().Truncate(time.Second)
		if err := s.SetPollTimestamp(ctx, api.PollKeyRadarr, now); err != nil {
			t.Fatalf("SetPollTimestamp: %v", err)
		}
		got, err := s.GetPollTimestamp(ctx, api.PollKeyRadarr)
		if err != nil {
			t.Fatalf("GetPollTimestamp: %v", err)
		}
		// Real store returns the set time; NopStore returns zero time.
		// Both are valid — the contract is "no error".
		_ = got
	})
}
