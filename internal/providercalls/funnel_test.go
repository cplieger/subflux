package providercalls

import (
	"context"
	"errors"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"
	"unicode/utf8"

	"github.com/cplieger/go/httpclient"
	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/statedb"
	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

const prov provider.ProviderID = "opensubtitles"

var errBusy = errors.New("hasher busy")

// fakeHasher fingerprints c as "fp:"+c; busy makes both calls fail.
type fakeHasher struct {
	busy error
	mu   sync.Mutex
}

func (h *fakeHasher) setBusy(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.busy = err
}

func (h *fakeHasher) Hash(_ context.Context, c string) (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.busy != nil {
		return "", h.busy
	}
	return "fp:" + c, nil
}

func (h *fakeHasher) Verify(_ context.Context, fp, c string) (bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.busy != nil {
		return false, h.busy
	}
	return fp == "fp:"+c, nil
}

type rig struct {
	f    *Funnel
	db   *statedb.Store
	st   *status.Store
	h    *fakeHasher
	path string
}

func newRig(t *testing.T) *rig {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.db")
	r := &rig{path: path, h: &fakeHasher{}}
	r.reopen(t)
	st, err := status.New(t.Context(), statustest.NewMemPersister())
	if err != nil {
		t.Fatal(err)
	}
	r.st = st
	r.f = NewFunnel(r.db, r.h, st)
	return r
}

// reopen opens the store at r.path, as a restart does.
func (r *rig) reopen(t *testing.T) {
	t.Helper()
	db, err := statedb.Open(r.path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r.db = db
}

func (r *rig) record(t *testing.T) (statedb.CredentialDisable, bool) {
	t.Helper()
	rec, found, err := r.db.CredentialDisable(t.Context(), string(prov))
	if err != nil {
		t.Fatal(err)
	}
	return rec, found
}

func (r *rig) providerState(t *testing.T) status.ProviderState {
	t.Helper()
	snap, _ := r.st.Snapshot()
	for _, p := range snap.Providers {
		if p.ID == string(prov) {
			return p
		}
	}
	return status.ProviderState{}
}

func (r *rig) alert(t *testing.T) (status.Alert, bool) {
	t.Helper()
	snap, _ := r.st.Snapshot()
	for _, a := range snap.Alerts {
		if a.Source == status.ProviderAlert(string(prov)) {
			return a, true
		}
	}
	return status.Alert{}, false
}

// inBubble runs fn as subtest name in its own synctest bubble.
func inBubble(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	t.Run(name, func(t *testing.T) { synctest.Test(t, fn) })
}

func returns(err error) func(context.Context) error {
	return func(context.Context) error { return err }
}

func authFail() error {
	return &provider.Failure{Kind: provider.Auth, Host: "api.opensubtitles.com", Status: 401, Msg: "Unauthorized"}
}

func rateFail(after time.Duration) error {
	return &provider.Failure{Kind: provider.RateLimited, Host: "api.opensubtitles.com", Status: 429, RetryAfter: after}
}

func transientFail(c httpclient.Cause, host string) error {
	return &provider.Failure{Kind: provider.Transient, Host: host, Cause: c, Msg: "unreachable"}
}

// ran reports whether a call reached its fn.
func ran(t *testing.T, b *Binding, op Op, c Class) (bool, error) {
	t.Helper()
	called := false
	err := b.Call(t.Context(), op, c, func(context.Context) error { called = true; return nil })
	return called, err
}

func backoffUntil(t *testing.T, err error) time.Time {
	t.Helper()
	var be *BackoffError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v, want a *BackoffError", err)
	}
	if be.Provider != prov {
		t.Errorf("BackoffError.Provider = %q, want %q", be.Provider, prov)
	}
	return be.Until
}

// disable refuses b's credential three times, waiting out each cooldown.
func disable(t *testing.T, b *Binding) {
	t.Helper()
	for _, wait := range []time.Duration{5 * time.Minute, 30 * time.Minute, 0} {
		if err := b.Call(t.Context(), Search, Automatic, returns(authFail())); err == nil {
			t.Fatal("an Auth answer returned nil")
		}
		time.Sleep(wait)
	}
}

func TestAuthLadder(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "key-a", 0)
		start := time.Now()
		_ = b.Call(t.Context(), Search, Automatic, returns(authFail()))
		if got := backoffUntil(t, b.Call(t.Context(), Fetch, Interactive, returns(nil))); !got.Equal(start.Add(5 * time.Minute)) {
			t.Errorf("after one refusal Until = %v, want now+5m", got.Sub(start))
		}
		time.Sleep(5 * time.Minute)
		start = time.Now()
		_ = b.Call(t.Context(), Search, Automatic, returns(authFail()))
		if got := backoffUntil(t, b.Call(t.Context(), Search, Interactive, returns(nil))); !got.Equal(start.Add(30 * time.Minute)) {
			t.Errorf("after two refusals Until = %v, want now+30m", got.Sub(start))
		}
		if _, found := r.record(t); found {
			t.Error("a record exists after two refusals")
		}
		time.Sleep(30 * time.Minute)
		_ = b.Call(t.Context(), Search, Automatic, returns(authFail()))
		rec, found := r.record(t)
		if !found || rec.Fingerprint != "fp:key-a" || !strings.Contains(rec.Reason, "401") {
			t.Errorf("record = %+v, %v; want key-a's fingerprint with the answer", rec, found)
		}
		if called, err := ran(t, b, Search, Interactive); called || !errors.Is(err, ErrCredentialDisabled) {
			t.Errorf("after the third refusal: ran %v, err %v; want ErrCredentialDisabled", called, err)
		}
		if s := r.providerState(t); s.Health != status.Disabled {
			t.Errorf("status = %+v, want Disabled", s)
		}
		if a, ok := r.alert(t); !ok || !strings.Contains(a.Text, "connection test") {
			t.Errorf("alert = %+v, %v; want the disabled credential's", a, ok)
		}
	})
}

