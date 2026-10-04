package providergate

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/boltstore"
	"github.com/cplieger/subflux/internal/subflux"
)

func TestOpen_refuses_an_invalid_config(t *testing.T) {
	var noMetrics *fakeMetrics
	var noStore *memStore
	tests := []struct {
		name string
		cfg  Config
	}{
		{name: "no metrics", cfg: Config{}},
		{name: "nil metrics pointer", cfg: Config{Metrics: noMetrics}},
		{name: "store without hasher", cfg: Config{Store: newMemStore(), Metrics: newMetrics()}},
		{name: "nil store pointer", cfg: Config{Store: noStore, Hasher: testHasher(t), Metrics: newMetrics()}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g, err := Open(t.Context(), tc.cfg)
			if err == nil || g != nil {
				t.Errorf("Open(%s) = (%v, %v), want (nil, error)", tc.name, g, err)
			}
		})
	}
}

func TestOpen_starts_empty_when_the_store_cannot_be_read(t *testing.T) {
	r := &rig{clock: newClock(), metrics: newMetrics(), events: &eventLog{}, store: newMemStore()}
	r.store.recs[hdbits] = subflux.ProviderAuthRecord{Provider: hdbits, Failures: 3, DisabledAt: r.clock.Now()}
	r.store.readErr = errors.New("bolt: read failed")
	r.gate = r.open(t, r.store)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	mustAdmit(t, b, hdbits, OpSearch)
}

func TestAdmit_walks_the_ladder_to_a_disable(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))

	mustAdmit(t, b, hdbits, OpSearch)
	b.Observe(t.Context(), hdbits, OpSearch, errAuth)
	mustRefuse(t, b, hdbits, OpSearch, "the credentials were rejected, so the next attempt is in 5m")
	r.clock.Advance(4*time.Minute + 30*time.Second)
	mustRefuse(t, b, hdbits, OpDownload, "the credentials were rejected, so the next attempt is in 30s")
	r.clock.Advance(30 * time.Second)

	mustAdmit(t, b, hdbits, OpSearch)
	mustRefuse(t, b, hdbits, OpSearch, "credential check in progress")
	b.Observe(t.Context(), hdbits, OpSearch, errAuth)
	mustRefuse(t, b, hdbits, OpSearch, "the credentials were rejected, so the next attempt is in 30m")
	r.clock.Advance(30 * time.Minute)

	mustAdmit(t, b, hdbits, OpDownload)
	b.Observe(t.Context(), hdbits, OpDownload, errAuth)
	mustRefuse(t, b, hdbits, OpSearch, "the credentials were rejected, so the provider is disabled until the settings change or a Test passes")
	r.clock.Advance(48 * time.Hour)
	mustRefuse(t, b, hdbits, OpSearch, "the credentials were rejected, so the provider is disabled until the settings change or a Test passes")

	if v, ok := r.metrics.disabledSeries(hdbits); !ok || !v {
		t.Errorf("provider_disabled{hdbits} = (%v, exists %v), want 1", v, ok)
	}
	if got := r.metrics.failures(hdbits); got != 3 {
		t.Errorf("provider_auth_failures_total{hdbits} = %d, want 3", got)
	}
	if got := r.events.kinds(hdbits); !slices.Equal(got, []Kind{Disabled}) {
		t.Errorf("hdbits events = %v, want [disabled]", got)
	}
	stored, ok := r.store.stored(hdbits)
	if !ok || stored.Failures != 3 || stored.DisabledAt.IsZero() || !slices.Equal(stored.FailedOps, []string{"search", "download"}) {
		t.Errorf("stored record = %+v (present %v), want failures 3, disabled, failed ops [search download]", stored, ok)
	}
	if stored.Fingerprint == "" || strings.Contains(stored.Fingerprint, secretA) {
		t.Errorf("stored fingerprint = %q, want a non-empty hash without the passkey", stored.Fingerprint)
	}
	mustAdmit(t, b, subdl, OpSearch)
}

func TestObserve_counts_a_concurrent_burst_once(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	for range 4 {
		mustAdmit(t, b, hdbits, OpSearch)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { b.Observe(t.Context(), hdbits, OpSearch, errAuth) })
	}
	wg.Wait()
	if got := r.metrics.failures(hdbits); got != 1 {
		t.Errorf("auth failures after a burst of 4 = %d, want 1", got)
	}
	if got := b.Status()[hdbits].AuthFailures; got != 1 {
		t.Errorf("Status().AuthFailures = %d, want 1", got)
	}
}

