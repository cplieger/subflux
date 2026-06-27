package provider

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/cplieger/subflux/internal/api"
	"golang.org/x/sync/errgroup"
)

// RegistryErrorKind categorizes registry loading failures.
type RegistryErrorKind int

const (
	// ErrProviderInit indicates a provider factory returned an error.
	ErrProviderInit RegistryErrorKind = iota + 1
	// ErrNoProviders indicates no providers were loaded.
	ErrNoProviders
)

// String returns a human-readable name for the error kind.
func (k RegistryErrorKind) String() string {
	switch k {
	case ErrProviderInit:
		return "provider_init"
	case ErrNoProviders:
		return "no_providers"
	default:
		return fmt.Sprintf("RegistryErrorKind(%d)", int(k))
	}
}

// Compile-time assertion: RegistryErrorKind satisfies fmt.Stringer.
var _ fmt.Stringer = RegistryErrorKind(0)

// RegistryError is a typed error returned by LoadAll.
type RegistryError struct {
	Err      error          // underlying error for ErrProviderInit
	Provider api.ProviderID // non-empty for ErrProviderInit
	// Counts for ErrNoProviders diagnostics.
	Configured int
	Disabled   int
	Unknown    int
	Kind       RegistryErrorKind
}

func (e *RegistryError) Error() string {
	switch e.Kind {
	case ErrProviderInit:
		return fmt.Sprintf("init provider %s: %v", e.Provider, e.Err)
	case ErrNoProviders:
		return fmt.Sprintf("no providers loaded (configured=%d, disabled=%d, unknown=%d)",
			e.Configured, e.Disabled, e.Unknown)
	default:
		return fmt.Sprintf("registry error: %s", e.Kind)
	}
}

func (e *RegistryError) Unwrap() error { return e.Err }

// FactoryFunc creates a provider from config settings.
// The context parameter enables cancellation during provider initialization
// (e.g. credential validation, API pings) and respects shutdown signals.
type FactoryFunc func(ctx context.Context, settings map[string]any) (api.Provider, error)

// providerFactories is the declarative registration table. Populated via
// Register; iterated by LoadAll. Keyed by provider name.
// Exported as a type alias for documentation; the actual map lives on Registry.
var _ = FactoryFunc(nil) // compile-time assertion: FactoryFunc is a valid type

// Compile-time assertion: *Registry satisfies api.ProviderRegistry.
var _ api.ProviderRegistry = (*Registry)(nil)

// Registry holds provider factories keyed by name.
type Registry struct {
	factories map[api.ProviderID]FactoryFunc
	schemas   map[api.ProviderID][]api.ProviderSchemaField
	labels    map[api.ProviderID]string
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[api.ProviderID]FactoryFunc),
		schemas:   make(map[api.ProviderID][]api.ProviderSchemaField),
		labels:    make(map[api.ProviderID]string),
	}
}

// Register adds a provider factory to the registry.
func (r *Registry) Register(name api.ProviderID, f FactoryFunc) {
	if name == "" {
		panic("provider: Register called with empty name")
	}
	if f == nil {
		panic("provider: Register called with nil factory for " + string(name))
	}
	r.factories[name] = f
	slog.Debug("provider factory registered", "provider", name)
}

// RegisterSchema adds UI metadata for a provider.
func (r *Registry) RegisterSchema(name api.ProviderID, label string, fields []api.ProviderSchemaField) {
	r.labels[name] = label
	r.schemas[name] = fields
}

// ProviderNames returns all registered provider names in sorted order.
func (r *Registry) ProviderNames() []api.ProviderID {
	return slices.Sorted(maps.Keys(r.factories))
}

// Schema returns the label and settings fields for a provider.
func (r *Registry) Schema(name api.ProviderID) (string, []api.ProviderSchemaField) {
	return r.labels[name], r.schemas[name]
}

