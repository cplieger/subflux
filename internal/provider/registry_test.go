package provider

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
)

// captureRegistryLogs routes the default logger into a buffer for the rest of
// the test and restores it afterwards. The default logger is process-global, so
// a test that calls this must not run in parallel.
func captureRegistryLogs(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return &buf
}

func TestRegister_and_LoadAll(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	called := false
	r.Register("test", func(_ context.Context, _ map[string]any) (Provider, error) {
		called = true
		return &fakeProvider{name: "test"}, nil
	})

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"test": {Enabled: true, Settings: map[string]any{"key": "val"}},
	})
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if !called {
		t.Error("LoadAll() did not call factory")
	}
	if len(providers) != 1 {
		t.Fatalf("LoadAll() returned %d providers, want 1", len(providers))
	}
	if providers[0].Name() != "test" {
		t.Errorf("providers[0].Name() = %q, want %q", providers[0].Name(), "test")
	}
}

func TestLoadAll_skips_disabled(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register("disabled", func(_ context.Context, _ map[string]any) (Provider, error) {
		t.Fatal("factory should not be called for disabled provider")
		return nil, nil
	})
	r.Register("enabled", func(_ context.Context, _ map[string]any) (Provider, error) {
		return &fakeProvider{name: "enabled"}, nil
	})

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"disabled": {Enabled: false},
		"enabled":  {Enabled: true},
	})
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("LoadAll() returned %d providers, want 1", len(providers))
	}
	if providers[0].Name() != "enabled" {
		t.Errorf("providers[0].Name() = %q, want %q", providers[0].Name(), "enabled")
	}
}

func TestLoadAll_unknown_provider_skipped(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"unknown": {Enabled: true},
	})
	// Unknown provider is skipped with a warning; zero providers loading is
	// a valid state (embedded detection and coverage only), not an error.
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("LoadAll() returned %d providers, want 0", len(providers))
	}
}

func TestLoadAll_unknown_provider_with_valid(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register("good", func(_ context.Context, _ map[string]any) (Provider, error) {
		return &fakeProvider{name: "good"}, nil
	})

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"good":    {Enabled: true},
		"unknown": {Enabled: true},
	})
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if len(providers) != 1 {
		t.Errorf("LoadAll() got %d providers, want 1", len(providers))
	}
}

func TestLoadAll_factory_error(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	cause := errors.New("init failed")
	r.Register("broken", func(_ context.Context, _ map[string]any) (Provider, error) {
		return nil, cause
	})

	_, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"broken": {Enabled: true},
	})
	if err == nil {
		t.Fatal("LoadAll() expected error for factory failure")
	}
	if !strings.Contains(err.Error(), "init provider broken") {
		t.Errorf("error = %q, want substring %q", err, "init provider broken")
	}
	if !errors.Is(err, cause) {
		t.Errorf("error chain does not wrap cause: %v", err)
	}
}

func TestLoadAll_partial_success(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register("good", func(_ context.Context, _ map[string]any) (Provider, error) {
		return &fakeProvider{name: "good"}, nil
	})
	r.Register("broken", func(_ context.Context, _ map[string]any) (Provider, error) {
		return nil, errors.New("init failed")
	})

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"good":   {Enabled: true},
		"broken": {Enabled: true},
	})
	if err == nil {
		t.Fatal("LoadAll() expected error for partial failure")
	}
	if !strings.Contains(err.Error(), "init provider broken") {
		t.Errorf("error = %q, want substring %q", err, "init provider broken")
	}
	if len(providers) != 1 {
		t.Fatalf("LoadAll() returned %d providers, want 1 (partial success)", len(providers))
	}
	if providers[0].Name() != "good" {
		t.Errorf("providers[0].Name() = %q, want %q", providers[0].Name(), "good")
	}
}

func TestLoadAll_no_providers_loaded(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"test": {Enabled: false},
	})
	// All-disabled is a deliberate, valid state after the embedded-detector
	// separation: no error, zero providers.
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("LoadAll() returned %d providers, want 0", len(providers))
	}
}