func TestObserve_success_resets_only_an_operation_that_failed(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	mustAdmit(t, b, hdbits, OpSearch)
	b.Observe(t.Context(), hdbits, OpSearch, errAuth)
	r.clock.Advance(5 * time.Minute)

	mustAdmit(t, b, hdbits, OpDownload)
	b.Observe(t.Context(), hdbits, OpDownload, nil)
	if got := b.Status()[hdbits].AuthFailures; got != 1 {
		t.Fatalf("after a download success, AuthFailures = %d, want 1 (download never failed)", got)
	}

	mustAdmit(t, b, hdbits, OpSearch)
	b.Observe(t.Context(), hdbits, OpSearch, nil)
	if st, ok := b.Status()[hdbits]; ok {
		t.Errorf("after a search success, Status()[hdbits] = %+v, want no entry", st)
	}
	if e, _ := r.events.last(hdbits); e.Kind != Enabled || e.Cause != CauseCredentialsAccepted {
		t.Errorf("last hdbits event = %+v, want enabled/credentials_accepted", e)
	}
	if _, ok := r.store.stored(hdbits); ok {
		t.Error("store still holds the hdbits record after it was cleared")
	}
	mustAdmit(t, b, hdbits, OpSearch)
	mustAdmit(t, b, hdbits, OpSearch)
}

func TestObserve_rate_limit_pauses_only_its_operation(t *testing.T) {
	tests := []struct {
		name       string
		retryAfter time.Duration
		wantUntil  string
	}{
		{name: "retry-after honoured", retryAfter: 40 * time.Minute, wantUntil: "14:30"},
		{name: "default without a hint", wantUntil: "14:00"},
		{name: "capped at a day", retryAfter: 72 * time.Hour, wantUntil: "13:50"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t)
			b := r.liveBinding(t, hdbitsSettings(secretA))
			mustAdmit(t, b, hdbits, OpDownload)
			b.Observe(t.Context(), hdbits, OpDownload, &subflux.RateLimitError{Msg: "download limit exceeded (406)", RetryAfter: tc.retryAfter})

			mustRefuse(t, b, hdbits, OpDownload, "rate limited until "+tc.wantUntil)
			mustAdmit(t, b, hdbits, OpSearch)
			if got := r.events.kinds(hdbits); !slices.Equal(got, []Kind{Paused}) {
				t.Errorf("events = %v, want [paused]", got)
			}
			if st := b.Status()[hdbits]; st.PausedFor <= 0 || st.Disabled || st.AuthFailures != 0 {
				t.Errorf("Status()[hdbits] = %+v, want a running pause and no auth state", st)
			}
		})
	}
}

func TestObserve_rate_limit_ends_at_its_deadline_and_announces_once(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	rl := &subflux.RateLimitError{Msg: "429", RetryAfter: time.Hour}
	b.Observe(t.Context(), hdbits, OpSearch, rl)
	b.Observe(t.Context(), hdbits, OpSearch, rl)
	if got := r.events.kinds(hdbits); !slices.Equal(got, []Kind{Paused}) {
		t.Errorf("events after two 429s = %v, want one [paused]", got)
	}
	if got := r.metrics.rateLimited[pauseKey{id: hdbits, op: OpSearch}]; got != 2 {
		t.Errorf("provider_rate_limited_total{hdbits,search} = %d, want 2", got)
	}
	r.clock.Advance(time.Hour)
	mustAdmit(t, b, hdbits, OpSearch)
}

func TestAdmit_frees_a_probe_whose_caller_never_reported(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	b.Observe(t.Context(), hdbits, OpSearch, errAuth)
	r.clock.Advance(5 * time.Minute)
	mustAdmit(t, b, hdbits, OpSearch)
	r.clock.Advance(time.Minute)
	mustRefuse(t, b, hdbits, OpSearch, "credential check in progress")
	r.clock.Advance(time.Minute)
	mustAdmit(t, b, hdbits, OpSearch)
}

func TestObserve_other_errors_free_the_probe_and_count_nothing(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	b.Observe(t.Context(), hdbits, OpSearch, errAuth)
	r.clock.Advance(5 * time.Minute)
	for _, err := range []error{errTransient, fmt.Errorf("search: %w", errTransient)} {
		mustAdmit(t, b, hdbits, OpSearch)
		b.Observe(t.Context(), hdbits, OpSearch, err)
	}
	if got := b.Status()[hdbits].AuthFailures; got != 1 {
		t.Errorf("AuthFailures after transient errors = %d, want 1", got)
	}
	if _, ok := r.metrics.disabledSeries(hdbits); !ok {
		t.Error("provider_disabled{hdbits} series missing")
	}
}

func TestObserve_recognises_a_wrapped_auth_error(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	b.Observe(t.Context(), hdbits, OpSearch, fmt.Errorf("find torrents: %w", errAuth))
	mustRefuse(t, b, hdbits, OpSearch, "the credentials were rejected, so the next attempt is in 5m")
}

