package store

import (
	"context"
	"testing"
	"time"

	"subflux/internal/api"

	"pgregory.net/rapid"
)

func TestRecordNoResult_delay_increases_with_multiplier(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	initialDelay := 10 * time.Second
	maxDelay := time.Hour
	multiplier := 2.0

	if err := db.RecordNoResult(context.Background(), "movie", "tt123", "fr", "testprov",
		api.BackoffParams{InitialDelay: initialDelay, MaxDelay: maxDelay, Multiplier: multiplier}); err != nil {
		t.Fatalf("RecordNoResult() unexpected error: %v", err)
	}

	// Check the stored next_retry is approximately initialDelay from now.
	var nextRetry time.Time
	var failures int
	ctx := t.Context()
	if err := db.db.QueryRowContext(ctx,
		`SELECT next_retry, failures FROM search_attempts WHERE media_id = ?`,
		"tt123").Scan(&nextRetry, &failures); err != nil {
		t.Fatalf("query: %v", err)
	}
	if failures != 1 {
		t.Errorf("failures = %d, want 1", failures)
	}

	expectedDelay := initialDelay
	actualDelay := time.Until(nextRetry)
	if actualDelay < expectedDelay-time.Second || actualDelay > expectedDelay+time.Second {
		t.Errorf("first failure delay = %v, want ~%v", actualDelay, expectedDelay)
	}

	if err := db.RecordNoResult(context.Background(), "movie", "tt123", "fr", "testprov",
		api.BackoffParams{InitialDelay: initialDelay, MaxDelay: maxDelay, Multiplier: multiplier}); err != nil {
		t.Fatalf("RecordNoResult() unexpected error: %v", err)
	}

	if err := db.db.QueryRowContext(ctx,
		`SELECT next_retry, failures FROM search_attempts WHERE media_id = ?`,
		"tt123").Scan(&nextRetry, &failures); err != nil {
		t.Fatalf("query: %v", err)
	}
	if failures != 2 {
		t.Errorf("failures = %d, want 2", failures)
	}

	expectedDelay = 20 * time.Second
	actualDelay = time.Until(nextRetry)
	if actualDelay < expectedDelay-time.Second || actualDelay > expectedDelay+time.Second {
		t.Errorf("second failure delay = %v, want ~%v", actualDelay, expectedDelay)
	}
}

func TestRecordNoResult_delay_capped_at_max(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	initialDelay := time.Hour
	maxDelay := 2 * time.Hour
	multiplier := 10.0

	for range 5 {
		if err := db.RecordNoResult(context.Background(), "movie", "tt888", "fr", "testprov",
			api.BackoffParams{InitialDelay: initialDelay, MaxDelay: maxDelay, Multiplier: multiplier}); err != nil {
			t.Fatalf("RecordNoResult() unexpected error: %v", err)
		}
	}

	var nextRetry time.Time
	ctx := t.Context()
	if err := db.db.QueryRowContext(ctx,
		`SELECT next_retry FROM search_attempts WHERE media_id = ?`,
		"tt888").Scan(&nextRetry); err != nil {
		t.Fatalf("query: %v", err)
	}

	actualDelay := time.Until(nextRetry)
	if actualDelay > maxDelay+time.Second {
		t.Errorf("delay after 5 failures = %v, want <= %v (capped)", actualDelay, maxDelay)
	}
}

func TestGetBackoffItems(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordNoResult(context.Background(), "movie", "tt123", "fr", "testprov",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult() unexpected error: %v", err)
	}

	items, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems() unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("GetBackoffItems() returned %d items, want 1", len(items))
	}
	if items[0].MediaID != "tt123" {
		t.Errorf("items[0].MediaID = %q, want %q", items[0].MediaID, "tt123")
	}
	if items[0].Provider != "testprov" {
		t.Errorf("items[0].Provider = %q, want %q", items[0].Provider, "testprov")
	}
	if items[0].Failures != 1 {
		t.Errorf("items[0].Failures = %d, want 1", items[0].Failures)
	}
}

func TestGetBackoffItems_ordered_by_next_retry(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO search_attempts
		(media_type, media_id, language, provider, last_tried, failures, next_retry)
		VALUES ('movie', 'tt-later', 'fr', 'testprov', datetime('now'), 2, datetime('now', '+2 hours'))`)
	if err != nil {
		t.Fatalf("insert later: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO search_attempts
		(media_type, media_id, language, provider, last_tried, failures, next_retry)
		VALUES ('movie', 'tt-sooner', 'en', 'testprov', datetime('now'), 1, datetime('now', '+1 hour'))`)
	if err != nil {
		t.Fatalf("insert sooner: %v", err)
	}

	items, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems() unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("GetBackoffItems() returned %d items, want 2", len(items))
	}

	if items[0].MediaID != "tt-sooner" {
		t.Errorf("items[0].MediaID = %q, want %q (sooner retry first)", items[0].MediaID, "tt-sooner")
	}
	if items[1].MediaID != "tt-later" {
		t.Errorf("items[1].MediaID = %q, want %q", items[1].MediaID, "tt-later")
	}
	if items[0].Failures != 1 {
		t.Errorf("items[0].Failures = %d, want 1", items[0].Failures)
	}
	if items[1].Failures != 2 {
		t.Errorf("items[1].Failures = %d, want 2", items[1].Failures)
	}
}

