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

		// Invariant 2: every returned provider corresponds to an enabled entry
		// (LoadAll never invents or returns disabled/unknown providers).
		for _, p := range result {
			if !slices.Contains(enabledNames, string(p.Name())) {
				rt.Fatalf("LoadAll returned provider %q not in enabled set", p.Name())
			}
		}
	})
}