func TestBinding_stale_settings_are_refused_and_ignored(t *testing.T) {
	r := newRig(t)
	stale := r.gate.Bind(hdbitsSettings(secretA))
	live := r.liveBinding(t, hdbitsSettings(secretB))
	before := r.store.writeCount()

	mustRefuse(t, stale, hdbits, OpSearch, "the settings changed and the new settings are still loading")
	stale.Observe(t.Context(), hdbits, OpSearch, errAuth)
	stale.ObserveSetting(hdbits, refusing("passkey", errAuth))

	if st, ok := live.Status()[hdbits]; ok {
		t.Errorf("Status()[hdbits] after a stale observation = %+v, want no entry", st)
	}
	if got := r.store.writeCount(); got != before {
		t.Errorf("store writes after a stale observation = %d, want %d", got, before)
	}
	mustAdmit(t, live, hdbits, OpSearch)
	mustAdmit(t, stale, subdl, OpSearch)
}

func TestBinding_formatting_never_prints_a_setting(t *testing.T) {
	r := newRig(t)
	b := r.gate.Bind(hdbitsSettings(secretA))
	out := fmt.Sprintf("%v %+v %#v %s", b, b, b, b)
	if strings.Contains(out, secretA) || strings.Contains(out, "placeholder-user") {
		t.Errorf("formatted binding %q contains a setting value", out)
	}
	if !strings.Contains(out, "2 providers") {
		t.Errorf("formatted binding %q, want the provider count", out)
	}
}

func TestReconcile_survives_restart_and_settings_changes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "subflux.bolt")
	db, err := boltstore.Open(path)
	if err != nil {
		t.Fatalf("boltstore.Open: %v", err)
	}
	r := &rig{clock: newClock(), metrics: newMetrics(), events: &eventLog{}}
	r.gate = r.open(t, db)
	settings := map[subflux.ProviderID]map[string]any{
		hdbits: {"username": "placeholder-user", "passkey": secretA, "max_torrents": 5, "strict": true},
	}
	b := r.liveBinding(t, settings)
	for range MaxAuthFailures {
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		r.clock.Advance(time.Hour)
	}
	if err := db.Close(t.Context()); err != nil {
		t.Fatalf("Close: %v", err)
	}

	db2, err := boltstore.Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close(t.Context()) })
	restarted := &rig{clock: r.clock, metrics: newMetrics(), events: &eventLog{}}
	restarted.gate = restarted.open(t, db2)
	asStrings := map[subflux.ProviderID]map[string]any{
		hdbits: {"username": "placeholder-user", "passkey": secretA, "max_torrents": "5", "strict": "true", "optional": ""},
	}
	nb := restarted.gate.Bind(asStrings)
	restarted.gate.Activate(nb)
	mustRefuse(t, nb, hdbits, OpSearch, "the settings changed and the new settings are still loading")
	restarted.gate.Reconcile(t.Context())

	mustRefuse(t, nb, hdbits, OpSearch, "the credentials were rejected, so the provider is disabled until the settings change or a Test passes")
	if got := restarted.events.kinds(hdbits); !slices.Equal(got, []Kind{Disabled}) {
		t.Errorf("events after restart = %v, want [disabled]", got)
	}
	if v, ok := restarted.metrics.disabledSeries(hdbits); !ok || !v {
		t.Errorf("provider_disabled{hdbits} after restart = (%v, %v), want 1", v, ok)
	}

	restarted.gate.Activate(restarted.gate.Bind(map[subflux.ProviderID]map[string]any{subdl: {"api_key": "placeholder-api-key"}}))
	restarted.gate.Reconcile(t.Context())
	if _, ok := restarted.metrics.disabledSeries(hdbits); ok {
		t.Error("provider_disabled{hdbits} series survived the provider being switched off")
	}
	if e, _ := restarted.events.last(hdbits); e.Kind != Inactive {
		t.Errorf("last event after switch-off = %+v, want inactive", e)
	}
	recs, err := db2.ProviderAuthRecords(t.Context())
	if err != nil || len(recs) != 1 {
		t.Fatalf("stored records after switch-off = %v (%v), want the hdbits record kept", recs, err)
	}

	back := restarted.gate.Bind(asStrings)
	restarted.gate.Activate(back)
	restarted.gate.Reconcile(t.Context())
	mustRefuse(t, back, hdbits, OpSearch, "the credentials were rejected, so the provider is disabled until the settings change or a Test passes")

	changed := restarted.gate.Bind(hdbitsSettings(secretB))
	restarted.gate.Activate(changed)
	restarted.gate.Reconcile(t.Context())
	mustAdmit(t, changed, hdbits, OpSearch)
	if e, _ := restarted.events.last(hdbits); e.Kind != Enabled || e.Cause != CauseSettingsChanged {
		t.Errorf("last event after a settings change = %+v, want enabled/settings_changed", e)
	}
	if recs, _ := db2.ProviderAuthRecords(t.Context()); len(recs) != 0 {
		t.Errorf("stored records after a settings change = %v, want none", recs)
	}
}