// TestLoadAll_warnsWithTheClassificationCountsWhenNothingLoads asserts the
// embedded-only outcome is reported with the counts that let an operator tell a
// config typo (unknown > 0) from a deliberate all-disabled setup.
// Not parallel: it swaps the process-wide default logger.
func TestLoadAll_warnsWithTheClassificationCountsWhenNothingLoads(t *testing.T) {
	logs := captureRegistryLogs(t)
	r := NewRegistry()
	r.Register("known", func(_ context.Context, _ map[string]any) (Provider, error) {
		return &fakeProvider{name: "known"}, nil
	})

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"known":   {Enabled: false},
		"missing": {Enabled: true},
	})
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if len(providers) != 0 {
		t.Fatalf("LoadAll() returned %d providers, want 0", len(providers))
	}

	const want = `msg="no acquisition providers loaded; embedded detection and coverage only" configured=2 disabled=1 unknown=1`
	if got := logs.String(); !strings.Contains(got, want) {
		t.Errorf("LoadAll() log = %q, want it to contain %q", got, want)
	}
}

func TestLoadAll_empty_config(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{})
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if len(providers) != 0 {
		t.Errorf("LoadAll() returned %d providers, want 0", len(providers))
	}
}

func TestLoadAll_deterministic_order(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, name := range []subflux.ProviderID{"zeta", "alpha", "mid"} {
		r.Register(name, func(_ context.Context, _ map[string]any) (Provider, error) {
			return &fakeProvider{name: string(name)}, nil
		})
	}

	cfg := map[subflux.ProviderID]subflux.ProviderCfg{
		"zeta":  {Enabled: true},
		"alpha": {Enabled: true},
		"mid":   {Enabled: true},
	}

	// Run multiple times to verify ordering is stable.
	for range 10 {
		providers, err := r.LoadAll(t.Context(), cfg)
		if err != nil {
			t.Fatalf("LoadAll() unexpected error: %v", err)
		}
		want := []string{"alpha", "mid", "zeta"}
		if len(providers) != len(want) {
			t.Fatalf("LoadAll() returned %d providers, want %d", len(providers), len(want))
		}
		for i, p := range providers {
			if string(p.Name()) != want[i] {
				t.Errorf("providers[%d].Name() = %q, want %q", i, p.Name(), want[i])
			}
		}
	}
}

func TestRegister_panics_empty_name(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	defer func() {
		if recover() == nil {
			t.Fatal("Register(\"\", f) did not panic")
		}
	}()
	r.Register("", func(_ context.Context, _ map[string]any) (Provider, error) {
		return nil, nil
	})
}

func TestRegister_panics_nil_factory(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	defer func() {
		if recover() == nil {
			t.Fatal("Register(name, nil) did not panic")
		}
	}()
	r.Register("test", nil)
}

func TestLoadAll_passes_settings_to_factory(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	var got map[string]any
	r.Register("test", func(_ context.Context, s map[string]any) (Provider, error) {
		got = s
		return &fakeProvider{name: "test"}, nil
	})

	want := map[string]any{"user": "alice", "pass": "secret"}
	_, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"test": {Enabled: true, Settings: want},
	})
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if len(got) != len(want) {
		t.Errorf("factory received settings %v, want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("factory settings[%q] = %v, want %v", k, got[k], v)
		}
	}
}

func TestRegister_overwrites_duplicate(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	r.Register("dup", func(_ context.Context, _ map[string]any) (Provider, error) {
		return &fakeProvider{name: "first"}, nil
	})
	r.Register("dup", func(_ context.Context, _ map[string]any) (Provider, error) {
		return &fakeProvider{name: "second"}, nil
	})

	providers, err := r.LoadAll(t.Context(), map[subflux.ProviderID]subflux.ProviderCfg{
		"dup": {Enabled: true},
	})
	if err != nil {
		t.Fatalf("LoadAll() unexpected error: %v", err)
	}
	if len(providers) != 1 {
		t.Fatalf("LoadAll() returned %d providers, want 1", len(providers))
	}
	if providers[0].Name() != "second" {
		t.Errorf("providers[0].Name() = %q, want %q (last registered wins)", providers[0].Name(), "second")
	}
}

// --- RegisterSchema, ProviderNames, Schema ---

