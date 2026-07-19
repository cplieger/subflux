package activity

import (
	"sync"
	"testing"
)

// --- StopRegistry lifecycle ---

func TestStopRegistry_requestStop_invokes_callback_once(t *testing.T) {
	t.Parallel()
	var r StopRegistry
	calls := 0
	unregister := r.RegisterStop("1", func() { calls++ })
	defer unregister()

	if got := r.RequestStop("1"); got != StopRequested {
		t.Fatalf("first RequestStop = %v, want StopRequested", got)
	}
	if calls != 1 {
		t.Fatalf("callback calls = %d, want 1", calls)
	}

	// Double-cancel is idempotent: reported as already stopping, callback
	// NOT re-invoked.
	if got := r.RequestStop("1"); got != StopAlreadyStopping {
		t.Fatalf("second RequestStop = %v, want StopAlreadyStopping", got)
	}
	if calls != 1 {
		t.Errorf("callback calls after double-cancel = %d, want 1", calls)
	}
}

func TestStopRegistry_unknown_id_not_found(t *testing.T) {
	t.Parallel()
	var r StopRegistry
	if got := r.RequestStop("nope"); got != StopNotFound {
		t.Errorf("RequestStop(unknown) = %v, want StopNotFound", got)
	}
	if r.Cancellable("nope") {
		t.Error("Cancellable(unknown) = true, want false")
	}
}

func TestStopRegistry_unregister_releases(t *testing.T) {
	t.Parallel()
	var r StopRegistry
	unregister := r.RegisterStop("1", func() {})

	if !r.Cancellable("1") {
		t.Fatal("Cancellable = false while registered, want true")
	}
	unregister()
	if r.Cancellable("1") {
		t.Error("Cancellable = true after unregister, want false")
	}
	// Cancel-vs-end race: RequestStop on a released registration is a no-op
	// not_found (the composing endpoint maps it to 409 via the entry state).
	if got := r.RequestStop("1"); got != StopNotFound {
		t.Errorf("RequestStop(after unregister) = %v, want StopNotFound", got)
	}
	// Unregister is idempotent.
	unregister()
}

func TestStopRegistry_callback_invoked_after_unlock(t *testing.T) {
	t.Parallel()
	var r StopRegistry
	// The callback re-enters the registry: it deadlocks if RequestStop still
	// holds the mutex while invoking it.
	reentered := make(chan StopResult, 1)
	r.RegisterStop("other", func() {})
	r.RegisterStop("1", func() {
		reentered <- r.RequestStop("other")
	})

	if got := r.RequestStop("1"); got != StopRequested {
		t.Fatalf("RequestStop = %v, want StopRequested", got)
	}
	if got := <-reentered; got != StopRequested {
		t.Errorf("re-entrant RequestStop = %v, want StopRequested", got)
	}
}

func TestStopRegistry_concurrent_requests_single_invocation(t *testing.T) {
	t.Parallel()
	var r StopRegistry
	var mu sync.Mutex
	calls := 0
	r.RegisterStop("1", func() {
		mu.Lock()
		calls++
		mu.Unlock()
	})

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { r.RequestStop("1") })
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Errorf("callback calls under concurrent RequestStop = %d, want exactly 1", calls)
	}
}

// --- Log.StartScan: scope identity + idempotent same-scope start ---

func TestStartScan_same_scope_returns_existing(t *testing.T) {
	t.Parallel()
	log := New(10)
	scope := ScanScope{Kind: ScanKindSeries, MediaID: 42}

	id1, existing := log.StartScan("Series Search", "T", SourceManual, scope, "user")
	if existing {
		t.Fatal("first StartScan reported existing")
	}
	id2, existing := log.StartScan("Series Search", "T", SourceManual, scope, "user")
	if !existing {
		t.Fatal("second same-scope StartScan did not report existing")
	}
	if id1 != id2 {
		t.Errorf("second StartScan id = %q, want the running entry's id %q", id2, id1)
	}
	if n := len(log.Entries()); n != 1 {
		t.Errorf("entries = %d after idempotent start, want 1 (no duplicate work)", n)
	}
}

