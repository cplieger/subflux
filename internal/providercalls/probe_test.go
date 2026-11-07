package providercalls

import (
	"context"
	"errors"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/go/httpclient"
)

// probe reports whether Probe ran its fn.
func probe(t *testing.T, f *Funnel, credential string, err error) (called, cleared bool, got error) {
	t.Helper()
	cleared, got = f.Probe(t.Context(), prov, credential, func(context.Context) error { called = true; return err })
	return called, cleared, got
}

func TestProbeClearsAuthCooldown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "key-a", 0)
		_ = b.Call(t.Context(), Search, Automatic, returns(authFail()))
		if called, cleared, err := probe(t, r.f, "key-a", nil); !called || cleared || err != nil {
			t.Fatalf("Probe: ran %v, cleared %v, err %v", called, cleared, err)
		}
		if called, err := ran(t, b, Search, Automatic); !called || err != nil {
			t.Errorf("after a passing Probe: ran %v, err %v; want the wait reset", called, err)
		}
		start := time.Now()
		_ = b.Call(t.Context(), Search, Automatic, returns(authFail()))
		if got := backoffUntil(t, b.Call(t.Context(), Search, Interactive, returns(nil))); !got.Equal(start.Add(5 * time.Minute)) {
			t.Errorf("the next refusal waits %v, want 5m (count reset)", got.Sub(start))
		}
	})
}

func TestProbeVerifyErrorIsErr(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "key-a", 0)
		disable(t, b)
		r.h.setBusy(errBusy)
		called, cleared, err := probe(t, r.f, "key-a", nil)
		r.h.setBusy(nil)
		if !called || cleared || !errors.Is(err, errBusy) {
			t.Errorf("Probe with a busy hasher: ran %v, cleared %v, err %v; want the hasher's error", called, cleared, err)
		}
		if _, found := r.record(t); !found {
			t.Error("the record went with a busy hasher")
		}
		if called, err := ran(t, b, Search, Interactive); called || !errors.Is(err, ErrCredentialDisabled) {
			t.Errorf("after the failed Probe: ran %v, err %v", called, err)
		}
	})
}

func TestProbeAdmitIgnoresRecordAndHealth(t *testing.T) {
	inBubble(t, "disabled", func(t *testing.T) {
		r := newRig(t)
		disable(t, r.f.Bind(prov, "key-a", 0))
		if called, _, _ := probe(t, r.f, "key-a", errBusy); !called {
			t.Error("Probe of a disabled credential did not run")
		}
	})
	inBubble(t, "health_cooldown", func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		for range healthThreshold {
			_ = b.Call(t.Context(), Search, Interactive, returns(transientFail(httpclient.CauseReset, "api.opensubtitles.com")))
		}
		if called, _, err := probe(t, r.f, "", nil); !called || err != nil {
			t.Errorf("Probe during a cooldown: ran %v, err %v", called, err)
		}
	})
	inBubble(t, "search_pause", func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "", 0)
		start := time.Now()
		_ = b.Call(t.Context(), Search, Interactive, returns(rateFail(time.Hour)))
		called, _, err := probe(t, r.f, "", nil)
		if called || !backoffUntil(t, err).Equal(start.Add(time.Hour)) {
			t.Errorf("Probe during a search pause: ran %v, err %v", called, err)
		}
	})
}

func TestUnsavedTestKeepsRecord(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		a := r.f.Bind(prov, "key-a", 0)
		disable(t, a)
		if called, cleared, err := probe(t, r.f, "key-b", nil); !called || cleared || err != nil {
			t.Errorf("a draft Probe: ran %v, cleared %v, err %v", called, cleared, err)
		}
		if rec, found := r.record(t); !found || rec.Fingerprint != "fp:key-a" {
			t.Errorf("record after a draft Probe = %+v, %v", rec, found)
		}
		if called, err := ran(t, a, Search, Interactive); called || !errors.Is(err, ErrCredentialDisabled) {
			t.Errorf("the disabled key after a draft Probe: ran %v, err %v", called, err)
		}
	})
}

func TestProbeReenablesLiveCredential(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		a := r.f.Bind(prov, "key-a", 0)
		disable(t, a)
		if called, cleared, err := probe(t, r.f, "key-a", nil); !called || !cleared || err != nil {
			t.Fatalf("Probe of the disabled key: ran %v, cleared %v, err %v", called, cleared, err)
		}
		if _, found := r.record(t); found {
			t.Error("the record outlived a re-proving Probe")
		}
		if called, err := ran(t, a, Search, Automatic); !called || err != nil {
			t.Errorf("the live binding after the Probe: ran %v, err %v", called, err)
		}
		if a, ok := r.alert(t); ok {
			t.Errorf("alert %+v outlived the re-proving Probe", a)
		}
	})
}

func TestProbeHonoursRatePause(t *testing.T) {
	inBubble(t, "fetch_pause_ignored", func(t *testing.T) {
		r := newRig(t)
		_ = r.f.Bind(prov, "", 0).Call(t.Context(), Fetch, Interactive, returns(rateFail(time.Hour)))
		if called, _, err := probe(t, r.f, "", nil); !called || err != nil {
			t.Errorf("Probe during a fetch pause: ran %v, err %v", called, err)
		}
	})
	inBubble(t, "test_pause", func(t *testing.T) {
		r := newRig(t)
		start := time.Now()
		if _, _, err := probe(t, r.f, "", rateFail(time.Hour)); err == nil {
			t.Fatal("a rate-limited Probe returned nil")
		}
		called, _, err := probe(t, r.f, "", nil)
		if called || !backoffUntil(t, err).Equal(start.Add(time.Hour)) {
			t.Errorf("Probe after a rate-limited Probe: ran %v, err %v", called, err)
		}
		if called, err := ran(t, r.f.Bind(prov, "", 0), Search, Automatic); !called || err != nil {
			t.Errorf("a live search after a rate-limited Probe: ran %v, err %v", called, err)
		}
	})
}

func TestProbeFailureFeedsNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		b := r.f.Bind(prov, "key-a", 0)
		for range maxAuthFailures {
			_, _, _ = probe(t, r.f, "key-a", authFail())
		}
		for range healthThreshold {
			_, _, _ = probe(t, r.f, "key-a", transientFail(httpclient.CauseDNS, "api.opensubtitles.com"))
		}
		if called, err := ran(t, b, Search, Automatic); !called || err != nil {
			t.Errorf("after failing Probes: ran %v, err %v; want nothing fed", called, err)
		}
		if _, found := r.record(t); found {
			t.Error("failing Probes wrote a record")
		}
	})
}

func TestProbeCallerLeft(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t)
		ctx, cancel := context.WithCancel(t.Context())
		_, err := r.f.Probe(ctx, prov, "", func(context.Context) error { cancel(); return rateFail(time.Hour) })
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Probe whose caller left returned %v", err)
		}
		if called, _, err := probe(t, r.f, "", nil); !called || err != nil {
			t.Errorf("after a left Probe: ran %v, err %v; want no pause", called, err)
		}
	})
}
