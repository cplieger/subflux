package statedb

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/cplieger/bolttable"
)

// putInstances stores instances 1 to n with no items.
func putInstances(t *testing.T, s *Store, n int) {
	t.Helper()
	update(t, s, func(tx *bolttable.Tx) error {
		for i := 1; i <= n; i++ {
			if err := putInstance(s, InstanceID(i))(tx); err != nil {
				return err
			}
		}
		return nil
	})
}

func enqueueWork(t *testing.T, s *Store, cause WorkCause, ids ...InstanceID) {
	t.Helper()
	if err := s.EnqueueWork(t.Context(), ids, cause); err != nil {
		t.Fatalf("EnqueueWork: %v", err)
	}
}

func nextWork(t *testing.T, s *Store, limit int) []Work {
	t.Helper()
	ws, err := s.NextWork(t.Context(), limit)
	if err != nil {
		t.Fatalf("NextWork: %v", err)
	}
	return ws
}

func storedWork(t *testing.T, s *Store, id InstanceID) (workRow, bool) {
	t.Helper()
	var (
		row   workRow
		found bool
	)
	if err := s.db.View(t.Context(), func(r *bolttable.ReadTx) (err error) {
		row, found, err = s.t.work.Get(r, id)
		return err
	}); err != nil {
		t.Fatalf("work row: %v", err)
	}
	return row, found
}

func insts(ws []Work) []InstanceID {
	out := make([]InstanceID, len(ws))
	for i, w := range ws {
		out[i] = w.Inst
	}
	return out
}

func drainWorkReady(s *Store) bool {
	select {
	case <-s.WorkReady():
		return true
	default:
		return false
	}
}

func reopen(t *testing.T, s *Store, path string) *Store {
	t.Helper()
	if err := s.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return mustOpen(t, path, productionConfig())
}

func TestNextWorkItemFirst(t *testing.T) {
	s := newStore(t)
	putInstances(t, s, 1002)
	scans := make([]InstanceID, 1000)
	for i := range scans {
		scans[i] = InstanceID(i + 1)
	}
	enqueueWork(t, s, Scan, scans...)
	enqueueWork(t, s, Item, 1001)
	got := nextWork(t, s, 10)
	if want := append([]InstanceID{1001}, scans[:9]...); !slices.Equal(insts(got), want) || got[0].Cause != Item {
		t.Errorf("NextWork(10) = %v, want the Item row then the 9 oldest Scan rows", got)
	}
	enqueueWork(t, s, Import, 1002)
	if got, want := insts(nextWork(t, s, 3)), []InstanceID{1001, 1002, 1}; !slices.Equal(got, want) {
		t.Errorf("after a later Import row: %v, want %v", got, want)
	}
}

func TestEnqueueKeepsPositionAndHigherCause(t *testing.T) {
	s := newStore(t)
	putInstances(t, s, 3)
	enqueueWork(t, s, Import, 1, 2, 3)
	enqueueWork(t, s, Scan, 1)
	got := nextWork(t, s, 10)
	if !slices.Equal(insts(got), []InstanceID{1, 2, 3}) || got[0].Cause != Import || got[0].Gen != 2 || got[1].Gen != 1 {
		t.Errorf("after re-enqueuing 1 as Scan: %+v, want 1 kept first as Import at gen 2", got)
	}
	enqueueWork(t, s, Item, 3)
	got = nextWork(t, s, 10)
	if !slices.Equal(insts(got), []InstanceID{3, 1, 2}) || got[0].Cause != Item {
		t.Errorf("after raising 3 to Item: %+v, want 3 first as Item", got)
	}
}

func TestEnqueueSkipsUnknownInstance(t *testing.T) {
	s := newStore(t)
	enqueueWork(t, s, Scan, 9)
	if _, found := storedWork(t, s, 9); found || drainWorkReady(s) {
		t.Errorf("an unknown instance was queued (row %v) or signalled", found)
	}
	putInstances(t, s, 1)
	enqueueWork(t, s, Scan, 9, 1)
	if got := insts(nextWork(t, s, 10)); !slices.Equal(got, []InstanceID{1}) || !drainWorkReady(s) {
		t.Errorf("queued %v, want [1] with WorkReady signalled", got)
	}
}

func TestEnqueueRefusesUnknownCause(t *testing.T) {
	s := newStore(t)
	putInstances(t, s, 1)
	for _, c := range []WorkCause{0, Item + 1} {
		if err := s.EnqueueWork(t.Context(), []InstanceID{1}, c); err == nil {
			t.Errorf("cause %d accepted", c)
		}
	}
	if _, found := storedWork(t, s, 1); found {
		t.Error("a refused enqueue wrote a row")
	}
}

func TestNextWorkLimitBounds(t *testing.T) {
	s := newStore(t)
	putInstances(t, s, 1001)
	ids := make([]InstanceID, 1001)
	for i := range ids {
		ids[i] = InstanceID(i + 1)
	}
	enqueueWork(t, s, Scan, ids...)
	for _, limit := range []int{-1, 0, 1001} {
		if ws, err := s.NextWork(t.Context(), limit); err == nil || ws != nil {
			t.Errorf("NextWork(%d) = %d rows, %v; want an error", limit, len(ws), err)
		}
	}
	for _, limit := range []int{1, 1000} {
		if got := len(nextWork(t, s, limit)); got != limit {
			t.Errorf("NextWork(%d) answered %d rows", limit, got)
		}
	}
}