func TestStartScan_different_scope_creates_new(t *testing.T) {
	t.Parallel()
	log := New(10)
	cases := []ScanScope{
		{Kind: ScanKindSeries, MediaID: 42},
		{Kind: ScanKindSeries, MediaID: 43},
		{Kind: ScanKindSeason, MediaID: 42, Season: 1},
		{Kind: ScanKindSeason, MediaID: 42, Season: 2},
		{Kind: ScanKindItem, MediaType: "episode", MediaID: 42, Season: 1, Episode: 1},
		{Kind: ScanKindItem, MediaType: "episode", MediaID: 42, Season: 1, Episode: 2},
		{Kind: ScanKindMovie, MediaID: 42},
		{Kind: ScanKindFull},
	}
	seen := map[string]bool{}
	for _, sc := range cases {
		id, existing := log.StartScan("Scan", "d", SourceManual, sc, "user")
		if existing {
			t.Errorf("scope %+v matched an earlier different scope", sc)
		}
		if seen[id] {
			t.Errorf("scope %+v returned duplicate id %q", sc, id)
		}
		seen[id] = true
	}
}

func TestStartScan_terminal_and_cancelled_entries_do_not_match(t *testing.T) {
	t.Parallel()
	log := New(10)
	scope := ScanScope{Kind: ScanKindMovie, MediaID: 7}

	id1, _ := log.StartScan("Movie Search", "d", SourceManual, scope, "user")
	log.End(id1)
	id2, existing := log.StartScan("Movie Search", "d", SourceManual, scope, "user")
	if existing || id2 == id1 {
		t.Errorf("StartScan matched a DONE entry (id1=%q id2=%q existing=%v)", id1, id2, existing)
	}

	// Queued-dismiss-cancelled (Cancelled without Done yet): doomed, must
	// not satisfy a new start.
	log.SetQueued(id2, true)
	log.Cancel(id2)
	id3, existing := log.StartScan("Movie Search", "d", SourceManual, scope, "user")
	if existing || id3 == id2 {
		t.Errorf("StartScan matched a cancelled queued entry (id2=%q id3=%q existing=%v)", id2, id3, existing)
	}
}

func TestStartScan_stores_scope_and_role(t *testing.T) {
	t.Parallel()
	log := New(10)
	scope := ScanScope{Kind: ScanKindItem, MediaType: "episode", MediaID: 5, Season: 2, Episode: 3}

	id, _ := log.StartScan("Episode Search", "d", SourceManual, scope, "admin")
	e, ok := log.Get(id)
	if !ok {
		t.Fatal("entry not found")
	}
	if e.Kind != ScanKindItem || e.MediaType != "episode" || e.MediaID != 5 || e.Season != 2 || e.Episode != 3 {
		t.Errorf("stored scope = kind=%q type=%q id=%d s=%d e=%d, want the started scope",
			e.Kind, e.MediaType, e.MediaID, e.Season, e.Episode)
	}
	if e.RequiredRole != "admin" {
		t.Errorf("RequiredRole = %q, want admin", e.RequiredRole)
	}
}

// --- FinishCancelled: the terminal cancelled state ---

func TestFinishCancelled_reaches_terminal_state(t *testing.T) {
	t.Parallel()
	log := New(10)
	id, _ := log.StartScan("Series Search", "d", SourceManual,
		ScanScope{Kind: ScanKindSeries, MediaID: 1}, "user")

	log.FinishCancelled(id)

	e, ok := log.Get(id)
	if !ok {
		t.Fatal("entry not found")
	}
	if !e.Done {
		t.Error("Done = false, want true (cancelled must be TERMINAL, not running forever)")
	}
	if !e.Cancelled {
		t.Error("Cancelled = false, want true")
	}
	if e.EndedAt == nil {
		t.Error("EndedAt = nil, want set (prunable)")
	}
	if e.Failed {
		t.Error("Failed = true; cancelled is distinct from failed")
	}
}