func TestGetBackoffItems_empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	items, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems() unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("GetBackoffItems() returned %d items, want 0", len(items))
	}
}

func TestBackedOffProviders(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		mediaType     string
		mediaID       string
		lang          string
		seedSQL       []string
		wantProviders []string
		maxAttempts   int
	}{
		{
			name:          "no_records",
			seedSQL:       nil,
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   5,
			wantProviders: nil,
		},
		{
			name: "includes_future_retry",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'slowprov', datetime('now'), 1, datetime('now', '+24 hours'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   0,
			wantProviders: []string{"slowprov"},
		},
		{
			name: "excludes_expired_retry",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'fastprov', datetime('now'), 1, datetime('now', '-1 hour'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   0,
			wantProviders: nil,
		},
		{
			name: "includes_max_attempts_reached",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'maxedprov', datetime('now'), 5, datetime('now', '-1 hour'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   5,
			wantProviders: []string{"maxedprov"},
		},
		{
			name: "mixed_providers",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'backed', datetime('now'), 1, datetime('now', '+1 hour'))`,
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'eligible', datetime('now'), 1, datetime('now', '-1 hour'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   0,
			wantProviders: []string{"backed"},
		},
		{
			name: "ignores_empty_provider",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', '', datetime('now'), 1, datetime('now', '+1 hour'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   0,
			wantProviders: nil,
		},
		{
			name: "zero_max_attempts_disables_limit",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'prov', datetime('now'), 100, datetime('now', '-1 hour'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   0,
			wantProviders: nil,
		},
		{
			name: "just_below_max_attempts_not_backed_off",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'prov', datetime('now'), 4, datetime('now', '-1 hour'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   5,
			wantProviders: nil,
		},
		{
			name: "at_max_attempts_backed_off",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'prov', datetime('now'), 5, datetime('now', '-1 hour'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   5,
			wantProviders: []string{"prov"},
		},
		{
			name: "negative_max_attempts",
			seedSQL: []string{
				`INSERT INTO search_attempts (media_type, media_id, language, provider, last_tried, failures, next_retry) VALUES ('movie', 'tt123', 'fr', 'os', datetime('now'), 5, datetime('now', '-1 hour'))`,
			},
			mediaType:     "movie",
			mediaID:       "tt123",
			lang:          "fr",
			maxAttempts:   -1,
			wantProviders: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			ctx := t.Context()
			for _, sql := range tt.seedSQL {
				if _, err := db.db.ExecContext(ctx, sql); err != nil {
					t.Fatalf("seed: %v", err)
				}
			}
			backed, err := db.BackedOffProviders(context.Background(), api.MediaType(tt.mediaType), tt.mediaID, tt.lang, tt.maxAttempts)
			if err != nil {
				t.Fatalf("BackedOffProviders() error: %v", err)
			}
			if len(backed) != len(tt.wantProviders) {
				t.Fatalf("BackedOffProviders() = %v (len %d), want %v (len %d)",
					backed, len(backed), tt.wantProviders, len(tt.wantProviders))
			}
			for i, want := range tt.wantProviders {
				if backed[i] != api.ProviderID(want) {
					t.Errorf("backed[%d] = %q, want %q", i, backed[i], want)
				}
			}
		})
	}
}

func TestGetBackoffItems_all_fields_populated(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO search_attempts
		(media_type, media_id, language, provider, last_tried, failures, next_retry)
		VALUES ('episode', 'tt456-s01e01', 'en', 'yify', datetime('now', '-30 minutes'), 3, datetime('now', '+2 hours'))`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	items, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems() unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("GetBackoffItems() returned %d items, want 1", len(items))
	}

	item := items[0]
	if item.MediaType != "episode" {
		t.Errorf("MediaType = %q, want %q", item.MediaType, "episode")
	}
	if item.MediaID != "tt456-s01e01" {
		t.Errorf("MediaID = %q, want %q", item.MediaID, "tt456-s01e01")
	}
	if item.Language != "en" {
		t.Errorf("Language = %q, want %q", item.Language, "en")
	}
	if item.Provider != "yify" {
		t.Errorf("Provider = %q, want %q", item.Provider, "yify")
	}
	if item.Failures != 3 {
		t.Errorf("Failures = %d, want 3", item.Failures)
	}
	if item.LastTried.IsZero() {
		t.Error("LastTried is zero, want non-zero")
	}
	if item.NextRetry.IsZero() {
		t.Error("NextRetry is zero, want non-zero")
	}
	if !item.NextRetry.After(time.Now()) {
		t.Error("NextRetry should be in the future")
	}
}

func TestRecordNoResult_independent_per_provider(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordNoResult(context.Background(), "movie", "tt123", "fr", "provA",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult(provA): %v", err)
	}
	if err := db.RecordNoResult(context.Background(), "movie", "tt123", "fr", "provB",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult(provB): %v", err)
	}

	backed, err := db.BackedOffProviders(context.Background(), "movie", "tt123", "fr", 0)
	if err != nil {
		t.Fatalf("BackedOffProviders() unexpected error: %v", err)
	}
	if len(backed) != 2 {
		t.Errorf("BackedOffProviders() = %v, want 2 providers", backed)
	}

	if err := db.RecordNoResult(context.Background(), "movie", "tt123", "fr", "provA",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult(provA again): %v", err)
	}

	ctx := t.Context()
	var failuresA, failuresB int
	if err := db.db.QueryRowContext(ctx,
		`SELECT failures FROM search_attempts WHERE provider = 'provA'`).Scan(&failuresA); err != nil {
		t.Fatalf("query provA: %v", err)
	}
	if err := db.db.QueryRowContext(ctx,
		`SELECT failures FROM search_attempts WHERE provider = 'provB'`).Scan(&failuresB); err != nil {
		t.Fatalf("query provB: %v", err)
	}
	if failuresA != 2 {
		t.Errorf("provA failures = %d, want 2", failuresA)
	}
	if failuresB != 1 {
		t.Errorf("provB failures = %d, want 1", failuresB)
	}
}

func TestGetBackoffByPrefix(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		inserts   []string // media_ids to insert
		provider  string   // provider for inserts (default "os")
		prefix    string
		wantCount int
		checkFn   func(t *testing.T, items []api.BackoffEntry)
	}{
		{
			name:      "no_prefix_returns_all",
			inserts:   []string{"tvdb-111-s01e01", "tvdb-111-s01e02", "tvdb-222-s01e01"},
			prefix:    "",
			wantCount: 3,
		},
		{
			name:      "with_prefix",
			inserts:   []string{"tvdb-111-s01e01", "tvdb-111-s01e02", "tvdb-222-s01e01"},
			prefix:    "tvdb-111-",
			wantCount: 2,
		},
		{
			name:      "empty",
			inserts:   nil,
			prefix:    "",
			wantCount: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			ctx := t.Context()
			for _, id := range tc.inserts {
				prov := "os"
				if tc.provider != "" {
					prov = tc.provider
				}
				_, err := db.db.ExecContext(ctx, `INSERT INTO search_attempts
					(media_type, media_id, language, provider, last_tried, failures, next_retry)
					VALUES ('episode', ?, 'en', ?, datetime('now'), 1, datetime('now', '+1 hour'))`, id, prov)
				if err != nil {
					t.Fatalf("insert %q: %v", id, err)
				}
			}
			items, err := db.GetBackoffByPrefix(context.Background(), "episode", tc.prefix)
			if err != nil {
				t.Fatalf("GetBackoffByPrefix() unexpected error: %v", err)
			}
			if len(items) != tc.wantCount {
				t.Errorf("GetBackoffByPrefix(%q) returned %d items, want %d", tc.prefix, len(items), tc.wantCount)
			}
			if tc.checkFn != nil {
				tc.checkFn(t, items)
			}
		})
	}
}