func TestReenqueueDuringRunSurvivesAck(t *testing.T) {
	s := newStore(t)
	putInstances(t, s, 1)
	enqueueWork(t, s, Scan, 1)
	run := nextWork(t, s, 1)
	enqueueWork(t, s, Scan, 1)
	if err := s.AckWork(t.Context(), run); err != nil {
		t.Fatal(err)
	}
	got := nextWork(t, s, 1)
	if len(got) != 1 || got[0].Gen != run[0].Gen+1 {
		t.Fatalf("after acknowledging the older generation: %+v, want the row at gen %d", got, run[0].Gen+1)
	}
	if err := s.AckWork(t.Context(), got); err != nil {
		t.Fatal(err)
	}
	if left := nextWork(t, s, 1); len(left) != 0 {
		t.Errorf("acknowledging the current generation left %+v", left)
	}
}

func TestWorkSurvivesCrashBeforeAck(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	s := mustOpen(t, path, productionConfig())
	putInstances(t, s, 2)
	enqueueWork(t, s, Import, 2, 1)
	before := nextWork(t, s, 10)
	s = reopen(t, s, path)
	if after := nextWork(t, s, 10); !slices.Equal(after, before) || len(after) != 2 {
		t.Errorf("after a reopen with no AckWork: %+v, want %+v", after, before)
	}
}

func TestWorkReadyAtOpenForStoredQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	s := mustOpen(t, path, productionConfig())
	if drainWorkReady(s) {
		t.Error("an empty store signalled WorkReady at Open")
	}
	putInstances(t, s, 1)
	enqueueWork(t, s, Scan, 1)
	if !drainWorkReady(s) {
		t.Fatal("EnqueueWork did not signal WorkReady")
	}
	s = reopen(t, s, path)
	if !drainWorkReady(s) {
		t.Error("a stored queue did not signal WorkReady at Open")
	}
	if drainWorkReady(s) {
		t.Error("Open signalled more than one wake")
	}
}

func TestDeferWorkGenChecked(t *testing.T) {
	s := newStore(t)
	putInstances(t, s, 1)
	enqueueWork(t, s, Scan, 1)
	w := nextWork(t, s, 1)[0]
	start := time.Now()
	first, deferred, err := s.DeferWork(t.Context(), w, start.Add(time.Hour))
	if err != nil || !deferred || first.Before(start.Add(-time.Second)) || first.After(time.Now()) {
		t.Fatalf("first DeferWork = %v, %v, %v; want deferred with the first deferral now", first, deferred, err)
	}
	again, deferred, err := s.DeferWork(t.Context(), w, start.Add(2*time.Hour))
	row, _ := storedWork(t, s, 1)
	if err != nil || !deferred || !again.Equal(first) || !row.FirstDeferral.Equal(first) || !row.NotBefore.Equal(start.Add(2*time.Hour).UTC().Round(0)) {
		t.Errorf("second DeferWork = %v, %v, %v, row %+v; want the first deferral kept and not-before moved", again, deferred, err, row)
	}
	for name, other := range map[string]Work{"other_gen": {Inst: 1, Gen: w.Gen + 1}, "no_row": {Inst: 9, Gen: 1}} {
		t.Run(name, func(t *testing.T) {
			got, deferred, err := s.DeferWork(t.Context(), other, start.Add(3*time.Hour))
			if after, _ := storedWork(t, s, 1); err != nil || deferred || !got.IsZero() || after != row {
				t.Errorf("DeferWork = %v, %v, %v, row %+v; want false and nothing changed", got, deferred, err, after)
			}
		})
	}
}

func TestNextWorkDueOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	s := mustOpen(t, path, productionConfig())
	putInstances(t, s, 2)
	enqueueWork(t, s, Scan, 1, 2)
	ws := nextWork(t, s, 2)
	first, _, err := s.DeferWork(t.Context(), ws[0], time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.DeferWork(t.Context(), ws[1], time.Now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if got := insts(nextWork(t, s, 2)); !slices.Equal(got, []InstanceID{2}) {
		t.Errorf("NextWork = %v, want only the row whose not-before passed", got)
	}
	s = reopen(t, s, path)
	if got := insts(nextWork(t, s, 2)); !slices.Equal(got, []InstanceID{2}) {
		t.Errorf("after a reopen: %v, want the deferred row still held back", got)
	}
	if again, _, err := s.DeferWork(t.Context(), ws[0], time.Now().Add(time.Hour)); err != nil || !again.Equal(first) {
		t.Errorf("first deferral after a reopen: %v (%v), want %v", again, err, first)
	}
}

func TestEnqueueMakesDeferredDue(t *testing.T) {
	s := newStore(t)
	putInstances(t, s, 1)
	enqueueWork(t, s, Scan, 1)
	w := nextWork(t, s, 1)[0]
	first, _, err := s.DeferWork(t.Context(), w, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	enqueueWork(t, s, Scan, 1)
	got := nextWork(t, s, 1)
	row, _ := storedWork(t, s, 1)
	if len(got) != 1 || got[0].Gen != w.Gen+1 || !row.FirstDeferral.Equal(first) || !row.NotBefore.IsZero() {
		t.Errorf("after EnqueueWork on a deferred row: %+v, row %+v; want it due at gen %d with its first deferral", got, row, w.Gen+1)
	}
}