func TestReconcile_refuses_until_an_unverified_record_is_checked(t *testing.T) {
	r := newRig(t)
	a := r.liveBinding(t, hdbitsSettings(secretA))
	a.Observe(t.Context(), hdbits, OpSearch, errAuth)

	b := r.gate.Bind(hdbitsSettings(secretB))
	r.gate.Activate(b)
	mustRefuse(t, b, hdbits, OpSearch, "the settings changed and the new settings are still loading")
	r.gate.Reconcile(t.Context())
	mustAdmit(t, b, hdbits, OpSearch)
	if _, ok := r.store.stored(hdbits); ok {
		t.Error("store still holds the record of the replaced settings")
	}
}

func TestReconcile_matching_record_is_admitted_only_per_the_ladder(t *testing.T) {
	r := newRig(t)
	a := r.liveBinding(t, hdbitsSettings(secretA))
	a.Observe(t.Context(), hdbits, OpSearch, errAuth)
	r2 := &rig{clock: r.clock, metrics: newMetrics(), events: &eventLog{}, store: r.store}
	r2.gate = r2.open(t, r.store)
	b := r2.liveBinding(t, hdbitsSettings(secretA))
	mustRefuse(t, b, hdbits, OpSearch, "the credentials were rejected, so the next attempt is in 5m")
	r.clock.Advance(5 * time.Minute)
	mustAdmit(t, b, hdbits, OpSearch)
}

func TestReconcile_deletes_a_record_whose_fingerprint_cannot_be_read(t *testing.T) {
	r := newRig(t)
	r.store.recs[hdbits] = subflux.ProviderAuthRecord{Provider: hdbits, Fingerprint: "not-a-phc-string", Failures: 3, DisabledAt: r.clock.Now()}
	r.gate = r.open(t, r.store)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	mustAdmit(t, b, hdbits, OpSearch)
	if _, ok := r.store.stored(hdbits); ok {
		t.Error("store still holds the unreadable record")
	}
}

// blockingStore blocks every Put until release is closed, so a test can
// order a second mutation while the first one's write is in flight.
type blockingStore struct {
	*memStore
	entered chan struct{}
	release chan struct{}
}

func (s *blockingStore) PutProviderAuthRecord(ctx context.Context, rec *subflux.ProviderAuthRecord) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return s.memStore.PutProviderAuthRecord(ctx, rec)
}

// A mutation that writes nothing returns while an earlier disable's record
// write is blocked; the disable's event must still wait for the write.
func TestObserve_an_event_waits_for_its_record_behind_a_later_drain(t *testing.T) {
	r := newRig(t)
	store := &blockingStore{memStore: newMemStore(), entered: make(chan struct{}, 1), release: make(chan struct{})}
	close(store.release)
	r.gate = r.open(t, store)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	for range MaxAuthFailures - 1 {
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		r.clock.Advance(time.Hour)
	}
	<-store.entered
	store.release = make(chan struct{})

	var wg sync.WaitGroup
	wg.Go(func() { b.Observe(t.Context(), hdbits, OpSearch, errAuth) })
	<-store.entered
	b.Observe(t.Context(), subdl, OpSearch, &subflux.RateLimitError{Msg: "slow down"})
	if got := r.events.kinds(hdbits); slices.Contains(got, Disabled) {
		t.Errorf("hdbits events before its record was written = %v, want no disabled", got)
	}
	close(store.release)
	wg.Wait()

	if got := r.events.kinds(hdbits); !slices.Equal(got, []Kind{Disabled}) {
		t.Errorf("hdbits events = %v, want [disabled]", got)
	}
	if got := r.events.kinds(subdl); !slices.Equal(got, []Kind{Paused}) {
		t.Errorf("subdl events = %v, want [paused]", got)
	}
	if rec, _ := store.stored(hdbits); rec.DisabledAt.IsZero() {
		t.Errorf("stored record = %+v, want disabled", rec)
	}
}

// ctxStore refuses a write whose context has ended, as a store honouring
// cancellation would.
type ctxStore struct{ *memStore }

func (s ctxStore) PutProviderAuthRecord(ctx context.Context, rec *subflux.ProviderAuthRecord) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.memStore.PutProviderAuthRecord(ctx, rec)
}

// The record write is detached from the deciding call's cancellation, so a
// call whose context has already ended still persists the state it decided.
func TestObserve_persists_a_decided_state_after_its_call_ended(t *testing.T) {
	r := newRig(t)
	store := ctxStore{newMemStore()}
	r.gate = r.open(t, store)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	b.Observe(ctx, hdbits, OpSearch, errAuth)
	if rec, ok := store.stored(hdbits); !ok || rec.Failures != 1 {
		t.Errorf("stored record = %+v (present %v), want one failure", rec, ok)
	}
}