func TestRecordReasonBounded(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "key-a", 0)
		long := &provider.Failure{Kind: provider.Auth, Host: "api.opensubtitles.com", Status: 401, Msg: strings.Repeat("é", 200)}
		for _, wait := range []time.Duration{5 * time.Minute, 30 * time.Minute, 0} {
			_ = b.Call(t.Context(), Search, Automatic, returns(long))
			time.Sleep(wait)
		}
		rec, found := r.record(t)
		if !found || len(rec.Reason) > maxReason || len(rec.Reason) < maxReason-1 || !utf8.ValidString(rec.Reason) {
			t.Errorf("record reason %d bytes, valid %v, found %v; want %d on a rune boundary", len(rec.Reason), utf8.ValidString(rec.Reason), found, maxReason)
		}
	})
}

func TestFirstCallReadsRecord(t *testing.T) {
	// restarted disables key-a, then answers a funnel over the same store,
	// as after a restart.
	restarted := func(t *testing.T) (*rig, *Funnel) {
		r := newRig(t)
		disable(t, r.f.Bind(prov, "key-a", 0))
		return r, NewFunnel(r.db, r.h, r.st)
	}
	inBubble(t, "matching", func(t *testing.T) {
		_, f := restarted(t)
		if called, err := ran(t, f.Bind(prov, "key-a", 0), Search, Interactive); called || !errors.Is(err, ErrCredentialDisabled) {
			t.Errorf("restart under the disabled key: ran %v, err %v", called, err)
		}
	})
	inBubble(t, "hasher_busy", func(t *testing.T) {
		r, f := restarted(t)
		b := f.Bind(prov, "key-a", 0)
		r.h.setBusy(errBusy)
		start := time.Now()
		called, err := ran(t, b, Search, Interactive)
		r.h.setBusy(nil)
		if called || !backoffUntil(t, err).Equal(start.Add(5*time.Second)) {
			t.Errorf("busy hasher: ran %v, err %v; want a 5 s refusal", called, err)
		}
		if called, err := ran(t, b, Search, Interactive); called || !errors.Is(err, ErrCredentialDisabled) {
			t.Errorf("after the busy hasher: ran %v, err %v", called, err)
		}
	})
	inBubble(t, "store_unreadable", func(t *testing.T) {
		r, f := restarted(t)
		b := f.Bind(prov, "key-a", 0)
		_ = r.db.Close()
		start := time.Now()
		if called, err := ran(t, b, Search, Interactive); called || !backoffUntil(t, err).Equal(start.Add(5*time.Second)) {
			t.Errorf("unreadable record: ran %v, err %v; want a 5 s refusal", called, err)
		}
	})
	inBubble(t, "old_key_leaves_record", func(t *testing.T) {
		r, f := restarted(t)
		old := f.Bind(prov, "key-old", 0)
		f.Bind(prov, "key-a", 0)
		if called, err := ran(t, old, Search, Interactive); !called || err != nil {
			t.Errorf("an old key's first call: ran %v, err %v; want it run", called, err)
		}
		if _, found := r.record(t); !found {
			t.Error("an old key's first call deleted the latest key's record")
		}
	})
	inBubble(t, "new_key_deletes_record", func(t *testing.T) {
		r, f := restarted(t)
		if called, err := ran(t, f.Bind(prov, "key-b", 0), Search, Interactive); !called || err != nil {
			t.Errorf("a new key's first call: ran %v, err %v; want it run", called, err)
		}
		if _, found := r.record(t); found {
			t.Error("the record outlived the new key's first call")
		}
	})
}

func TestSkippedChangesNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "key-a", 0)
		_ = b.Call(t.Context(), Search, Automatic, returns(authFail()))
		time.Sleep(5 * time.Minute)
		skip := &provider.Skipped{Cause: "no IMDb id"}
		if err := b.Call(t.Context(), Search, Automatic, returns(skip)); !errors.Is(err, skip) {
			t.Fatalf("Skipped came back as %v", err)
		}
		start := time.Now()
		_ = b.Call(t.Context(), Search, Automatic, returns(authFail()))
		if got := backoffUntil(t, b.Call(t.Context(), Search, Interactive, returns(nil))); !got.Equal(start.Add(30 * time.Minute)) {
			t.Errorf("the refusal after a Skipped waits %v, want 30m (ladder count 1 kept)", got.Sub(start))
		}
		time.Sleep(30 * time.Minute)
		for range 4 {
			_ = b.Call(t.Context(), Search, Interactive, returns(transientFail(httpclient.CauseReset, "api.opensubtitles.com")))
		}
		_ = b.Call(t.Context(), Search, Interactive, returns(skip))
		if called, err := ran(t, b, Search, Automatic); !called || err != nil {
			t.Errorf("after four transients and a Skipped: ran %v, err %v; want no cooldown", called, err)
		}
	})
}

func TestCredentialChangeNeverDisablesNew(t *testing.T) {
	inBubble(t, "old_binding_live", func(t *testing.T) {
		r := newRig(t)
		oldB := r.f.Bind(prov, "key-a", 0)
		for _, wait := range []time.Duration{5 * time.Minute, 30 * time.Minute} {
			_ = oldB.Call(t.Context(), Search, Automatic, returns(authFail()))
			time.Sleep(wait)
		}
		newB := r.f.Bind(prov, "key-b", 0)
		start := time.Now()
		_ = oldB.Call(t.Context(), Search, Automatic, returns(authFail()))
		if _, found := r.record(t); found {
			t.Error("a refusal under the old key wrote a record")
		}
		if got := backoffUntil(t, oldB.Call(t.Context(), Search, Interactive, returns(nil))); !got.Equal(start.Add(30 * time.Minute)) {
			t.Errorf("the old key waits %v, want 30m (count stays at 2)", got.Sub(start))
		}
		if called, err := ran(t, newB, Search, Automatic); !called || err != nil {
			t.Errorf("the new key: ran %v, err %v", called, err)
		}
	})
	inBubble(t, "old_binding_released", func(t *testing.T) {
		r := newRig(t)
		oldB := r.f.Bind(prov, "key-a", 0)
		newB := r.f.Bind(prov, "key-b", 0)
		_ = oldB.Call(t.Context(), Search, Automatic, func(context.Context) error { oldB.Release(); return authFail() })
		if called, err := ran(t, newB, Search, Automatic); !called || err != nil {
			t.Errorf("the new key after the released old one's refusal: ran %v, err %v", called, err)
		}
		if called, err := ran(t, r.f.Bind(prov, "key-a", 0), Search, Automatic); !called || err != nil {
			t.Errorf("the old key rebound: ran %v, err %v; a released call counted", called, err)
		}
	})
}

func TestCancelledCallsNeverTripHealth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		for range healthThreshold {
			ctx, cancel := context.WithCancel(t.Context())
			err := b.Call(ctx, Search, Interactive, func(context.Context) error {
				cancel()
				return transientFail(httpclient.CauseReset, "api.opensubtitles.com")
			})
			if !errors.Is(err, context.Canceled) {
				t.Errorf("a call whose caller left returned %v, want context.Canceled", err)
			}
		}
		if called, err := ran(t, b, Search, Automatic); !called || err != nil {
			t.Errorf("after five cancelled calls: ran %v, err %v; want no cooldown", called, err)
		}
	})
}

