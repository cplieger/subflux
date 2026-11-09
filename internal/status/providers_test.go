package status_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/status"
	"github.com/cplieger/subflux/internal/status/statustest"
)

func providerIDs(s *status.Store) []string {
	snap, _ := s.Snapshot()
	ids := make([]string, 0, len(snap.Providers))
	for _, p := range snap.Providers {
		ids = append(ids, p.ID)
	}
	return ids
}

func closed(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func TestRemovedProviderLeavesSnapshot(t *testing.T) {
	s := open(t, statustest.NewMemPersister())
	s.SetProvider(status.ProviderState{ID: "z", Health: status.OK})
	if got := providerIDs(s); !slices.Equal(got, []string{"z"}) {
		t.Fatalf("before RetainProviders, providers %v, want any id accepted", got)
	}

	s.RetainProviders([]string{"a", "b"})
	s.SetProvider(status.ProviderState{ID: "a", Health: status.OK})
	s.SetProvider(status.ProviderState{ID: "b", Health: status.Degraded, Reason: "rate limited"})
	if got := providerIDs(s); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("providers %v, want [a b]", got)
	}

	woken := s.Changed()
	_, before := s.Snapshot()
	s.RetainProviders([]string{"a"})
	if got := providerIDs(s); !slices.Equal(got, []string{"a"}) {
		t.Errorf("after dropping b, providers %v, want [a]", got)
	}
	if !closed(woken) {
		t.Error("dropping b did not wake a blocked Changed observer")
	}
	if _, after := s.Snapshot(); after != before+1 {
		t.Errorf("dropping b moved the sequence from %d to %d, want one step", before, after)
	}

	quiet := s.Changed()
	_, before = s.Snapshot()
	s.SetProvider(status.ProviderState{ID: "b", Health: status.Disabled})
	if got := providerIDs(s); !slices.Equal(got, []string{"a"}) {
		t.Errorf("after a late SetProvider for b, providers %v, want [a]", got)
	}
	if _, after := s.Snapshot(); closed(quiet) || after != before {
		t.Errorf("a late SetProvider for b signalled (sequence %d to %d)", before, after)
	}

	quiet = s.Changed()
	_, before = s.Snapshot()
	s.RetainProviders([]string{"a"})
	if _, after := s.Snapshot(); closed(quiet) || after != before {
		t.Errorf("a retain that drops nothing signalled (sequence %d to %d)", before, after)
	}

	s.RetainProviders([]string{"a", "b"})
	s.SetProvider(status.ProviderState{ID: "b", Health: status.OK})
	if got := providerIDs(s); !slices.Equal(got, []string{"a", "b"}) {
		t.Errorf("after b is retained again, providers %v, want [a b]", got)
	}
}
