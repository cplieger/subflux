package provider

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

func TestNewRegistry_empty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if r == nil {
		t.Fatal("NewRegistry() returned nil")
	}
	if len(r.factories) != 0 {
		t.Errorf("NewRegistry().factories has %d entries, want 0", len(r.factories))
	}
}

func TestRegister_and_LoadAll(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	called := false
	r.Register("test", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		called = true
		return &fakeProvider{name: "test"}, nil
	})

	providers, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
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
	r.Register("disabled", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		t.Fatal("factory should not be called for disabled provider")
		return nil, nil
	})
	r.Register("enabled", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &fakeProvider{name: "enabled"}, nil
	})

	providers, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
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

	_, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
		"unknown": {Enabled: true},
	})
	// Unknown provider is skipped, but with no other providers loaded
	// the result is a shaped "no providers loaded" error with counts.
	if err == nil {
		t.Fatal("LoadAll() expected error when only unknown providers exist")
	}
	if !strings.Contains(err.Error(), "no providers loaded") {
		t.Errorf("error = %q, want substring %q", err, "no providers loaded")
	}
	if !strings.Contains(err.Error(), "unknown=1") {
		t.Errorf("error = %q, want substring %q (shaped count)", err, "unknown=1")
	}
}

func TestLoadAll_unknown_provider_with_valid(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Register("good", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &fakeProvider{name: "good"}, nil
	})

	providers, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
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
	r.Register("broken", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return nil, cause
	})

	_, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
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
	r.Register("good", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &fakeProvider{name: "good"}, nil
	})
	r.Register("broken", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return nil, errors.New("init failed")
	})

	providers, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
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

	_, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
		"test": {Enabled: false},
	})
	if err == nil {
		t.Fatal("LoadAll() expected error when no providers loaded")
	}
	if !strings.Contains(err.Error(), "no providers loaded") {
		t.Errorf("error = %q, want substring %q", err, "no providers loaded")
	}
	if !strings.Contains(err.Error(), "disabled=1") {
		t.Errorf("error = %q, want substring %q (shaped count)", err, "disabled=1")
	}
}

func TestLoadAll_empty_config(t *testing.T) {
	t.Parallel()
	r := NewRegistry()

	_, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{})
	if err == nil {
		t.Fatal("LoadAll() expected error for empty config")
	}
	if !strings.Contains(err.Error(), "no providers loaded") {
		t.Errorf("error = %q, want substring %q", err, "no providers loaded")
	}
}

func TestLoadAll_deterministic_order(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	for _, name := range []api.ProviderID{"zeta", "alpha", "mid"} {
		r.Register(name, func(_ context.Context, _ map[string]any) (api.Provider, error) {
			return &fakeProvider{name: string(name)}, nil
		})
	}

	cfg := map[api.ProviderID]api.ProviderCfg{
		"zeta":  {Enabled: true},
		"alpha": {Enabled: true},
		"mid":   {Enabled: true},
	}

	// Run multiple times to verify ordering is stable.
	for range 10 {
		providers, err := r.LoadAll(context.Background(), cfg)
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
	r.Register("", func(_ context.Context, _ map[string]any) (api.Provider, error) {
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
	r.Register("test", func(_ context.Context, s map[string]any) (api.Provider, error) {
		got = s
		return &fakeProvider{name: "test"}, nil
	})

	want := map[string]any{"user": "alice", "pass": "secret"}
	_, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
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

	r.Register("dup", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &fakeProvider{name: "first"}, nil
	})
	r.Register("dup", func(_ context.Context, _ map[string]any) (api.Provider, error) {
		return &fakeProvider{name: "second"}, nil
	})

	providers, err := r.LoadAll(context.Background(), map[api.ProviderID]api.ProviderCfg{
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

// --- SettingBool ---

func TestSettingBool(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		key      SettingKey
		def      bool
		want     bool
	}{
		{"native true", map[string]any{"k": true}, "k", false, true},
		{"native false", map[string]any{"k": false}, "k", true, false},
		{"string true", map[string]any{"k": "true"}, "k", false, true},
		{"string false", map[string]any{"k": "false"}, "k", true, false},
		{"missing key returns default true", map[string]any{}, "k", true, true},
		{"missing key returns default false", map[string]any{}, "k", false, false},
		{"nil map returns default", nil, "k", true, true},
		{"non-bool non-string returns default", map[string]any{"k": 42}, "k", true, true},
		{"string yes is not true", map[string]any{"k": "yes"}, "k", false, false},
		{"unrecognized string returns default", map[string]any{"k": "yes"}, "k", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SettingBool(tt.settings, tt.key, tt.def)
			if got != tt.want {
				t.Errorf("SettingBool(%v, %q, %v) = %v, want %v",
					tt.settings, tt.key, tt.def, got, tt.want)
			}
		})
	}
}

// --- SettingString ---

func TestSettingString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		key      SettingKey
		want     string
	}{
		{"present", map[string]any{"k": "val"}, "k", "val"},
		{"empty string", map[string]any{"k": ""}, "k", ""},
		{"missing key", map[string]any{}, "k", ""},
		{"nil map", nil, "k", ""},
		{"non-string value", map[string]any{"k": 42}, "k", ""},
		{"bool value", map[string]any{"k": true}, "k", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SettingString(tt.settings, tt.key)
			if got != tt.want {
				t.Errorf("SettingString(%v, %q) = %q, want %q",
					tt.settings, tt.key, got, tt.want)
			}
		})
	}
}

// --- SettingInt ---

