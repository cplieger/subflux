package boltstore

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

func openCycleDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "cycle.bolt"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close(context.Background()) })
	return db
}

func TestScanCycleMark_roundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openCycleDB(t)

	got, err := db.ScanCycleStart(ctx)
	if err != nil {
		t.Fatalf("ScanCycleStart (empty): %v", err)
	}
	if !got.IsZero() {
		t.Errorf("ScanCycleStart on fresh store = %v, want zero time", got)
	}

	want := time.Date(2026, 7, 17, 8, 30, 15, 123456789, time.UTC)
	if err := db.SetScanCycleStart(ctx, want); err != nil {
		t.Fatalf("SetScanCycleStart: %v", err)
	}
	got, err = db.ScanCycleStart(ctx)
	if err != nil {
		t.Fatalf("ScanCycleStart: %v", err)
	}
	if !got.Equal(want) {
		t.Errorf("ScanCycleStart = %v, want %v (RFC3339Nano round-trip)", got, want)
	}

	if err := db.ClearScanCycleStart(ctx); err != nil {
		t.Fatalf("ClearScanCycleStart: %v", err)
	}
	got, err = db.ScanCycleStart(ctx)
	if err != nil {
		t.Fatalf("ScanCycleStart (cleared): %v", err)
	}
	if !got.IsZero() {
		t.Errorf("ScanCycleStart after clear = %v, want zero time", got)
	}

	// Clearing an absent mark is a no-op, not an error.
	if err := db.ClearScanCycleStart(ctx); err != nil {
		t.Errorf("ClearScanCycleStart (idempotent): %v", err)
	}
}

func TestRecordScanState_searched_flag_roundtrip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db := openCycleDB(t)

	put := func(id string, searched bool) {
		t.Helper()
		if err := db.RecordScanState(ctx, &api.ScanRecord{
			MediaType: api.MediaTypeEpisode,
			MediaID:   id,
			Title:     "T",
			Searched:  searched,
		}); err != nil {
			t.Fatalf("RecordScanState(%s): %v", id, err)
		}
	}
	put("tvdb-1-s01e01", true)
	put("tvdb-1-s01e02", false)

	rows, err := db.GetScanStates(ctx, api.MediaTypeEpisode, "")
	if err != nil {
		t.Fatalf("GetScanStates: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("GetScanStates rows = %d, want 2", len(rows))
	}
	bySearched := map[string]bool{}
	for _, r := range rows {
		bySearched[r.MediaID] = r.Searched
	}
	if !bySearched["tvdb-1-s01e01"] {
		t.Errorf("searched stamp round-tripped as false")
	}
	if bySearched["tvdb-1-s01e02"] {
		t.Errorf("inventory-only stamp round-tripped as true")
	}

	// A later inventory-only visit overwrites a searched stamp: each stamp
	// describes its own visit.
	put("tvdb-1-s01e01", false)
	rows, err = db.GetScanStates(ctx, api.MediaTypeEpisode, "tvdb-1-s01e01")
	if err != nil {
		t.Fatalf("GetScanStates (prefix): %v", err)
	}
	if len(rows) != 1 || rows[0].Searched {
		t.Errorf("re-stamp rows = %+v, want single Searched=false row", rows)
	}
}
