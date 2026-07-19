package scanning

import (
	"context"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
)

// These tests pin the P5 duration-aware resume cutoff: a dangling cycle mark
// keeps the interrupted cycle's start as the resume origin, and the recency
// cutoff extends back to it when the pass outran scan_interval.

type fakeScanStore struct {
	mark      time.Time
	markSets  []time.Time
	cleared   int
	recent    map[string]bool
	gotCutoff time.Time
}

func (f *fakeScanStore) RecentlyScanned(_ context.Context, cutoff time.Time) (map[string]bool, error) {
	f.gotCutoff = cutoff
	return f.recent, nil
}

func (f *fakeScanStore) RecordScanState(context.Context, *api.ScanRecord) error { return nil }

func (f *fakeScanStore) ScanCycleStart(context.Context) (time.Time, error) { return f.mark, nil }

func (f *fakeScanStore) SetScanCycleStart(_ context.Context, t time.Time) error {
	f.mark = t
	f.markSets = append(f.markSets, t)
	return nil
}

func (f *fakeScanStore) ClearScanCycleStart(context.Context) error {
	f.cleared++
	f.mark = time.Time{}
	return nil
}

func TestResumeCycleStart_fresh_cycle_persists_now(t *testing.T) {
	t.Parallel()
	db := &fakeScanStore{}
	before := time.Now()
	start := resumeCycleStart(context.Background(), db)
	after := time.Now()

	if start.Before(before) || start.After(after) {
		t.Errorf("fresh cycle start = %v, want within [%v, %v]", start, before, after)
	}
	if len(db.markSets) != 1 || !db.markSets[0].Equal(start) {
		t.Errorf("persisted marks = %v, want exactly the returned start", db.markSets)
	}
}

func TestResumeCycleStart_dangling_mark_keeps_origin(t *testing.T) {
	t.Parallel()
	origin := time.Now().Add(-30 * time.Hour)
	db := &fakeScanStore{mark: origin}

	start := resumeCycleStart(context.Background(), db)
	if !start.Equal(origin) {
		t.Errorf("interrupted cycle start = %v, want the dangling origin %v", start, origin)
	}
	// A second interruption must keep the SAME origin (re-persisted as-is).
	if len(db.markSets) != 1 || !db.markSets[0].Equal(origin) {
		t.Errorf("persisted marks = %v, want the origin re-persisted", db.markSets)
	}
}

func TestLoadRecentScans_cutoff_extends_to_cycle_start(t *testing.T) {
	t.Parallel()
	db := &fakeScanStore{recent: map[string]bool{"x": true}}
	interval := time.Hour

	// Cycle started 3h ago, interval 1h: the cutoff must extend back to the
	// cycle start so the pass's early segment stays in the resume set.
	cycleStart := time.Now().Add(-3 * time.Hour)
	loadRecentScans(context.Background(), db, interval, cycleStart)
	if !db.gotCutoff.Equal(cycleStart) {
		t.Errorf("cutoff = %v, want cycle start %v (duration-aware extension)", db.gotCutoff, cycleStart)
	}

	// Cycle started 10 minutes ago: the plain interval cutoff applies.
	fresh := time.Now().Add(-10 * time.Minute)
	beforeCall := time.Now()
	loadRecentScans(context.Background(), db, interval, fresh)
	wantMin := beforeCall.Add(-interval)
	if db.gotCutoff.Before(wantMin.Add(-time.Second)) || db.gotCutoff.After(time.Now().Add(-interval).Add(time.Second)) {
		t.Errorf("cutoff = %v, want ~now-interval (%v)", db.gotCutoff, wantMin)
	}
}