func TestSettingInt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		key      SettingKey
		def      int
		want     int
	}{
		{"native int", map[string]any{"k": 42}, "k", 0, 42},
		{"native int64", map[string]any{"k": int64(7)}, "k", 0, 7},
		{"native float64 whole", map[string]any{"k": 3.0}, "k", 0, 3},
		{"native float64 non-whole returns default", map[string]any{"k": 3.5}, "k", 99, 99},
		{"numeric string", map[string]any{"k": "12"}, "k", 0, 12},
		{"negative numeric string accepted", map[string]any{"k": "-5"}, "k", 0, -5},
		{"non-numeric string returns default", map[string]any{"k": "abc"}, "k", 7, 7},
		{"missing key returns default", map[string]any{}, "k", 99, 99},
		{"nil map returns default", nil, "k", 5, 5},
		{"bool returns default", map[string]any{"k": true}, "k", 3, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SettingInt(tt.settings, tt.key, tt.def)
			if got != tt.want {
				t.Errorf("SettingInt(%v, %q, %d) = %d, want %d",
					tt.settings, tt.key, tt.def, got, tt.want)
			}
		})
	}
}

// --- SettingFloat ---

func TestSettingFloat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		settings map[string]any
		key      SettingKey
		def      float64
		want     float64
	}{
		{"native float64", map[string]any{"k": 1.5}, "k", 0, 1.5},
		{"native int promoted", map[string]any{"k": 4}, "k", 0, 4.0},
		{"native int64 promoted", map[string]any{"k": int64(7)}, "k", 0, 7.0},
		{"numeric string", map[string]any{"k": "2.5"}, "k", 0, 2.5},
		{"non-numeric string returns default", map[string]any{"k": "abc"}, "k", 1.0, 1.0},
		{"missing key returns default", map[string]any{}, "k", 0.75, 0.75},
		{"nil map returns default", nil, "k", 0.1, 0.1},
		{"bool returns default", map[string]any{"k": true}, "k", 0.5, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SettingFloat(tt.settings, tt.key, tt.def)
			if got != tt.want {
				t.Errorf("SettingFloat(%v, %q, %v) = %v, want %v",
					tt.settings, tt.key, tt.def, got, tt.want)
			}
		})
	}
}

// --- RegisterSchema, ProviderNames, Schema ---

func TestRegisterSchema_and_Schema(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	fields := []api.ProviderSchemaField{
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
	for _, name := range []api.ProviderID{"zeta", "alpha", "mid"} {
		n := name
		r.Register(n, func(_ context.Context, _ map[string]any) (api.Provider, error) {
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

// fakeProvider implements api.Provider for testing.
type fakeProvider struct {
	name string
}

func (f *fakeProvider) Name() api.ProviderID { return api.ProviderID(f.name) }

func (f *fakeProvider) Search(_ context.Context, _ *api.SearchRequest) ([]api.Subtitle, error) {
	return nil, nil
}

func (f *fakeProvider) Download(_ context.Context, _ *api.Subtitle) ([]byte, error) {
	return nil, nil
}

func TestLoadAll_property_invariants(t *testing.T) {
	t.Parallel()

	rapid.Check(t, func(rt *rapid.T) {
		n := rapid.IntRange(1, 10).Draw(rt, "num_providers")
		r := NewRegistry()
		cfgs := make(map[api.ProviderID]api.ProviderCfg)

		var enabledNames []string
		failIdx := -1
		if rapid.Bool().Draw(rt, "has_failure") {
			failIdx = rapid.IntRange(0, n-1).Draw(rt, "fail_idx")
		}

		for i := range n {
			name := api.ProviderID(strings.Repeat("p", i+1)) // unique names: "p", "pp", "ppp"...
			state := rapid.IntRange(0, 2).Draw(rt, "state_"+string(name))
			switch state {
			case 0: // enabled
				idx := i
				r.Register(name, func(_ context.Context, _ map[string]any) (api.Provider, error) {
					if idx == failIdx {
						return nil, errors.New("factory error")
					}
					return &fakeProvider{name: string(name)}, nil
				})
				cfgs[name] = api.ProviderCfg{Enabled: true}
				enabledNames = append(enabledNames, string(name))
			case 1: // disabled
				r.Register(name, func(_ context.Context, _ map[string]any) (api.Provider, error) {
					rt.Fatalf("disabled factory called for %s", name)
					return nil, nil
				})
				cfgs[name] = api.ProviderCfg{Enabled: false}
			case 2: // unknown (not registered, but in config)
				cfgs[name] = api.ProviderCfg{Enabled: true}
			}
		}

		result, err := r.LoadAll(context.Background(), cfgs)

		// Invariant 3: if error and no successful providers, result is nil.
		// With partial success, error + non-nil result is valid when some providers loaded.
		if err != nil && len(enabledNames) == 0 {
			if result != nil {
				rt.Fatalf("LoadAll returned error AND non-nil result with no enabled providers: %v", err)
			}
			return
		}

		if result == nil {
			// Either all failed or none were enabled.
			return
		}

		// Invariant 1: output order is deterministic (sorted by name).
		for i := 1; i < len(result); i++ {
			if result[i].Name() < result[i-1].Name() {
				rt.Fatalf("LoadAll result not sorted: %q before %q",
					result[i-1].Name(), result[i].Name())
			}
		}

		// Invariant 2: all returned providers are wrapped (Name() matches inner).
		for _, p := range result {
			if !slices.Contains(enabledNames, string(p.Name())) {
				rt.Fatalf("LoadAll returned provider %q not in enabled set", p.Name())
			}
		}
	})
}