func TestOwnDeadlineIsTransient(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		for op, want := range map[Op]time.Duration{Search: 30 * time.Second, Fetch: 60 * time.Second} {
			start := time.Now()
			err := b.Call(t.Context(), op, Interactive, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
			var fail *provider.Failure
			if !errors.As(err, &fail) || fail.Kind != provider.Transient || fail.Cause != httpclient.CauseTimeout {
				t.Errorf("%v past its deadline returned %v, want a Transient timeout", op, err)
			}
			if got := time.Since(start); got != want {
				t.Errorf("%v ran %v, want %v", op, got, want)
			}
		}
		for range healthThreshold - 2 {
			_ = b.Call(t.Context(), Search, Interactive, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
		}
		if _, err := ran(t, b, Search, Automatic); err == nil {
			t.Error("five calls past their deadline set no cooldown")
		} else {
			backoffUntil(t, err)
		}
	})
}

func TestRecordWriteFailureLeavesMemory(t *testing.T) {
	// Each fault reports whether it closed the store, which the check of
	// the stored records then reopens.
	cases := map[string]func(r *rig) bool{
		"hasher_busy":  func(r *rig) bool { r.h.setBusy(errBusy); return false },
		"store_closed": func(r *rig) bool { _ = r.db.Close(); return true },
	}
	for name, fault := range cases {
		t.Run(name, func(t *testing.T) {
			var logs strings.Builder
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelError})))
			t.Cleanup(func() { slog.SetDefault(prev) })
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t)
				b := r.f.Bind(prov, "key-a", 0)
				for _, wait := range []time.Duration{5 * time.Minute, 30 * time.Minute} {
					_ = b.Call(t.Context(), Search, Automatic, returns(authFail()))
					time.Sleep(wait)
				}
				closed := fault(r)
				err := b.Call(t.Context(), Search, Automatic, returns(authFail()))
				var fail *provider.Failure
				if !errors.As(err, &fail) || fail.Kind != provider.Auth {
					t.Errorf("the call returned %v, want its own Auth failure", err)
				}
				if !strings.Contains(logs.String(), "level=ERROR") {
					t.Errorf("no ERROR logged; logs: %q", logs.String())
				}
				if s := r.providerState(t); s.Health == status.Disabled {
					t.Errorf("status %+v after a failed write", s)
				}
				r.h.setBusy(nil)
				if closed {
					r.reopen(t)
				}
				if _, found := r.record(t); found {
					t.Error("a record exists after its write failed")
				}
				if called, err := ran(t, b, Search, Automatic); !called || err != nil {
					t.Errorf("after the failed write: ran %v, err %v; want memory as it was", called, err)
				}
			})
		})
	}
}

func TestReleasedBindingRefusesAndIgnores(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "key-a", 0)
		b.Release()
		if called, err := ran(t, b, Search, Interactive); called || !errors.Is(err, ErrReleased) {
			t.Errorf("a released binding: ran %v, err %v", called, err)
		}
		inFlight := func(err error) {
			b := r.f.Bind(prov, "key-a", 0)
			_ = b.Call(t.Context(), Search, Interactive, func(context.Context) error { b.Release(); return err })
		}
		for range maxAuthFailures {
			inFlight(authFail())
		}
		for range healthThreshold {
			inFlight(transientFail(httpclient.CauseDNS, "api.opensubtitles.com"))
		}
		live := r.f.Bind(prov, "key-a", 0)
		if called, err := ran(t, live, Search, Automatic); !called || err != nil {
			t.Errorf("after released refusals and transients: ran %v, err %v", called, err)
		}
		_ = live.Call(t.Context(), Search, Automatic, returns(authFail()))
		time.Sleep(5 * time.Minute)
		inFlight(nil)
		start := time.Now()
		_ = live.Call(t.Context(), Search, Automatic, returns(authFail()))
		if got := backoffUntil(t, live.Call(t.Context(), Search, Interactive, returns(nil))); !got.Equal(start.Add(30 * time.Minute)) {
			t.Errorf("a released success reset the ladder: wait %v, want 30m", got.Sub(start))
		}
	})
}

