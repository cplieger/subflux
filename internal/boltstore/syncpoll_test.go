package boltstore

import (
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/subflux"
)

// TestSyncOffset_setThenGetRoundTrip asserts a set offset is returned exactly by
// a later get for the same path (Requirement 6.1), including a negative offset
// (subtitle ahead of audio), which exercises the bit-preserving int64<->uint64
// round-trip through be64.
func TestSyncOffset_setThenGetRoundTrip(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()

	cases := []struct {
		name string
		path string
		off  int64
	}{
		{"positive", "/media/movies/m.fr.srt", 1234},
		{"zero", "/media/movies/z.en.srt", 0},
		{"negative", "/media/tv/show.s01e01.fr.srt", -987},
		{"large", "/media/movies/big.de.srt", 9_223_372_036_854_775_807},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := db.SetSyncOffset(ctx, tc.path, tc.off); err != nil {
				t.Fatalf("SetSyncOffset(%q, %d): %v", tc.path, tc.off, err)
			}
			got, err := db.SyncOffset(ctx, tc.path)
			if err != nil {
				t.Fatalf("SyncOffset(%q): %v", tc.path, err)
			}
			if got != tc.off {
				t.Errorf("SyncOffset(%q) = %d, want %d", tc.path, got, tc.off)
			}
		})
	}
}

// TestSyncOffset_getAbsentReturnsZero asserts a get for a path with no stored
// offset returns 0 and no error, matching the old SQLite not-found-means-zero
// behaviour (Requirement 6.1).
func TestSyncOffset_getAbsentReturnsZero(t *testing.T) {
	db, _ := openTemp(t)

	got, err := db.SyncOffset(t.Context(), "/never/written.srt")
	if err != nil {
		t.Fatalf("SyncOffset(absent): %v", err)
	}
	if got != 0 {
		t.Errorf("SyncOffset(absent) = %d, want 0", got)
	}
}

// TestSyncOffset_overwriteUpdates asserts a second set for the same path
// replaces the prior offset rather than accumulating or appending.
func TestSyncOffset_overwriteUpdates(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()
	const path = "/media/movies/over.fr.srt"

	if err := db.SetSyncOffset(ctx, path, 500); err != nil {
		t.Fatalf("first SetSyncOffset: %v", err)
	}
	if err := db.SetSyncOffset(ctx, path, -250); err != nil {
		t.Fatalf("second SetSyncOffset: %v", err)
	}
	got, err := db.SyncOffset(ctx, path)
	if err != nil {
		t.Fatalf("SyncOffset: %v", err)
	}
	if got != -250 {
		t.Errorf("SyncOffset after overwrite = %d, want -250", got)
	}
}

// TestPollTimestamp_setThenGetRoundTrip asserts a set poll cursor is returned by
// a later get with its instant preserved, for each canonical key (Requirement
// 6.2). The stored value uses RFC3339Nano, so sub-second precision survives; the
// comparison normalises to UTC and to the stored layout's resolution.
func TestPollTimestamp_setThenGetRoundTrip(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()

	want := time.Date(2024, 3, 9, 12, 34, 56, 123456789, time.UTC)
	for _, key := range []subflux.PollKey{subflux.PollKeySonarr, subflux.PollKeyRadarr} {
		if err := db.SetPollTimestamp(ctx, key, want); err != nil {
			t.Fatalf("SetPollTimestamp(%q): %v", key, err)
		}
		got, err := db.PollTimestamp(ctx, key)
		if err != nil {
			t.Fatalf("PollTimestamp(%q): %v", key, err)
		}
		if !got.Equal(want) {
			t.Errorf("PollTimestamp(%q) = %v, want %v", key, got, want)
		}
	}
}

// TestPollTimestamp_keysAreIndependent asserts the two arr sources keep separate
// cursors: setting sonarr does not disturb radarr.
func TestPollTimestamp_keysAreIndependent(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()

	sonarrAt := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	radarrAt := time.Date(2024, 6, 15, 9, 30, 0, 0, time.UTC)
	if err := db.SetPollTimestamp(ctx, subflux.PollKeySonarr, sonarrAt); err != nil {
		t.Fatalf("SetPollTimestamp(sonarr): %v", err)
	}
	if err := db.SetPollTimestamp(ctx, subflux.PollKeyRadarr, radarrAt); err != nil {
		t.Fatalf("SetPollTimestamp(radarr): %v", err)
	}

	gotS, err := db.PollTimestamp(ctx, subflux.PollKeySonarr)
	if err != nil {
		t.Fatalf("PollTimestamp(sonarr): %v", err)
	}
	gotR, err := db.PollTimestamp(ctx, subflux.PollKeyRadarr)
	if err != nil {
		t.Fatalf("PollTimestamp(radarr): %v", err)
	}
	if !gotS.Equal(sonarrAt) {
		t.Errorf("sonarr cursor = %v, want %v", gotS, sonarrAt)
	}
	if !gotR.Equal(radarrAt) {
		t.Errorf("radarr cursor = %v, want %v", gotR, radarrAt)
	}
}

// TestPollTimestamp_getAbsentReturnsZeroTime asserts a get for a key with no
// stored cursor returns the zero time and no error (Requirement 6.3).
func TestPollTimestamp_getAbsentReturnsZeroTime(t *testing.T) {
	db, _ := openTemp(t)

	got, err := db.PollTimestamp(t.Context(), subflux.PollKeySonarr)
	if err != nil {
		t.Fatalf("PollTimestamp(absent): %v", err)
	}
	if !got.IsZero() {
		t.Errorf("PollTimestamp(absent) = %v, want zero time", got)
	}
}

// TestPollTimestamp_invalidKeyRejected asserts a non-canonical key is rejected
// on both get and set, mirroring the old store's key.Valid guard (prevents a
// typo from silently creating a new cursor row).
func TestPollTimestamp_invalidKeyRejected(t *testing.T) {
	db, _ := openTemp(t)
	ctx := t.Context()

	if err := db.SetPollTimestamp(ctx, subflux.PollKey("Sonarr"), time.Now()); err == nil {
		t.Error("SetPollTimestamp(invalid key): error = nil, want rejection")
	}
	if _, err := db.PollTimestamp(ctx, subflux.PollKey("sonar")); err == nil {
		t.Error("PollTimestamp(invalid key): error = nil, want rejection")
	}
}
