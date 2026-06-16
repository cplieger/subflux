package boltstore

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	bolt "go.etcd.io/bbolt"
)

// This file covers the task-5.2 scan_state domain: RecordScanState upsert with
// ix_scan_at maintenance (Requirement 5.3), GetScanStates listing/prefix/order
// (Requirement 5.2), RecentlyScanned with an INCLUSIVE cutoff served from the
// scan index (Requirement 5.4), and LastScanTime formatting + empty-when-none
// (Requirement 5.6).

const scanMTEp = api.MediaTypeEpisode

// recordScan is a context-free RecordScanState for tests.
func recordScan(t *testing.T, db *DB, mt api.MediaType, mid, title, audio string, season, episode int) {
	t.Helper()
	if err := db.RecordScanState(context.Background(), &api.ScanRecord{
		MediaType: mt, MediaID: mid, Title: title, AudioLang: audio, Season: season, Episode: episode,
	}); err != nil {
		t.Fatalf("RecordScanState(%q): %v", mid, err)
	}
}

// setScannedAt white-box writes a scan_state row with an explicit scanned_at
// through the same chokepoint production uses, so the inclusive-cutoff boundary
// can be tested at an exact instant (RecordScanState stamps now()).
func setScannedAt(t *testing.T, db *DB, mt api.MediaType, mid, title, audio string, season, episode int, at time.Time) {
	t.Helper()
	if err := db.db.Update(func(tx *bolt.Tx) error {
		rec := scanRec{Title: title, AudioLang: audio, Season: season, Episode: episode, ScannedAt: at.UTC()}
		return putScanState(tx, mt, mid, &rec)
	}); err != nil {
		t.Fatalf("setScannedAt(%q): %v", mid, err)
	}
}

// countScanIndexRaw counts ix_scan_at entries by a raw bucket walk, the
// ground-truth the index must match (one entry per scan_state row).
func countScanIndexRaw(t *testing.T, db *DB) int {
	t.Helper()
	n := 0
	if err := db.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucketIxScanAt)).ForEach(func(_, _ []byte) error {
			n++
			return nil
		})
	}); err != nil {
		t.Fatalf("countScanIndexRaw: %v", err)
	}
	return n
}

// --- RecordScanState upsert (Requirement 5.3) ---

// TestRecordScanState_upsert_updates_same_row asserts re-recording the same
// (media_type, media_id) overwrites the row in place (one row, latest metadata)
// and keeps the ix_scan_at index at exactly one entry (the stale entry from the
// prior scanned_at is removed, not orphaned).
func TestRecordScanState_upsert_updates_same_row(t *testing.T) {
	db, _ := openTemp(t)

	recordScan(t, db, scanMTEp, "tvdb-1-s01e01", "Old Title", "eng", 1, 1)
	recordScan(t, db, scanMTEp, "tvdb-1-s01e01", "New Title", "fre", 1, 1)

	rows := getScans(t, db, scanMTEp, "")
	if len(rows) != 1 {
		t.Fatalf("after upsert got %d rows, want 1: %+v", len(rows), rows)
	}
	if rows[0].Title != "New Title" || rows[0].AudioLang != "fre" {
		t.Errorf("row = %+v, want Title=New Title AudioLang=fre", rows[0])
	}
	if n := countScanIndexRaw(t, db); n != 1 {
		t.Errorf("ix_scan_at entries = %d, want 1 (stale entry must be removed on upsert)", n)
	}
}

// getScans is a context-free GetScanStates for tests.
func getScans(t *testing.T, db *DB, mt api.MediaType, prefix string) []api.ScanStateRow {
	t.Helper()
	rows, err := db.GetScanStates(context.Background(), mt, prefix)
	if err != nil {
		t.Fatalf("GetScanStates(%q, %q): %v", mt, prefix, err)
	}
	return rows
}

// --- GetScanStates listing/prefix/order (Requirement 5.2) ---

func TestGetScanStates_empty(t *testing.T) {
	db, _ := openTemp(t)
	if rows := getScans(t, db, scanMTEp, ""); len(rows) != 0 {
		t.Errorf("empty store GetScanStates = %+v, want none", rows)
	}
}

