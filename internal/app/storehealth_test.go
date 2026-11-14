package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

// fakeHealth stands in for statedb's write outcomes, whose write fault is
// internal to statedb (its TestWriteHealth covers the counting).
type fakeHealth struct {
	w  statedb.WriteHealth
	mu sync.Mutex
}

func (f *fakeHealth) WriteHealth() statedb.WriteHealth {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.w
}

func (f *fakeHealth) set(w statedb.WriteHealth) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.w = w
}

func TestStoreAlertClears(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		st, err := status.New(t.Context(), statustest.NewMemPersister())
		if err != nil {
			t.Fatal(err)
		}
		m := obs.New()
		h := &fakeHealth{}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { _ = storeHealth(ctx, h, st) }()
		go func() { _ = alertMetrics(ctx, st, m) }()
		storeSeries := `subflux_alerts_active{id="",kind="store"} 1`
		tick := func(w statedb.WriteHealth) {
			h.set(w)
			time.Sleep(storeHealthTick)
			synctest.Wait()
		}
		start := time.Now()

		tick(statedb.WriteHealth{LastOK: start})
		if hasAlert(st, status.AlertStore) {
			t.Error("store raised while every write committed")
		}
		h.set(statedb.WriteHealth{Failures: 3, Last: errors.New("no space left on device"), LastFailed: start.Add(time.Second), LastOK: start})
		time.Sleep(storeHealthTick / 2)
		synctest.Wait()
		if hasAlert(st, status.AlertStore) {
			t.Error("store raised before the tick read the failure")
		}
		time.Sleep(storeHealthTick / 2)
		synctest.Wait()
		if !hasAlert(st, status.AlertStore) || !slices.Contains(alertLines(m), storeSeries) {
			t.Errorf("a failure newer than the last commit: store %v, alerts_active %q; want %s", hasAlert(st, status.AlertStore), alertLines(m), storeSeries)
		}
		const wantText = "the state store could not write (3 failed writes since start): no space left on device"
		if snap, _ := st.Snapshot(); !slices.ContainsFunc(snap.Alerts, func(a status.Alert) bool { return a.Source == status.AlertStore && a.Text == wantText }) {
			t.Errorf("store alert %+v; want its text %q", snap.Alerts, wantText)
		}
		tick(statedb.WriteHealth{Failures: 1, Last: errors.New("no space left on device"), LastFailed: start.Add(time.Second), LastOK: start.Add(2 * time.Second)})
		if hasAlert(st, status.AlertStore) || len(alertLines(m)) != 0 {
			t.Errorf("a commit after the failure: store %v, alerts_active %q; want cleared and the series deleted", hasAlert(st, status.AlertStore), alertLines(m))
		}
		cancel()
	})
}