func TestReleasedKeepsRateLimit(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		old := r.f.Bind(prov, "key-a", 0)
		start := time.Now()
		_ = old.Call(t.Context(), Search, Interactive, func(context.Context) error { old.Release(); return rateFail(2 * time.Minute) })
		b := r.f.Bind(prov, "key-b", 0)
		if called, err := ran(t, b, Search, Interactive); called || !backoffUntil(t, err).Equal(start.Add(2*time.Minute)) {
			t.Errorf("search after a released call's 429: ran %v, err %v", called, err)
		}
		if called, err := ran(t, b, Fetch, Interactive); !called || err != nil {
			t.Errorf("fetch after a search's 429: ran %v, err %v; want only search paused", called, err)
		}
	})
}

func TestPauseNotClearedBySuccess(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		_ = b.Call(t.Context(), Search, Automatic, returns(rateFail(time.Hour)))
		if called, err := ran(t, b, Fetch, Automatic); !called || err != nil {
			t.Fatalf("fetch: ran %v, err %v", called, err)
		}
		if called, err := ran(t, b, Search, Interactive); called || err == nil {
			t.Errorf("search after a fetch success: ran %v, err %v; want the pause kept", called, err)
		}
		time.Sleep(time.Hour)
		if called, err := ran(t, b, Search, Automatic); !called || err != nil {
			t.Errorf("search after the pause: ran %v, err %v", called, err)
		}
	})
}

func TestRatePauseLength(t *testing.T) {
	cases := []struct {
		name string
		// inner answers a call sent while outer's is in flight, so both
		// were admitted before either pause.
		outer, inner time.Duration
		want         time.Duration
		nested       bool
	}{
		{name: "retry_after", outer: 90 * time.Second, want: 90 * time.Second},
		{name: "default", outer: 0, want: 10 * time.Minute},
		{name: "capped", outer: 48 * time.Hour, want: 24 * time.Hour},
		{name: "later_kept", outer: time.Minute, inner: time.Hour, nested: true, want: time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				r := newRig(t)
				b := r.f.Bind(prov, "", 0)
				start := time.Now()
				_ = b.Call(t.Context(), Fetch, Interactive, func(ctx context.Context) error {
					if tc.nested {
						_ = b.Call(ctx, Fetch, Interactive, returns(rateFail(tc.inner)))
					}
					return rateFail(tc.outer)
				})
				if got := backoffUntil(t, b.Call(t.Context(), Fetch, Interactive, returns(nil))); !got.Equal(start.Add(tc.want)) {
					t.Errorf("pause = %v, want %v", got.Sub(start), tc.want)
				}
			})
		})
	}
}

func TestCredentialLRU(t *testing.T) {
	inBubble(t, "fifth_evicts_oldest", func(t *testing.T) {
		r := newRig(t)
		a := r.f.Bind(prov, "key-a", 0)
		_ = a.Call(t.Context(), Search, Automatic, returns(authFail()))
		for _, k := range []string{"key-b", "key-c", "key-d", "key-e"} {
			r.f.Bind(prov, k, 0)
		}
		if called, err := ran(t, a, Search, Automatic); !called || err != nil {
			t.Errorf("the least recently used key kept its wait: ran %v, err %v", called, err)
		}
	})
	inBubble(t, "use_keeps", func(t *testing.T) {
		r := newRig(t)
		a := r.f.Bind(prov, "key-a", 0)
		_ = a.Call(t.Context(), Search, Automatic, returns(authFail()))
		for _, k := range []string{"key-b", "key-c", "key-d"} {
			r.f.Bind(prov, k, 0)
		}
		if _, err := ran(t, a, Search, Automatic); err == nil {
			t.Error("the fourth key lost its wait")
		}
		r.f.Bind(prov, "key-e", 0)
		if _, err := ran(t, a, Search, Automatic); err == nil {
			t.Error("a key used before the fifth bind lost its wait")
		}
	})
}