func TestGetScanStates_filters_by_media_type_and_orders_by_media_id(t *testing.T) {
	db, _ := openTemp(t)

	recordScan(t, db, scanMTEp, "tvdb-2-s01e01", "B", "eng", 1, 1)
	recordScan(t, db, scanMTEp, "tvdb-1-s01e01", "A", "eng", 1, 1)
	recordScan(t, db, api.MediaTypeMovie, "tmdb-9", "Movie", "eng", 0, 0)

	rows := getScans(t, db, scanMTEp, "")
	if len(rows) != 2 {
		t.Fatalf("episode rows = %d, want 2 (movie excluded): %+v", len(rows), rows)
	}
	if rows[0].MediaID != "tvdb-1-s01e01" || rows[1].MediaID != "tvdb-2-s01e01" {
		t.Errorf("ordering = [%q, %q], want sorted by media_id", rows[0].MediaID, rows[1].MediaID)
	}
}

func TestGetScanStates_prefix_filter(t *testing.T) {
	db, _ := openTemp(t)

	recordScan(t, db, scanMTEp, "tvdb-111-s01e01", "S1", "eng", 1, 1)
	recordScan(t, db, scanMTEp, "tvdb-111-s01e02", "S2", "eng", 1, 2)
	recordScan(t, db, scanMTEp, "tvdb-222-s01e01", "Other", "eng", 1, 1)

	rows := getScans(t, db, scanMTEp, "tvdb-111-")
	if len(rows) != 2 {
		t.Fatalf("prefix rows = %d, want 2: %+v", len(rows), rows)
	}
	for _, r := range rows {
		if r.MediaID == "tvdb-222-s01e01" {
			t.Errorf("prefix scan leaked non-matching row %q", r.MediaID)
		}
	}
}

// TestGetScanStates_prefix_boundary asserts a media-id prefix does not match a
// media id that merely shares a leading substring of the next component
// (the 0x00 after the media type seals the type boundary; the byte prefix on
// media_id mirrors the SQL LIKE 'prefix%').
func TestGetScanStates_prefix_boundary(t *testing.T) {
	db, _ := openTemp(t)

	recordScan(t, db, scanMTEp, "tvdb-1", "one", "eng", 1, 1)
	recordScan(t, db, scanMTEp, "tvdb-12", "twelve", "eng", 1, 1)

	rows := getScans(t, db, scanMTEp, "tvdb-1")
	// LIKE 'tvdb-1%' matches BOTH "tvdb-1" and "tvdb-12" (prefix semantics),
	// so this asserts the byte-prefix scan reproduces the SQL prefix match.
	if len(rows) != 2 {
		t.Fatalf("prefix 'tvdb-1' rows = %d, want 2 (prefix match): %+v", len(rows), rows)
	}
}

func TestGetScanStates_formats_scanned_at(t *testing.T) {
	db, _ := openTemp(t)
	at := time.Date(2026, 6, 15, 12, 30, 45, 0, time.UTC)
	setScannedAt(t, db, api.MediaTypeMovie, "tmdb-1", "M", "eng", 0, 0, at)

	rows := getScans(t, db, api.MediaTypeMovie, "")
	if len(rows) != 1 || rows[0].ScannedAt != "2026-06-15 12:30:45" {
		t.Fatalf("ScannedAt = %q, want %q", scannedAtOf(rows), "2026-06-15 12:30:45")
	}
}

func scannedAtOf(rows []api.ScanStateRow) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[0].ScannedAt
}

// --- RecentlyScanned inclusive cutoff (Requirement 5.4) ---