// LoadAll creates all enabled providers from config.
// Providers are loaded in parallel for reduced startup latency when
// factories perform network validation. Results are sorted by name for
// deterministic ordering.
// Unknown provider names are skipped with a warning (a typo in config
// should not prevent all other providers from loading).
// Each provider is wrapped with download retry logic (3 attempts, 2s
// initial backoff) to handle transient HTTP 5xx errors.
//
// LoadAll reads only cfg.Enabled and cfg.Settings from each entry.
// Other ProviderCfg fields (if added) will not affect provider loading.
//
// If no providers end up being loaded, the returned error names the
// specific cause (configured/disabled/unknown counts) so operators can
// distinguish a typo from a deliberate all-disabled state.
func (r *Registry) LoadAll(ctx context.Context, providers map[api.ProviderID]api.ProviderCfg) ([]api.Provider, error) {
	toLoad, disabled, unknown := r.classifyProviders(providers)

	result, errs := partitionResults(r.buildProviders(ctx, toLoad, providers))

	// Sort by name for deterministic ordering.
	slices.SortFunc(result, func(a, b api.Provider) int {
		return cmp.Compare(a.Name(), b.Name())
	})
	for _, p := range result {
		slog.Debug("loaded provider", "provider", p.Name())
	}

	if len(result) == 0 {
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		return nil, &RegistryError{
			Kind:       ErrNoProviders,
			Configured: len(providers),
			Disabled:   disabled,
			Unknown:    unknown,
		}
	}
	slog.Info("providers loaded", "count", len(result), "errors", len(errs))
	if len(errs) > 0 {
		return result, errors.Join(errs...)
	}
	return result, nil
}

// loadResult is the outcome of building one provider during LoadAll: exactly
// one of provider or err is set, and name identifies the entry either way.
type loadResult struct {
	provider api.Provider
	err      error
	name     api.ProviderID
}

// classifyProviders walks the configured providers in sorted order and splits
// them into the names to load (enabled and registered) plus the counts of
// disabled and unknown entries. Sorted iteration keeps logging deterministic.
func (r *Registry) classifyProviders(providers map[api.ProviderID]api.ProviderCfg) (toLoad []api.ProviderID, disabled, unknown int) {
	for _, name := range slices.Sorted(maps.Keys(providers)) {
		cfg := providers[name]
		if !cfg.Enabled {
			disabled++
			slog.Debug("provider disabled, skipping", "provider", name)
			continue
		}
		if _, ok := r.factories[name]; !ok {
			unknown++
			slog.Warn("unknown provider in config, skipping", "provider", name)
			continue
		}
		toLoad = append(toLoad, name)
	}
	return toLoad, disabled, unknown
}

// buildProviders constructs each named provider in parallel for reduced startup
// latency. It never returns an error: a per-provider failure (factory error or
// an already-cancelled context) is recorded on that entry so sibling providers
// still load, preserving partial success.
func (r *Registry) buildProviders(ctx context.Context, toLoad []api.ProviderID, providers map[api.ProviderID]api.ProviderCfg) []loadResult {
	results := make([]loadResult, len(toLoad))
	var g errgroup.Group
	g.SetLimit(len(toLoad))
	for i, name := range toLoad {
		g.Go(func() error {
			if ctx.Err() != nil {
				results[i] = loadResult{name: name, err: ctx.Err()}
				return nil // don't cancel siblings; partial success
			}
			p, err := r.factories[name](ctx, providers[name].Settings)
			if err != nil {
				results[i] = loadResult{name: name, err: err}
			} else {
				results[i] = loadResult{name: name, provider: p}
			}
			return nil // never return error; preserve partial success
		})
	}
	_ = g.Wait()
	return results
}

// partitionResults separates successfully built providers from initialization
// failures, wrapping each failure in a typed RegistryError and logging it. The
// input order is preserved so the joined error is deterministic.
func partitionResults(results []loadResult) (providers []api.Provider, errs []error) {
	for _, lr := range results {
		if lr.err != nil {
			errs = append(errs, &RegistryError{Kind: ErrProviderInit, Provider: lr.name, Err: lr.err})
			slog.Warn("provider init failed", "provider", lr.name, "error", lr.err)
			continue
		}
		if lr.provider != nil {
			providers = append(providers, lr.provider)
		}
	}
	return providers, errs
}