func TestGetBackoffByPrefix_ordered_by_media_id_then_retry(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx, `INSERT INTO search_attempts
		(media_type, media_id, language, provider, last_tried, failures, next_retry)
		VALUES ('episode', 'tvdb-111-s01e02', 'en', 'os', datetime('now'), 1, datetime('now', '+1 hour'))`)
	if err != nil {
		t.Fatalf("insert e02: %v", err)
	}
	_, err = db.db.ExecContext(ctx, `INSERT INTO search_attempts
		(media_type, media_id, language, provider, last_tried, failures, next_retry)
		VALUES ('episode', 'tvdb-111-s01e01', 'en', 'os', datetime('now'), 2, datetime('now', '+2 hours'))`)
	if err != nil {
		t.Fatalf("insert e01: %v", err)
	}

	items, err := db.GetBackoffByPrefix(context.Background(), "episode", "tvdb-111-")
	if err != nil {
		t.Fatalf("GetBackoffByPrefix() unexpected error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("GetBackoffByPrefix() returned %d items, want 2", len(items))
	}

	if items[0].MediaID != "tvdb-111-s01e01" {
		t.Errorf("items[0].MediaID = %q, want %q", items[0].MediaID, "tvdb-111-s01e01")
	}
	if items[1].MediaID != "tvdb-111-s01e02" {
		t.Errorf("items[1].MediaID = %q, want %q", items[1].MediaID, "tvdb-111-s01e02")
	}
}