func TestRecentlyScanned_empty_returns_empty_map(t *testing.T) {
	db, _ := openTemp(t)
	got, err := db.RecentlyScanned(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("RecentlyScanned: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("RecentlyScanned on empty store = %v, want empty", got)
	}
}

// TestRecentlyScanned_inclusive_cutoff_boundary is the core boundary assertion:
// a row scanned exactly at the cutoff is INCLUDED; a row scanned one instant
// before the cutoff is EXCLUDED; a row scanned after is included.
func TestRecentlyScanned_inclusive_cutoff_boundary(t *testing.T) {
	db, _ := openTemp(t)

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	setScannedAt(t, db, scanMTEp, "before", "B", "eng", 1, 1, cutoff.Add(-time.Nanosecond))
	setScannedAt(t, db, scanMTEp, "exact", "E", "eng", 1, 1, cutoff)
	setScannedAt(t, db, scanMTEp, "after", "A", "eng", 1, 1, cutoff.Add(time.Hour))

	got, err := db.RecentlyScanned(context.Background(), cutoff)
	if err != nil {
		t.Fatalf("RecentlyScanned: %v", err)
	}
	if got["before"] {
		t.Error("media scanned just before the cutoff must be EXCLUDED")
	}
	if !got["exact"] {
		t.Error("media scanned exactly at the cutoff must be INCLUDED (>= cutoff)")
	}
	if !got["after"] {
		t.Error("media scanned after the cutoff must be included")
	}
	if len(got) != 2 {
		t.Errorf("RecentlyScanned set size = %d, want 2 (exact + after): %v", len(got), got)
	}
}

// TestRecentlyScanned_future_cutoff_returns_empty asserts a cutoff later than
// every scan returns the empty set.
func TestRecentlyScanned_future_cutoff_returns_empty(t *testing.T) {
	db, _ := openTemp(t)
	setScannedAt(t, db, scanMTEp, "x", "X", "eng", 1, 1, time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))

	got, err := db.RecentlyScanned(context.Background(), time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatalf("RecentlyScanned: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("future cutoff = %v, want empty", got)
	}
}

// TestRecentlyScanned_includes_all_media_types asserts the recency set spans
// media types (keyed by media id alone), matching the SQL query.
func TestRecentlyScanned_includes_all_media_types(t *testing.T) {
	db, _ := openTemp(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	setScannedAt(t, db, scanMTEp, "tvdb-1-s01e01", "Ep", "eng", 1, 1, base.Add(time.Hour))
	setScannedAt(t, db, api.MediaTypeMovie, "tmdb-2", "Mov", "eng", 0, 0, base.Add(2*time.Hour))

	got, err := db.RecentlyScanned(context.Background(), base)
	if err != nil {
		t.Fatalf("RecentlyScanned: %v", err)
	}
	if !got["tvdb-1-s01e01"] || !got["tmdb-2"] {
		t.Errorf("RecentlyScanned = %v, want both episode and movie ids", got)
	}
}

// --- LastScanTime (Requirement 5.6) ---

func TestLastScanTime_empty_returns_empty(t *testing.T) {
	db, _ := openTemp(t)
	got, err := db.LastScanTime(context.Background())
	if err != nil {
		t.Fatalf("LastScanTime: %v", err)
	}
	if got != "" {
		t.Errorf("LastScanTime on empty store = %q, want empty", got)
	}
}

func TestLastScanTime_returns_most_recent_formatted(t *testing.T) {
	db, _ := openTemp(t)
	setScannedAt(t, db, scanMTEp, "old", "Old", "eng", 1, 1, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	setScannedAt(t, db, api.MediaTypeMovie, "new", "New", "fre", 0, 0, time.Date(2026, 6, 15, 12, 30, 0, 0, time.UTC))

	got, err := db.LastScanTime(context.Background())
	if err != nil {
		t.Fatalf("LastScanTime: %v", err)
	}
	if got != "2026-06-15 12:30:00" {
		t.Errorf("LastScanTime = %q, want %q", got, "2026-06-15 12:30:00")
	}
}

func TestLastScanTime_after_record_returns_timestamp(t *testing.T) {
	db, _ := openTemp(t)
	recordScan(t, db, api.MediaTypeMovie, "tmdb-1", "M", "eng", 0, 0)

	got, err := db.LastScanTime(context.Background())
	if err != nil {
		t.Fatalf("LastScanTime: %v", err)
	}
	if got == "" {
		t.Error("LastScanTime after RecordScanState = empty, want a timestamp")
	}
	if _, perr := time.Parse(scanTimeLayout, got); perr != nil {
		t.Errorf("LastScanTime %q not in layout %q: %v", got, scanTimeLayout, perr)
	}
}
