package store

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

func TestPollTimestamp_roundtrip(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	got, err := db.GetPollTimestamp(context.Background(), api.PollKeySonarr)
	if err != nil {
		t.Fatalf("GetPollTimestamp(unset): %v", err)
	}
	if !got.IsZero() {
		t.Errorf("GetPollTimestamp(unset) = %v, want zero", got)
	}

	now := time.Now().Truncate(time.Nanosecond)
	if err := db.SetPollTimestamp(context.Background(), api.PollKeySonarr, now); err != nil {
		t.Fatalf("SetPollTimestamp: %v", err)
	}

	got, err = db.GetPollTimestamp(context.Background(), api.PollKeySonarr)
	if err != nil {
		t.Fatalf("GetPollTimestamp(set): %v", err)
	}
	if got.Sub(now).Abs() > time.Microsecond {
		t.Errorf("GetPollTimestamp(sonarr) = %v, want ~%v", got, now)
	}
}

func TestPollTimestamp_update_overwrites(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	t1 := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 6, 15, 12, 30, 0, 0, time.UTC)

	if err := db.SetPollTimestamp(context.Background(), "radarr", t1); err != nil {
		t.Fatalf("SetPollTimestamp t1: %v", err)
	}
	if err := db.SetPollTimestamp(context.Background(), "radarr", t2); err != nil {
		t.Fatalf("SetPollTimestamp t2: %v", err)
	}

	got, err := db.GetPollTimestamp(context.Background(), "radarr")
	if err != nil {
		t.Fatalf("GetPollTimestamp: %v", err)
	}
	if !got.Equal(t2) {
		t.Errorf("GetPollTimestamp(radarr) = %v, want %v (updated)", got, t2)
	}
}

func TestPollTimestamp_independent_keys(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	t1 := time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)

	if err := db.SetPollTimestamp(context.Background(), api.PollKeySonarr, t1); err != nil {
		t.Fatalf("SetPollTimestamp sonarr: %v", err)
	}
	if err := db.SetPollTimestamp(context.Background(), "radarr", t2); err != nil {
		t.Fatalf("SetPollTimestamp radarr: %v", err)
	}

	got1, err := db.GetPollTimestamp(context.Background(), api.PollKeySonarr)
	if err != nil {
		t.Fatalf("GetPollTimestamp sonarr: %v", err)
	}
	got2, err := db.GetPollTimestamp(context.Background(), "radarr")
	if err != nil {
		t.Fatalf("GetPollTimestamp radarr: %v", err)
	}

	if !got1.Equal(t1) {
		t.Errorf("GetPollTimestamp(sonarr) = %v, want %v", got1, t1)
	}
	if !got2.Equal(t2) {
		t.Errorf("GetPollTimestamp(radarr) = %v, want %v", got2, t2)
	}
}

func TestPollTimestamp_roundtrip_property(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	rapid.Check(t, func(t *rapid.T) {
		key := rapid.SampledFrom([]api.PollKey{api.PollKeySonarr, api.PollKeyRadarr}).Draw(t, "key")
		year := rapid.IntRange(2020, 2030).Draw(t, "year")
		month := rapid.IntRange(1, 12).Draw(t, "month")
		day := rapid.IntRange(1, 28).Draw(t, "day")
		hour := rapid.IntRange(0, 23).Draw(t, "hour")
		minute := rapid.IntRange(0, 59).Draw(t, "minute")
		sec := rapid.IntRange(0, 59).Draw(t, "sec")

		ts := time.Date(year, time.Month(month), day, hour, minute, sec, 0, time.UTC)

		if err := db.SetPollTimestamp(context.Background(), key, ts); err != nil {
			t.Fatalf("SetPollTimestamp: %v", err)
		}
		got, err := db.GetPollTimestamp(context.Background(), key)
		if err != nil {
			t.Fatalf("GetPollTimestamp: %v", err)
		}

		if !got.Equal(ts) {
			t.Errorf("PollTimestamp round-trip failed: set %v, got %v", ts, got)
		}
	})
}

// Covers the time.Parse error path in GetPollTimestamp: a non-RFC3339 value
// stored in poll_state should return an error.
func TestGetPollTimestamp_invalid_format_returns_error(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)

	ctx := t.Context()
	_, err := db.db.ExecContext(ctx,
		`INSERT INTO poll_state (key, value) VALUES ('broken', 'not-a-timestamp')`)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}

	got, err := db.GetPollTimestamp(context.Background(), "broken")
	if err == nil {
		t.Errorf("GetPollTimestamp(invalid format) = %v, want error", got)
	}
	if !got.IsZero() {
		t.Errorf("GetPollTimestamp(invalid format) = %v, want zero time", got)
	}
}