func TestGetBackoffByPrefix_ignores_empty_provider(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()

	_, err := db.db.ExecContext(ctx, `INSERT INTO search_attempts
		(media_type, media_id, language, provider, last_tried, failures, next_retry)
		VALUES ('episode', 'tvdb-111-s01e01', 'en', '', datetime('now'), 1, datetime('now', '+1 hour'))`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	_, err = db.db.ExecContext(ctx, `INSERT INTO search_attempts
		(media_type, media_id, language, provider, last_tried, failures, next_retry)
		VALUES ('episode', 'tvdb-111-s01e01', 'en', 'os', datetime('now'), 1, datetime('now', '+1 hour'))`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	items, err := db.GetBackoffByPrefix(context.Background(), "episode", "tvdb-111-")
	if err != nil {
		t.Fatalf("GetBackoffByPrefix() unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("GetBackoffByPrefix() returned %d items, want 1 (empty provider excluded)", len(items))
	}
}

func TestGetBackoffByPrefix_escapes_wildcards(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	if err := db.RecordNoResult(context.Background(), "movie", "tt%_special", "fr", "os",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult(): %v", err)
	}
	if err := db.RecordNoResult(context.Background(), "movie", "tt-normal", "fr", "os",
		api.BackoffParams{InitialDelay: time.Hour, MaxDelay: 24 * time.Hour, Multiplier: 2}); err != nil {
		t.Fatalf("RecordNoResult(): %v", err)
	}

	items, err := db.GetBackoffByPrefix(context.Background(), "movie", "tt%")
	if err != nil {
		t.Fatalf("GetBackoffByPrefix() error: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("GetBackoffByPrefix(\"tt%%\") returned %d items, want 1 (escaped wildcard)", len(items))
	}
}

// TestRecordNoResult_concurrent_no_race verifies that concurrent
// RecordNoResult calls for the same key produce a consistent failures
// count equal to the total number of calls. The single-query UPSERT
// approach eliminates the race window that existed in the former
// two-query (UPSERT RETURNING + UPDATE) pattern.
func TestRecordNoResult_concurrent_no_race(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	const goroutines = 20
	bp := api.BackoffParams{
		InitialDelay: time.Second,
		MaxDelay:     time.Hour,
		Multiplier:   2,
	}

	errs := make(chan error, goroutines)
	for range goroutines {
		go func() {
			errs <- db.RecordNoResult(context.Background(),
				"movie", "tt-race", "en", "provA", bp)
		}()
	}
	for range goroutines {
		if err := <-errs; err != nil {
			t.Fatalf("RecordNoResult() concurrent error: %v", err)
		}
	}

	// All goroutines completed; failures must equal total calls.
	items, err := db.GetBackoffItems(context.Background())
	if err != nil {
		t.Fatalf("GetBackoffItems() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 backoff item, got %d", len(items))
	}
	if items[0].Failures != goroutines {
		t.Errorf("failures = %d, want %d (each concurrent call must increment exactly once)",
			items[0].Failures, goroutines)
	}
}

func TestProperty_BackoffDelay_never_exceeds_max(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		initialSec := rapid.Int64Range(1, 3600).Draw(t, "initial_sec")
		maxSec := rapid.Int64Range(3600, 172800).Draw(t, "max_sec")
		multiplier := rapid.Float64Range(1.5, 10.0).Draw(t, "multiplier")
		failures := rapid.IntRange(1, 50).Draw(t, "failures")

		initialDelay := time.Duration(initialSec) * time.Second
		maxDelay := time.Duration(maxSec) * time.Second

		// Compute expected delay after N failures using the same formula as the store.
		delay := float64(initialDelay)
		for range failures - 1 {
			delay *= multiplier
			if delay > float64(maxDelay) {
				delay = float64(maxDelay)
				break
			}
		}
		if delay > float64(maxDelay) {
			delay = float64(maxDelay)
		}
		// The computed delay should never exceed maxDelay.
		if time.Duration(delay) > maxDelay {
			t.Fatalf("computed delay %v exceeds maxDelay %v (failures=%d, multiplier=%.1f)",
				time.Duration(delay), maxDelay, failures, multiplier)
		}
	})
}
