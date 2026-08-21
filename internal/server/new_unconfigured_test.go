package server

import (
	"context"
	"testing"

	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// TestNew_UnconfiguredMode_NoPanic guards against a regression where
// server.New crashed with a nil pointer dereference in unconfigured
// mode (no WithConfig option) because polling.NewPoller invokes the
// stateFunc closure during construction (to read PollInterval for the
// tag-cache TTL) before Server.live had been initialized to a non-nil
// liveState. The fix initializes Server.live to a zero liveState
// immediately after the opts loop, so all dep-construction closures
// can safely read it.
//
// History: introduced when the cleanup-remaining-lint pass moved the
// late "ensure live state is non-nil" guard to after polling.NewPoller
// rather than before it. Surfaced in production logs on a self-hosted
// deployment with "starting in unconfigured mode" + segfault. CI did not
// catch this because no existing test exercises server.New directly:
// every test in this package constructs *Server{} field-literal style.
func TestNew_UnconfiguredMode_NoPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("server.New panicked in unconfigured mode: %v", r)
		}
	}()
	// WithMetrics because it is required, like every real caller supplies it.
	// The subject here is the unconfigured CONFIG path, not a missing recorder.
	srv := New(&testsupport.NopStore{}, nopProviderRegistry{},
		WithDefaultConfig([]byte("# default\n")),
		WithMetrics(obs.New()),
	)
	if srv == nil {
		t.Fatal("New returned nil")
	}
	if srv.live.Load() == nil {
		t.Fatal("live state should be non-nil after New (unconfigured mode)")
	}
}

// nopProviderRegistry is a minimal confighandlers.SchemaRegistry that returns
// empty values; sufficient for unconfigured-mode startup.
type nopProviderRegistry struct{}

func (nopProviderRegistry) LoadAll(_ context.Context, _ map[subflux.ProviderID]subflux.ProviderCfg) ([]provider.Provider, error) {
	return nil, nil
}
func (nopProviderRegistry) ProviderNames() []subflux.ProviderID { return nil }
func (nopProviderRegistry) Schema(_ subflux.ProviderID) (string, []subflux.ProviderSchemaField) {
	return "", nil
}