func TestObserve_a_slow_write_cannot_land_over_a_later_state(t *testing.T) {
	t.Run("concurrent failure during the write is ignored", func(t *testing.T) {
		r := newRig(t)
		store := &blockingStore{memStore: newMemStore(), entered: make(chan struct{}, 1), release: make(chan struct{})}
		r.gate = r.open(t, store)
		b := r.liveBinding(t, hdbitsSettings(secretA))
		var wg sync.WaitGroup
		wg.Go(func() { b.Observe(t.Context(), hdbits, OpSearch, errAuth) })
		<-store.entered
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		mustRefuse(t, b, hdbits, OpSearch, "the credentials were rejected, so the next attempt is in 5m")
		close(store.release)
		wg.Wait()
		if rec, _ := store.stored(hdbits); rec.Failures != 1 {
			t.Errorf("stored failures = %d, want 1", rec.Failures)
		}
	})
	t.Run("a second failure during the first write ends at two", func(t *testing.T) {
		r := newRig(t)
		store := &blockingStore{memStore: newMemStore(), entered: make(chan struct{}, 1), release: make(chan struct{})}
		r.gate = r.open(t, store)
		b := r.liveBinding(t, hdbitsSettings(secretA))
		var wg sync.WaitGroup
		wg.Go(func() { b.Observe(t.Context(), hdbits, OpSearch, errAuth) })
		<-store.entered
		r.clock.Advance(5 * time.Minute)
		mustAdmit(t, b, hdbits, OpSearch)
		wg.Go(func() { b.Observe(t.Context(), hdbits, OpSearch, errAuth) })
		close(store.release)
		wg.Wait()
		if rec, _ := store.stored(hdbits); rec.Failures != 2 {
			t.Errorf("stored failures = %d, want 2", rec.Failures)
		}
	})
	t.Run("observations after the disable write nothing", func(t *testing.T) {
		r := newRig(t)
		b := r.liveBinding(t, hdbitsSettings(secretA))
		for range MaxAuthFailures {
			b.Observe(t.Context(), hdbits, OpSearch, errAuth)
			r.clock.Advance(time.Hour)
		}
		before, _ := r.store.stored(hdbits)
		writes := r.store.writeCount()
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		b.Observe(t.Context(), hdbits, OpSearch, nil)
		after, _ := r.store.stored(hdbits)
		if r.store.writeCount() != writes || after.Failures != before.Failures || after.DisabledAt != before.DisabledAt {
			t.Errorf("record after post-disable observations = %+v (writes %d), want %+v unchanged (writes %d)",
				after, r.store.writeCount(), before, writes)
		}
	})
}

// An earlier, unrelated event still being delivered must not hold back a
// disable's record: the deciding call returns with it written.
func TestObserve_a_disable_is_durable_while_an_earlier_event_is_delivered(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	for range MaxAuthFailures - 1 {
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		r.clock.Advance(time.Hour)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r.gate.SetOnChange(func(e Event) {
		if e.Provider == subdl && e.Kind == Paused {
			once.Do(func() {
				close(entered)
				<-release
			})
		}
		r.events.record(e)
	})
	var wg sync.WaitGroup
	wg.Go(func() { b.Observe(t.Context(), subdl, OpSearch, &subflux.RateLimitError{Msg: "slow down"}) })
	<-entered

	b.Observe(t.Context(), hdbits, OpSearch, errAuth)
	if rec, ok := r.store.stored(hdbits); !ok || rec.DisabledAt.IsZero() {
		t.Errorf("stored hdbits record once its third rejection returned, with subdl's earlier event still being delivered = %+v (exists %v), want DisabledAt set",
			rec, ok)
	}
	close(release)
	wg.Wait()
	if got := r.events.kinds(hdbits); !slices.Equal(got, []Kind{Disabled}) {
		t.Errorf("hdbits events = %v, want [disabled]", got)
	}
}

// panicStore panics on its first Put once release is closed, and stores
// every later one.
type panicStore struct {
	*memStore
	entered, release chan struct{}
	once             sync.Once
}

func (s *panicStore) PutProviderAuthRecord(ctx context.Context, rec *subflux.ProviderAuthRecord) error {
	first := false
	s.once.Do(func() { first = true })
	if first {
		close(s.entered)
		<-s.release
		panic("placeholder store failure")
	}
	return s.memStore.PutProviderAuthRecord(ctx, rec)
}

// A caller waiting behind a write that panics still has its own record
// written, by a new flush, and returns.
func TestObserve_a_panicking_write_leaves_later_commits_to_a_new_flush(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		store := &panicStore{memStore: newMemStore(), entered: make(chan struct{}), release: make(chan struct{})}
		r.gate = r.open(t, store)
		b := r.liveBinding(t, hdbitsSettings(secretA))
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		}()
		<-store.entered
		done := make(chan struct{})
		go func() {
			b.Observe(t.Context(), subdl, OpSearch, errAuth)
			close(done)
		}()
		synctest.Wait()
		close(store.release)
		if p := <-panicked; p == nil {
			t.Fatal("Setup: the first record write did not panic")
		}
		<-done
		if rec, ok := store.stored(subdl); !ok || rec.Failures != 1 {
			t.Errorf("stored subdl record after the write ahead of it panicked = %+v (exists %v), want one failure", rec, ok)
		}
	})
}

