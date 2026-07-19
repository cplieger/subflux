package scanning

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/server/showskip"
)

// These tests pin the S10 earlyStop seeding constraints:
//  1. an episode counts toward the seed only when EVERY currently-enabled
//     provider has an active backoff row for it (a newly added provider
//     instantly zeroes every seed);
//  2. seeding pre-fills the counter only — earlyStop still trips via real
//     outcomes, and a found result resets everything.

type fakeBackoffReader struct {
	entries   []api.BackoffEntry
	err       error
	gotPrefix string
}

func (f *fakeBackoffReader) GetBackoffByPrefix(_ context.Context, _ api.MediaType, prefix string) ([]api.BackoffEntry, error) {
	f.gotPrefix = prefix
	return f.entries, f.err
}

// seedEntry builds an active-or-not backoff row for one episode/provider.
func seedEntry(mediaID string, provider api.ProviderID, lang string, nextRetry time.Time, failures int) api.BackoffEntry {
	return api.BackoffEntry{
		MediaID:   mediaID,
		Provider:  provider,
		Language:  lang,
		NextRetry: nextRetry,
		Failures:  failures,
	}
}

func seedTracker(reader BackoffPrefixReader, enabled []api.ProviderID, maxAttempts int, now time.Time) *seasonTracker {
	set := make(map[api.ProviderID]struct{}, len(enabled))
	for _, p := range enabled {
		set[p] = struct{}{}
	}
	return newSeasonTracker(nil, showskip.New(time.Hour), seedDeps{
		Backoff:     reader,
		Enabled:     set,
		MaxAttempts: maxAttempts,
		Now:         func() time.Time { return now },
	})
}

const seedPrefix = "tvdb-42-s01e"

func TestSeed_saturated_season_trips_on_first_real_no_result(t *testing.T) {
	t.Parallel()
	now := time.Now()
	future := now.Add(24 * time.Hour)
	reader := &fakeBackoffReader{entries: []api.BackoffEntry{
		seedEntry(seedPrefix+"01", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"02", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"03", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"04", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"05", "p1", "fr", future, 2),
	}}
	st := seedTracker(reader, []api.ProviderID{"p1"}, 0, now)

	// Threshold for epCount 10 = max(3, 2) = 3. Seed alone (5) must NOT set
	// earlyStop: the counter is pre-filled, the trip needs real evidence.
	if st.shouldSkipSeason("tt42", 1, "fr") {
		t.Fatal("earlyStop before any recorded outcome; seeding must not set it as a fact")
	}

	st.recordOutcome(context.Background(), "tt42", 1, "fr", seedPrefix, ScanNoResult, 10)
	if !st.shouldSkipSeason("tt42", 1, "fr") {
		t.Error("seeded season did not trip after its first real no-result (seed 5 + 1 >= threshold 3)")
	}
	if reader.gotPrefix != seedPrefix {
		t.Errorf("seed queried prefix %q, want %q", reader.gotPrefix, seedPrefix)
	}
}

func TestSeed_new_provider_zeroes_seed(t *testing.T) {
	t.Parallel()
	now := time.Now()
	future := now.Add(24 * time.Hour)
	// Five episodes fully suppressed for p1 — but p2 is now enabled and has
	// no rows anywhere, so NO episode has zero eligible providers.
	reader := &fakeBackoffReader{entries: []api.BackoffEntry{
		seedEntry(seedPrefix+"01", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"02", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"03", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"04", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"05", "p1", "fr", future, 2),
	}}
	st := seedTracker(reader, []api.ProviderID{"p1", "p2"}, 0, now)

	st.recordOutcome(context.Background(), "tt42", 1, "fr", seedPrefix, ScanNoResult, 10)
	if st.shouldSkipSeason("tt42", 1, "fr") {
		t.Error("season tripped with a fresh provider enabled; new-provider rows-absent must zero the seed")
	}
}

