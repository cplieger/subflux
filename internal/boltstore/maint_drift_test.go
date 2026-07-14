package boltstore

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// driftBP seeds search_attempts rows through RecordNoResult in the drift tests.
var driftBP = api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}

// seedAttempt records one search_attempts row for the (mt, mid, lang, provider)
// quad through the production RecordNoResult path (which maintains the
// attempts counter).
func seedAttempt(t *testing.T, db *DB, mt api.MediaType, mid, lang string, p api.ProviderID) {
	t.Helper()
	if err := db.RecordNoResult(context.Background(), mt, mid, lang, p, driftBP); err != nil {
		t.Fatalf("RecordNoResult(%s/%s/%s/%s): %v", mt, mid, lang, p, err)
	}
}

// assertBackoffConsistent asserts the attempts counter and a raw walk of the
// search_attempts primary agree on the row count. CleanupDrift must keep the
// counter consistent with the primary after deletions (Requirement 8.1).
func assertBackoffConsistent(t *testing.T, db *DB, wantRows int) {
	t.Helper()
	_, attempts, _ := mustStats(t, db)
	if attempts != wantRows {
		t.Errorf("Stats().attempts = %d, want %d", attempts, wantRows)
	}
	if n := attemptRowCount(t, db); n != wantRows {
		t.Errorf("search_attempts rows = %d, want %d", n, wantRows)
	}
}

// TestCleanupDrift_emptyIsNoop leaves seeded backoff untouched when the drift
// is empty.
func TestCleanupDrift_emptyIsNoop(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "os")
	seedAttempt(t, db, api.MediaTypeMovie, "id2", "fr", "bs")

	if err := db.CleanupDrift(ctx, api.ConfigDrift{}); err != nil {
		t.Fatalf("CleanupDrift(empty): %v", err)
	}
	assertBackoffConsistent(t, db, 2)
}

// TestCleanupDrift_adaptiveDisabledClearsAll clears every backoff row when
// adaptive search is disabled (Requirement 7.8), and the per-language/provider
// fields are ignored once the blanket clear runs.
func TestCleanupDrift_adaptiveDisabledClearsAll(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "os")
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "fr", "bs")
	seedAttempt(t, db, api.MediaTypeMovie, "id2", "es", "sd")
	assertBackoffConsistent(t, db, 3)

	if err := db.CleanupDrift(ctx, api.ConfigDrift{
		AdaptiveDisabled: true,
		// These would normally clear a subset, but adaptive-disabled subsumes
		// them; the result must still be a full clear.
		RemovedLanguages: []string{"en"},
		RemovedProviders: []api.ProviderID{"os"},
	}); err != nil {
		t.Fatalf("CleanupDrift(adaptive disabled): %v", err)
	}
	assertBackoffConsistent(t, db, 0)
}

// TestCleanupDrift_removedLanguages deletes only the rows whose language left
// the config, preserving kept-language rows (Requirement 7.7).
func TestCleanupDrift_removedLanguages(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	// Two languages across two providers and two media items; "fr" and "de"
	// are removed, "en" is kept.
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "os")
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "fr", "os")
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "de", "bs")
	seedAttempt(t, db, api.MediaTypeMovie, "id2", "en", "bs")
	seedAttempt(t, db, api.MediaTypeMovie, "id2", "fr", "bs")
	assertBackoffConsistent(t, db, 5)

	if err := db.CleanupDrift(ctx, api.ConfigDrift{RemovedLanguages: []string{"fr", "de"}}); err != nil {
		t.Fatalf("CleanupDrift(removed languages): %v", err)
	}

	// Only the two "en" rows survive.
	assertBackoffConsistent(t, db, 2)
	if _, found := readAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "os"); !found {
		t.Error("kept language row episode/id1/en/os was deleted")
	}
	if _, found := readAttempt(t, db, api.MediaTypeMovie, "id2", "en", "bs"); !found {
		t.Error("kept language row movie/id2/en/bs was deleted")
	}
	if _, found := readAttempt(t, db, api.MediaTypeEpisode, "id1", "fr", "os"); found {
		t.Error("removed language row episode/id1/fr/os survived")
	}
	if _, found := readAttempt(t, db, api.MediaTypeEpisode, "id1", "de", "bs"); found {
		t.Error("removed language row episode/id1/de/bs survived")
	}
}

// TestCleanupDrift_removedProviders deletes only the rows whose provider left
// the config, preserving kept-provider rows (Requirement 7.7).
func TestCleanupDrift_removedProviders(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "os")
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "bs")
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "fr", "sd")
	seedAttempt(t, db, api.MediaTypeMovie, "id2", "en", "bs")
	assertBackoffConsistent(t, db, 4)

	if err := db.CleanupDrift(ctx, api.ConfigDrift{RemovedProviders: []api.ProviderID{"bs", "sd"}}); err != nil {
		t.Fatalf("CleanupDrift(removed providers): %v", err)
	}

	// Only the single "os" row survives.
	assertBackoffConsistent(t, db, 1)
	if _, found := readAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "os"); !found {
		t.Error("kept provider row episode/id1/en/os was deleted")
	}
	if _, found := readAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "bs"); found {
		t.Error("removed provider row episode/id1/en/bs survived")
	}
	if _, found := readAttempt(t, db, api.MediaTypeMovie, "id2", "en", "bs"); found {
		t.Error("removed provider row movie/id2/en/bs survived")
	}
}

// TestCleanupDrift_combinedLanguagesAndProviders applies both removed-language
// and removed-provider cleanup in one call: a row is deleted if EITHER its
// language or its provider drifted out.
func TestCleanupDrift_combinedLanguagesAndProviders(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	seedAttempt(t, db, api.MediaTypeMovie, "id1", "en", "os") // kept
	seedAttempt(t, db, api.MediaTypeMovie, "id1", "fr", "os") // removed language
	seedAttempt(t, db, api.MediaTypeMovie, "id1", "en", "bs") // removed provider
	seedAttempt(t, db, api.MediaTypeMovie, "id1", "fr", "bs") // removed both
	assertBackoffConsistent(t, db, 4)

	if err := db.CleanupDrift(ctx, api.ConfigDrift{
		RemovedLanguages: []string{"fr"},
		RemovedProviders: []api.ProviderID{"bs"},
	}); err != nil {
		t.Fatalf("CleanupDrift(combined): %v", err)
	}

	assertBackoffConsistent(t, db, 1)
	if _, found := readAttempt(t, db, api.MediaTypeMovie, "id1", "en", "os"); !found {
		t.Error("kept row movie/id1/en/os was deleted")
	}
}

// TestCleanupDrift_noMatchingDriftIsNoop leaves backoff intact when the removed
// languages and providers match no stored row.
func TestCleanupDrift_noMatchingDriftIsNoop(t *testing.T) {
	db, _ := openTemp(t)
	ctx := context.Background()
	seedAttempt(t, db, api.MediaTypeEpisode, "id1", "en", "os")
	seedAttempt(t, db, api.MediaTypeMovie, "id2", "fr", "bs")
	assertBackoffConsistent(t, db, 2)

	if err := db.CleanupDrift(ctx, api.ConfigDrift{
		RemovedLanguages: []string{"de"},
		RemovedProviders: []api.ProviderID{"sd"},
	}); err != nil {
		t.Fatalf("CleanupDrift(no match): %v", err)
	}
	assertBackoffConsistent(t, db, 2)
}