func TestClearIfMatches_answers_per_recorded_settings(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	for range MaxAuthFailures {
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		r.clock.Advance(time.Hour)
	}

	if got := r.gate.ClearIfMatches(t.Context(), hdbits, hdbitsSettings(secretB)[hdbits]); got != Mismatch {
		t.Fatalf("ClearIfMatches(other settings) = %v, want Mismatch", got)
	}
	mustRefuse(t, b, hdbits, OpSearch, "the credentials were rejected, so the provider is disabled until the settings change or a Test passes")
	if got := r.gate.ClearIfMatches(t.Context(), subdl, hdbitsSettings(secretA)[subdl]); got != NoRecord {
		t.Errorf("ClearIfMatches(no record) = %v, want NoRecord", got)
	}
	if got := r.gate.ClearIfMatches(t.Context(), hdbits, map[string]any{"passkey": secretA, "username": "placeholder-user"}); got != Cleared {
		t.Fatalf("ClearIfMatches(recorded settings) = %v, want Cleared", got)
	}
	mustAdmit(t, b, hdbits, OpSearch)
	if v, _ := r.metrics.disabledSeries(hdbits); v {
		t.Error("provider_disabled{hdbits} still 1 after a passing test")
	}
	if e, _ := r.events.last(hdbits); e.Kind != Enabled || e.Cause != CauseCredentialTestPassed {
		t.Errorf("last event = %+v, want enabled/credential_test_passed", e)
	}
}

// A passing test that clears the provider while the disable's event is still
// being delivered must be heard after it, so the hook ends on enabled.
func TestClearIfMatches_during_a_slow_disable_event_ends_enabled(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	r.gate.SetOnChange(func(e Event) {
		if e.Provider == hdbits && e.Kind == Disabled {
			once.Do(func() {
				close(entered)
				<-release
			})
		}
		r.events.record(e)
	})
	for range MaxAuthFailures - 1 {
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		r.clock.Advance(time.Hour)
	}
	var wg sync.WaitGroup
	wg.Go(func() { b.Observe(t.Context(), hdbits, OpSearch, errAuth) })
	<-entered

	if got := r.gate.ClearIfMatches(t.Context(), hdbits, hdbitsSettings(secretA)[hdbits]); got != Cleared {
		t.Fatalf("ClearIfMatches(recorded settings) = %v, want Cleared", got)
	}
	close(release)
	wg.Wait()

	if got := r.events.kinds(hdbits); !slices.Equal(got, []Kind{Disabled, Enabled}) {
		t.Errorf("hdbits events = %v, want [disabled enabled]", got)
	}
	if v, _ := r.metrics.disabledSeries(hdbits); v {
		t.Error("provider_disabled{hdbits} = 1 after the passing test cleared it")
	}
	mustAdmit(t, b, hdbits, OpSearch)
}

func TestClearIfMatches_works_with_no_live_binding(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, hdbitsSettings(secretA))
	b.Observe(t.Context(), hdbits, OpSearch, errAuth)
	unconfigured := &rig{clock: r.clock, metrics: newMetrics(), events: &eventLog{}}
	unconfigured.gate = unconfigured.open(t, r.store)
	if got := unconfigured.gate.ClearIfMatches(t.Context(), hdbits, hdbitsSettings(secretA)[hdbits]); got != Cleared {
		t.Errorf("ClearIfMatches with no binding = %v, want Cleared", got)
	}
	if _, ok := r.store.stored(hdbits); ok {
		t.Error("store still holds the record a passing test cleared")
	}
}

func TestResetAll_clears_every_state(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, map[subflux.ProviderID]map[string]any{
		hdbits: {"passkey": secretA}, subdl: {"api_key": "placeholder-api-key"}, animeto: {"anidb_client_key": "placeholder-client"},
	})
	for range MaxAuthFailures {
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		r.clock.Advance(time.Hour)
	}
	b.Observe(t.Context(), subdl, OpDownload, &subflux.RateLimitError{Msg: "429"})
	held := refusing("anidb_client_key", errors.New("client version missing or invalid"))
	b.ObserveSetting(animeto, held)

	b.ResetAll(t.Context())

	if st := b.Status(); len(st) != 0 {
		t.Errorf("Status() after ResetAll = %+v, want empty", st)
	}
	mustAdmit(t, b, hdbits, OpSearch)
	mustAdmit(t, b, subdl, OpDownload)
	for _, id := range []subflux.ProviderID{hdbits, subdl} {
		if e, _ := r.events.last(id); e.Kind != Enabled || e.Cause != CauseReset {
			t.Errorf("last %s event = %+v, want enabled/reset", id, e)
		}
	}
	if e, _ := r.events.last(animeto); e.Kind != SettingCleared {
		t.Errorf("last animetosho event = %+v, want setting_cleared", e)
	}
	if v, ok := r.metrics.disabledSeries(hdbits); !ok || v {
		t.Errorf("provider_disabled{hdbits} after reset = (%v, %v), want 0", v, ok)
	}
	if len(r.metrics.rejected) != 0 {
		t.Errorf("provider_setting_rejected after reset = %v, want no series", r.metrics.rejected)
	}
	b.PrepareSetting(animeto, held)
	if held.forgets != 1 {
		t.Errorf("animetosho's instance forgot its answer %d times before its next call after reset, want 1", held.forgets)
	}
}