func TestHealthCooldownAutomaticOnly(t *testing.T) {
	inBubble(t, "five_in_window", func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		start := time.Now()
		for range healthThreshold {
			_ = b.Call(t.Context(), Fetch, Interactive, returns(transientFail(httpclient.CauseReset, "api.opensubtitles.com")))
		}
		if called, err := ran(t, b, Search, Automatic); called || !backoffUntil(t, err).Equal(start.Add(time.Hour)) {
			t.Errorf("automatic during the cooldown: ran %v, err %v", called, err)
		}
		if called, err := ran(t, b, Search, Interactive); !called || err != nil {
			t.Errorf("interactive during the cooldown: ran %v, err %v", called, err)
		}
		time.Sleep(time.Hour)
		if called, err := ran(t, b, Search, Automatic); !called || err != nil {
			t.Errorf("automatic after the cooldown: ran %v, err %v", called, err)
		}
	})
	inBubble(t, "window_slides", func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		for range healthThreshold - 1 {
			_ = b.Call(t.Context(), Search, Interactive, returns(transientFail(httpclient.CauseReset, "api.opensubtitles.com")))
		}
		time.Sleep(healthWindow)
		_ = b.Call(t.Context(), Search, Interactive, returns(transientFail(httpclient.CauseReset, "api.opensubtitles.com")))
		if called, err := ran(t, b, Search, Automatic); !called || err != nil {
			t.Errorf("five transients over more than ten minutes: ran %v, err %v", called, err)
		}
	})
}

func TestProviderAlertDNSRaisedAndCleared(t *testing.T) {
	inBubble(t, "dns", func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		for range healthThreshold {
			_ = b.Call(t.Context(), Search, Interactive, returns(transientFail(httpclient.CauseDNS, "feed.animetosho.net")))
		}
		if a, ok := r.alert(t); !ok || !strings.Contains(a.Text, "feed.animetosho.net does not resolve") {
			t.Errorf("alert = %+v, %v; want the DNS cause", a, ok)
		}
		if s := r.providerState(t); s.Health != status.Degraded || s.Reason != "feed.animetosho.net does not resolve" {
			t.Errorf("status = %+v", s)
		}
		_ = b.Call(t.Context(), Search, Interactive, returns(nil))
		if a, ok := r.alert(t); ok {
			t.Errorf("alert %+v outlived a success", a)
		}
	})
	inBubble(t, "status_answer", func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		for range healthThreshold {
			_ = b.Call(t.Context(), Search, Interactive, returns(&provider.Failure{Kind: provider.Transient, Host: "api.opensubtitles.com", Status: 503}))
		}
		if a, ok := r.alert(t); ok {
			t.Errorf("a 503 cooldown raised %+v", a)
		}
		if s := r.providerState(t); s.Reason != "api.opensubtitles.com answered 503" {
			t.Errorf("status reason %q", s.Reason)
		}
	})
}

func TestCallRefusesBadArguments(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		for _, tc := range []struct {
			op Op
			c  Class
		}{{Test, Interactive}, {0, Interactive}, {Search, 0}} {
			if called, err := ran(t, b, tc.op, tc.c); called || !errors.Is(err, errBadCall) {
				t.Errorf("Call(%v, %d): ran %v, err %v", tc.op, tc.c, called, err)
			}
		}
	})
}

func TestNewFunnelRequiresEveryArgument(t *testing.T) {
	r := newRig(t)
	for name, build := range map[string]func(){
		"store":  func() { NewFunnel(nil, r.h, r.st) },
		"hasher": func() { NewFunnel(r.db, nil, r.st) },
		"status": func() { NewFunnel(r.db, r.h, nil) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Error("no panic")
				}
			}()
			build()
		})
	}
}

func TestCollectors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		reg := measure.NewRegistry("subflux")
		for _, m := range r.f.Collectors() {
			if err := reg.Register(m); err != nil {
				t.Fatal(err)
			}
		}
		b := r.f.Bind(prov, "", 0)
		_ = b.Call(t.Context(), Search, Interactive, returns(nil))
		_ = b.Call(t.Context(), Fetch, Interactive, returns(rateFail(time.Minute)))
		_ = b.Call(t.Context(), Fetch, Interactive, returns(nil))
		rec := httptest.NewRecorder()
		reg.Handler()(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/metrics", nil))
		body := rec.Body.String()
		for _, want := range []string{
			`subflux_provider_calls_total{op="search",provider="opensubtitles",result="ok"} 1`,
			`subflux_provider_calls_total{op="fetch",provider="opensubtitles",result="rate_limited"} 1`,
			`subflux_provider_calls_total{op="fetch",provider="opensubtitles",result="backoff"} 1`,
			`subflux_provider_call_duration_seconds_count{op="fetch",provider="opensubtitles"} 1`,
			`subflux_provider_health{health="degraded",provider="opensubtitles"} 1`,
			`subflux_provider_health{health="ok",provider="opensubtitles"} 0`,
		} {
			if !strings.Contains(body, want) {
				t.Errorf("exposition lacks %s", want)
			}
		}
	})
}