func TestRegisterSchema_and_Schema(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	fields := []subflux.ProviderSchemaField{
		{Key: "api_key", Label: "API Key", Type: "secret", Secret: true},
		{Key: "enabled", Label: "Enabled", Type: "bool", Default: "true"},
	}
	r.RegisterSchema("test", "Test Provider", fields)

	label, got := r.Schema("test")
	if label != "Test Provider" {
		t.Errorf("Schema(\"test\") label = %q, want %q", label, "Test Provider")
	}
	if len(got) != 2 {
		t.Fatalf("Schema(\"test\") returned %d fields, want 2", len(got))
	}
	if got[0].Key != "api_key" {
		t.Errorf("Schema(\"test\")[0].Key = %q, want %q", got[0].Key, "api_key")
	}
}

// The registry is built once at boot and read by the settings-UI handler on
// every request, so a caller that sorts or rewrites the returned fields in
// place would be rewriting what every later request renders. One level of clone
// is the whole copy: ProviderSchemaField holds five strings and a bool and no
// reference type.
func TestSchema_copies_fields_on_return(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.RegisterSchema("test", "Test Provider", []subflux.ProviderSchemaField{
		{Key: "api_key", Label: "API Key", Type: "secret", Secret: true},
		{Key: "enabled", Label: "Enabled", Type: "bool", Default: "true"},
	})

	_, got := r.Schema("test")
	if len(got) != 2 {
		t.Fatalf("Schema(\"test\") returned %d fields, want 2", len(got))
	}
	got[0] = subflux.ProviderSchemaField{Key: "tampered", Secret: false}
	got[1].Label = "tampered"

	_, after := r.Schema("test")
	if after[0].Key != "api_key" || !after[0].Secret {
		t.Errorf("field[0] = %+v, want api_key/secret (caller edited the live schema)", after[0])
	}
	if after[1].Label != "Enabled" {
		t.Errorf("field[1].Label = %q, want %q (caller edited the live schema)", after[1].Label, "Enabled")
	}
}

func TestSchema_unknown_provider(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	label, fields := r.Schema("unknown")
	if label != "" {
		t.Errorf("Schema(\"unknown\") label = %q, want empty", label)
	}
	if fields != nil {
		t.Errorf("Schema(\"unknown\") fields = %v, want nil", fields)
	}
}

func TestProviderNames_sorted(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, name := range []subflux.ProviderID{"zeta", "alpha", "mid"} {
		n := name
		r.Register(n, func(_ context.Context, _ map[string]any) (Provider, error) {
			return &fakeProvider{name: string(n)}, nil
		})
	}

	got := r.ProviderNames()
	want := []string{"alpha", "mid", "zeta"}
	if len(got) != len(want) {
		t.Fatalf("ProviderNames() returned %d names, want %d", len(got), len(want))
	}
	for i, name := range got {
		if string(name) != want[i] {
			t.Errorf("ProviderNames()[%d] = %q, want %q", i, name, want[i])
		}
	}
}

func TestProviderNames_empty_registry(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	got := r.ProviderNames()
	if len(got) != 0 {
		t.Errorf("ProviderNames() = %v, want empty", got)
	}
}

// --- Test Helpers ---

// fakeProvider implements Provider for testing.
type fakeProvider struct {
	name string
}

func (f *fakeProvider) Name() subflux.ProviderID { return subflux.ProviderID(f.name) }

func (f *fakeProvider) Search(_ context.Context, _ *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	return nil, nil
}

func (f *fakeProvider) Download(_ context.Context, _ *subflux.Subtitle) ([]byte, error) {
	return nil, nil
}

// Mechanical pin for the C14 deletion. The two things removed were a
// declaration and a comment, so neither the compiler nor a linter can hold
// them: `var _ = FactoryFunc(nil)` asserted nothing (a declared type is always
// a valid type) and the doc above it described a `providerFactories` symbol
// that has never existed in this package. A doc comment naming a symbol the
// package does not declare is the defect; only a source scan sees it.
func TestRegistrySource_noVacuousAssertionOrOrphanedDoc(t *testing.T) {
	t.Parallel()
	src, err := os.ReadFile("registry.go")
	if err != nil {
		t.Fatalf("read registry.go: %v", err)
	}
	for _, dead := range []string{"providerFactories", "var _ = FactoryFunc("} {
		if strings.Contains(string(src), dead) {
			t.Errorf("registry.go still contains %q", dead)
		}
	}
}