func TestSeed_expired_rows_do_not_count(t *testing.T) {
	t.Parallel()
	now := time.Now()
	past := now.Add(-time.Hour)
	reader := &fakeBackoffReader{entries: []api.BackoffEntry{
		seedEntry(seedPrefix+"01", "p1", "fr", past, 2),
		seedEntry(seedPrefix+"02", "p1", "fr", past, 2),
		seedEntry(seedPrefix+"03", "p1", "fr", past, 2),
		seedEntry(seedPrefix+"04", "p1", "fr", past, 2),
	}}
	st := seedTracker(reader, []api.ProviderID{"p1"}, 0, now)

	st.recordOutcome(context.Background(), "tt42", 1, "fr", seedPrefix, ScanNoResult, 10)
	if st.shouldSkipSeason("tt42", 1, "fr") {
		t.Error("expired backoff rows counted toward the seed; ladder expiry must deflate it")
	}
}

func TestSeed_max_attempts_saturation_counts(t *testing.T) {
	t.Parallel()
	now := time.Now()
	past := now.Add(-time.Hour)
	// NextRetry is past, but failures reached the adaptive ceiling: the row
	// suppresses its provider permanently until cleared.
	reader := &fakeBackoffReader{entries: []api.BackoffEntry{
		seedEntry(seedPrefix+"01", "p1", "fr", past, 5),
		seedEntry(seedPrefix+"02", "p1", "fr", past, 5),
		seedEntry(seedPrefix+"03", "p1", "fr", past, 5),
	}}
	st := seedTracker(reader, []api.ProviderID{"p1"}, 5, now)

	st.recordOutcome(context.Background(), "tt42", 1, "fr", seedPrefix, ScanNoResult, 10)
	if !st.shouldSkipSeason("tt42", 1, "fr") {
		t.Error("maxAttempts-saturated rows did not count toward the seed (3 + 1 >= threshold 3)")
	}
}

func TestSeed_found_resets_seeded_counter(t *testing.T) {
	t.Parallel()
	now := time.Now()
	future := now.Add(24 * time.Hour)
	reader := &fakeBackoffReader{entries: []api.BackoffEntry{
		seedEntry(seedPrefix+"01", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"02", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"03", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"04", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"05", "p1", "fr", future, 2),
	}}
	st := seedTracker(reader, []api.ProviderID{"p1"}, 0, now)

	// A found result on the season's first outcome wipes the seeded count.
	st.recordOutcome(context.Background(), "tt42", 1, "fr", seedPrefix, ScanFound, 10)
	st.recordOutcome(context.Background(), "tt42", 1, "fr", seedPrefix, ScanNoResult, 10)
	if st.shouldSkipSeason("tt42", 1, "fr") {
		t.Error("season tripped after a found result; ScanFound must reset the seeded counter")
	}
}

func TestSeed_partial_provider_suppression_not_counted(t *testing.T) {
	t.Parallel()
	now := time.Now()
	future := now.Add(24 * time.Hour)
	// Each episode has p1 active but p2 eligible: one eligible provider
	// means the episode is still searchable and must not count.
	reader := &fakeBackoffReader{entries: []api.BackoffEntry{
		seedEntry(seedPrefix+"01", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"02", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"03", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"04", "p1", "fr", future, 2),
		seedEntry(seedPrefix+"04", "p2", "fr", past(now), 1), // expired p2 row
	}}
	st := seedTracker(reader, []api.ProviderID{"p1", "p2"}, 0, now)

	st.recordOutcome(context.Background(), "tt42", 1, "fr", seedPrefix, ScanNoResult, 10)
	if st.shouldSkipSeason("tt42", 1, "fr") {
		t.Error("partially-suppressed episodes counted toward the seed")
	}
}

func past(now time.Time) time.Time { return now.Add(-time.Hour) }

func TestSeed_other_language_rows_ignored(t *testing.T) {
	t.Parallel()
	now := time.Now()
	future := now.Add(24 * time.Hour)
	reader := &fakeBackoffReader{entries: []api.BackoffEntry{
		seedEntry(seedPrefix+"01", "p1", "de", future, 2),
		seedEntry(seedPrefix+"02", "p1", "de", future, 2),
		seedEntry(seedPrefix+"03", "p1", "de", future, 2),
	}}
	st := seedTracker(reader, []api.ProviderID{"p1"}, 0, now)

	st.recordOutcome(context.Background(), "tt42", 1, "fr", seedPrefix, ScanNoResult, 10)
	if st.shouldSkipSeason("tt42", 1, "fr") {
		t.Error("another language's rows seeded this language's counter")
	}
}