const animetoKey = "anidb_client_key"

var errKeyRefused = errors.New("client version missing or invalid")

func TestObserveSetting_marks_once_and_clears_on_change_or_test(t *testing.T) {
	r := newRig(t)
	settings := map[subflux.ProviderID]map[string]any{animeto: {animetoKey: "placeholder-client"}}
	b := r.liveBinding(t, settings)
	held := refusing(animetoKey, errKeyRefused)
	b.ObserveSetting(animeto, held)
	b.ObserveSetting(animeto, held)

	if got := r.events.kinds(animeto); !slices.Equal(got, []Kind{SettingRejected}) {
		t.Errorf("events = %v, want one [setting_rejected]", got)
	}
	if st := b.Status()[animeto]; !slices.Equal(st.RejectedSettings, []string{animetoKey}) || st.Disabled {
		t.Errorf("Status()[animetosho] = %+v, want the rejected key and no disable", st)
	}
	mustAdmit(t, b, animeto, OpSearch)

	if got := r.gate.ClearIfMatches(t.Context(), animeto, settings[animeto]); got != Cleared {
		t.Errorf("ClearIfMatches(live settings) = %v, want Cleared", got)
	}
	if len(r.metrics.rejected) != 0 {
		t.Errorf("provider_setting_rejected after a passing test = %v, want no series", r.metrics.rejected)
	}
	b.PrepareSetting(animeto, held)
	if held.forgets != 1 {
		t.Errorf("instance forgot its answer %d times before its next call after a passing test, want 1", held.forgets)
	}

	b.ObserveSetting(animeto, refusing(animetoKey, errKeyRefused))
	r.liveBinding(t, map[subflux.ProviderID]map[string]any{animeto: {animetoKey: "placeholder-other"}})
	if e, _ := r.events.last(animeto); e.Kind != SettingCleared {
		t.Errorf("last event after a key change = %+v, want setting_cleared", e)
	}
}

func TestObserveSetting_an_acceptance_clears_the_rejection_for_every_instance(t *testing.T) {
	r := newRig(t)
	settings := map[subflux.ProviderID]map[string]any{animeto: {animetoKey: "placeholder-client"}}
	outgoing := r.liveBinding(t, settings)
	refused := refusing(animetoKey, errKeyRefused)
	outgoing.ObserveSetting(animeto, refused)
	live := r.liveBinding(t, settings)
	if st := live.Status()[animeto]; !slices.Equal(st.RejectedSettings, []string{animetoKey}) {
		t.Fatalf("Setup: Status()[animetosho] after an unchanged activation = %+v, want the rejection kept", st)
	}

	accepted := accepting(animetoKey)
	live.ObserveSetting(animeto, accepted)

	if st, ok := live.Status()[animeto]; ok {
		t.Errorf("Status()[animetosho] after an acceptance = %+v, want no entry", st)
	}
	if e, _ := r.events.last(animeto); e.Kind != SettingCleared || e.Cause != CauseCredentialsAccepted {
		t.Errorf("last event after an acceptance = %+v, want setting_cleared/credentials_accepted", e)
	}
	if len(r.metrics.rejected) != 0 {
		t.Errorf("provider_setting_rejected after an acceptance = %v, want no series", r.metrics.rejected)
	}
	outgoing.PrepareSetting(animeto, refused)
	live.PrepareSetting(animeto, accepted)
	if refused.forgets != 1 || accepted.forgets != 0 {
		t.Errorf("forgets before the next call: outgoing %d, accepting %d; want 1 and 0", refused.forgets, accepted.forgets)
	}
}

