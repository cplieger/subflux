package server

import (
	"context"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/server/activity"
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
	// Every real caller supplies WithMetrics, WithAlertLog, WithProviderGate
	// and WithMediaWriter; the subject is the unconfigured CONFIG path, not a
	// missing collaborator.
	gate, err := providergate.Open(t.Context(), providergate.Config{Metrics: testsupport.NopGateMetrics{}})
	if err != nil {
		t.Fatalf("providergate.Open: %v", err)
	}
	srv := New(&testsupport.NopStore{}, nopProviderRegistry{},
		WithDefaultConfig([]byte("# default\n")),
		WithMetrics(obs.New()),
		WithAlertLog(activity.NewAlertLog(100)),
		WithProviderGate(gate),
		WithMediaWriter(testsupport.MediaWriter()),
		WithMediaPresence(testsupport.MediaPresence()),
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
func (nopProviderRegistry) CredentialCheck(_ subflux.ProviderID) bool { return false }
func (nopProviderRegistry) CheckCredentials(_ context.Context, _ subflux.ProviderID, _ map[string]any) error {
	return nil
}

func (nopProviderRegistry) Normalize(_ subflux.ProviderID, raw map[string]any) map[string]any {
	return raw
}

func TestNew_requires_metrics_and_the_alert_log_and_Start_the_media_writer(t *testing.T) {
	gate, err := providergate.Open(t.Context(), providergate.Config{Metrics: testsupport.NopGateMetrics{}})
	if err != nil {
		t.Fatalf("providergate.Open: %v", err)
	}
	panicMsg := func(f func()) (msg string) {
		defer func() { msg, _ = recover().(string) }()
		f()
		return ""
	}

	msg := panicMsg(func() {
		New(&testsupport.NopStore{}, nopProviderRegistry{}, WithMetrics(obs.New()), WithProviderGate(gate))
	})
	if msg != "server.New: WithAlertLog is required" {
		t.Errorf("New without WithAlertLog panicked with %q, want the WithAlertLog message", msg)
	}

	var noMetrics *obs.Metrics
	msg = panicMsg(func() {
		New(&testsupport.NopStore{}, nopProviderRegistry{}, WithMetrics(noMetrics),
			WithAlertLog(activity.NewAlertLog(100)), WithProviderGate(gate))
	})
	if msg != "server.New: WithMetrics is required" {
		t.Errorf("New with a nil *obs.Metrics panicked with %q, want the WithMetrics message", msg)
	}

	srv := New(&testsupport.NopStore{}, nopProviderRegistry{},
		WithMetrics(obs.New()), WithAlertLog(activity.NewAlertLog(100)), WithProviderGate(gate))
	if msg := panicMsg(srv.requireServiceable); !strings.HasPrefix(msg, "server: media is not wired") {
		t.Errorf("requireServiceable without WithMediaWriter panicked with %q, want it to name media", msg)
	}

	srv = New(&testsupport.NopStore{}, nopProviderRegistry{},
		WithMetrics(obs.New()), WithAlertLog(activity.NewAlertLog(100)), WithProviderGate(gate),
		WithMediaWriter(testsupport.MediaWriter()))
	if msg := panicMsg(srv.requireServiceable); !strings.HasPrefix(msg, "server: presence is not wired") {
		t.Errorf("requireServiceable without WithMediaPresence panicked with %q, want it to name presence", msg)
	}
}
