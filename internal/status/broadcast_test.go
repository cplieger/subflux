package status

import (
	"slices"
	"testing"
	"testing/synctest"
	"time"
)

func TestChangedWakesEveryObserver(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := newStore()
		_, before := s.Snapshot()
		seen := make(chan uint64, 2)
		for range 2 {
			go func() {
				<-s.Changed()
				_, seq := s.Snapshot()
				seen <- seq
			}()
		}
		synctest.Wait()
		s.Reconcile(AlertStore, true, "disk full")
		synctest.Wait()
		if len(seen) != 2 {
			t.Fatalf("%d of 2 observers woke", len(seen))
		}
		for range 2 {
			if seq := <-seen; seq <= before {
				t.Errorf("an observer read seq %d, want above %d", seq, before)
			}
		}
	})
}

func TestMutationsBumpSeqOnlyOnChange(t *testing.T) {
	s := newStore()
	snap, seq := s.Snapshot()
	if snap.Boot == ([16]byte{}) {
		t.Error("Boot is zero")
	}
	steps := []struct {
		name   string
		act    func()
		change bool
	}{
		{"a raise", func() { s.Reconcile(AlertStore, true, "full") }, true},
		{"a re-assert with the same text", func() { s.Reconcile(AlertStore, true, "full") }, false},
		{"a clear", func() { s.Reconcile(AlertStore, false, "") }, true},
		{"a clear of nothing", func() { s.Reconcile(AlertStore, false, "") }, false},
		{"a provider", func() { s.SetProvider(ProviderState{ID: "b", Health: Degraded, Reason: "429"}) }, true},
		{"the same provider state", func() { s.SetProvider(ProviderState{ID: "b", Health: Degraded, Reason: "429"}) }, false},
		{"another provider", func() { s.SetProvider(ProviderState{ID: "a", Health: OK, Until: time.Unix(5, 0)}) }, true},
	}
	for _, st := range steps {
		st.act()
		next, nseq := s.Snapshot()
		if (nseq != seq) != st.change {
			t.Errorf("%s: seq %d to %d, want a change %v", st.name, seq, nseq, st.change)
		}
		if next.Boot != snap.Boot {
			t.Errorf("%s: Boot changed", st.name)
		}
		seq = nseq
	}
	snap, _ = s.Snapshot()
	ids := []string{}
	for _, p := range snap.Providers {
		ids = append(ids, p.ID)
	}
	if !slices.Equal(ids, []string{"a", "b"}) {
		t.Errorf("providers %v, want ordered by id", ids)
	}
}

func TestSetProviderCutsReason(t *testing.T) {
	s := newStore()
	s.SetProvider(ProviderState{ID: "p", Health: Disabled, Reason: "line\nforged"})
	snap, _ := s.Snapshot()
	if len(snap.Providers) != 1 || snap.Providers[0].Reason != "line forged" {
		t.Errorf("providers %+v, want the reason on one line", snap.Providers)
	}
}