func TestClearIfMatches_reaches_every_instance_that_reported_the_rejection(t *testing.T) {
	r := newRig(t)
	settings := map[subflux.ProviderID]map[string]any{animeto: {animetoKey: "placeholder-client"}}
	outgoing := r.liveBinding(t, settings)
	live := r.liveBinding(t, settings)
	liveHeld, outgoingHeld := refusing(animetoKey, errKeyRefused), refusing(animetoKey, errKeyRefused)
	live.ObserveSetting(animeto, liveHeld)
	outgoing.PrepareSetting(animeto, outgoingHeld)
	outgoingHeld.setting, outgoingHeld.refusal = animetoKey, errKeyRefused
	outgoing.ObserveSetting(animeto, outgoingHeld)
	liveBefore, outgoingBefore := liveHeld.forgets, outgoingHeld.forgets

	if got := r.gate.ClearIfMatches(t.Context(), animeto, settings[animeto]); got != Cleared {
		t.Fatalf("ClearIfMatches(live settings) = %v, want Cleared", got)
	}
	live.PrepareSetting(animeto, liveHeld)
	outgoing.PrepareSetting(animeto, outgoingHeld)

	if liveHeld.forgets != liveBefore+1 || outgoingHeld.forgets != outgoingBefore+1 {
		t.Errorf("forgets after the test: live %d (was %d), outgoing %d (was %d); want one more each",
			liveHeld.forgets, liveBefore, outgoingHeld.forgets, outgoingBefore)
	}
}

func TestObserveSetting_an_answer_from_before_a_rejection_cannot_clear_it(t *testing.T) {
	r := newRig(t)
	settings := map[subflux.ProviderID]map[string]any{animeto: {animetoKey: "placeholder-client"}}
	outgoing := r.liveBinding(t, settings)
	live := r.liveBinding(t, settings)
	earlier := accepting(animetoKey)
	outgoing.PrepareSetting(animeto, earlier)
	live.ObserveSetting(animeto, refusing(animetoKey, errKeyRefused))

	outgoing.ObserveSetting(animeto, earlier)

	if st := live.Status()[animeto]; !slices.Equal(st.RejectedSettings, []string{animetoKey}) {
		t.Errorf("Status()[animetosho] after an acceptance from before the refusal = %+v, want the rejection kept", st)
	}
	if got := r.events.kinds(animeto); !slices.Equal(got, []Kind{SettingRejected}) {
		t.Errorf("events = %v, want [setting_rejected] only", got)
	}
	if earlier.forgets != 1 {
		t.Errorf("the earlier instance forgot its answer %d times, want 1 so its next call asks again", earlier.forgets)
	}
}

func TestObserveSetting_an_instance_with_no_answer_changes_nothing(t *testing.T) {
	r := newRig(t)
	b := r.liveBinding(t, map[subflux.ProviderID]map[string]any{animeto: {animetoKey: "placeholder-client"}})
	b.ObserveSetting(animeto, refusing("", nil))
	if got := r.events.kinds(animeto); len(got) != 0 {
		t.Errorf("events = %v, want none", got)
	}
	if st, ok := b.Status()[animeto]; ok {
		t.Errorf("Status()[animetosho] = %+v, want no entry", st)
	}
}

// blockingMetrics holds the first SetProviderDisabled(_, true) until release
// is closed.
type blockingMetrics struct {
	*fakeMetrics
	entered, release chan struct{}
	once             sync.Once
}

func (m *blockingMetrics) SetProviderDisabled(id subflux.ProviderID, disabled bool) {
	if disabled {
		m.once.Do(func() {
			close(m.entered)
			<-m.release
		})
	}
	m.fakeMetrics.SetProviderDisabled(id, disabled)
}

func TestObserve_a_paused_metric_call_blocks_neither_status_nor_persistence(t *testing.T) {
	r := newRig(t)
	block := &blockingMetrics{fakeMetrics: r.metrics, entered: make(chan struct{}), release: make(chan struct{})}
	g, err := Open(t.Context(), Config{Store: r.store, Hasher: testHasher(t), Metrics: block, Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	r.gate = g
	b := r.liveBinding(t, hdbitsSettings(secretA))
	for range MaxAuthFailures - 1 {
		b.Observe(t.Context(), hdbits, OpSearch, errAuth)
		r.clock.Advance(time.Hour)
	}
	var wg sync.WaitGroup
	wg.Go(func() { b.Observe(t.Context(), hdbits, OpSearch, errAuth) })
	<-block.entered

	statused := make(chan Status, 1)
	go func() { statused <- g.Status()[hdbits] }()
	select {
	case st := <-statused:
		if !st.Disabled {
			t.Errorf("Status()[hdbits] = %+v while the disable's gauge call was paused, want Disabled", st)
		}
	case <-time.After(5 * time.Second):
		t.Error("Status could not take the gate's lock while a metric call was paused")
	}
	if rec, ok := r.store.stored(hdbits); !ok || rec.DisabledAt.IsZero() {
		t.Errorf("stored record for hdbits while the disable's gauge call was paused = %+v (exists %v), want DisabledAt set", rec, ok)
	}
	close(block.release)
	wg.Wait()
	if v, ok := r.metrics.disabledSeries(hdbits); !v || !ok {
		t.Errorf("provider_disabled{hdbits} = %v (exists %v), want 1", v, ok)
	}
}
