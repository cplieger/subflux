package provider

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"

	"github.com/cplieger/subflux/internal/subflux"
	"golang.org/x/sync/errgroup"
)

// RegistryErrorKind categorizes registry loading failures.
type RegistryErrorKind int

// ErrProviderInit indicates a provider factory returned an error.
const ErrProviderInit RegistryErrorKind = iota + 1

// String returns a human-readable name for the error kind.
func (k RegistryErrorKind) String() string {
	if k == ErrProviderInit {
		return "provider_init"
	}
	return fmt.Sprintf("RegistryErrorKind(%d)", int(k))
}

// Compile-time assertion: RegistryErrorKind satisfies fmt.Stringer.
var _ fmt.Stringer = RegistryErrorKind(0)

// RegistryError is a typed error returned by LoadAll.
type RegistryError struct {
	Err      error              // underlying error for ErrProviderInit
	Provider subflux.ProviderID // non-empty for ErrProviderInit
	Kind     RegistryErrorKind
}

func (e *RegistryError) Error() string {
	if e.Kind == ErrProviderInit {
		return fmt.Sprintf("init provider %s: %v", e.Provider, e.Err)
	}
	return fmt.Sprintf("registry error: %s", e.Kind)
}

func (e *RegistryError) Unwrap() error { return e.Err }

// Provider is what a subtitle source must offer to be registered here: the name
// config and logs identify it by, a search over one media item, and a download
// of one result. THREE methods, against a denominator of five — the exported
// methods the nine implementations offer between them. Those three are the
// intersection: every implementation has all three, and the two that are not
// universal (CountShowSubtitles on opensubtitles, ClearCache on hdbits) are
// declared as their own one-method interfaces in cache.go and discovered by type
// assertion. Nothing in this contract is optional, which is why the optional
// pair is not in it.
//
// Declared in this package because this package is the consumer: FactoryFunc
// below returns one, Registry.LoadAll builds them, and WrapRetry wraps one.
// EXPORTED against the unexported-by-default rule because the nine Factory
// functions in the subpackages name it in their return type, and providers.go
// types them as FactoryFunc — the name has to cross the package boundary for the
// registration table to exist at all.
//
// Returning an interface from those factories is the stdlib registry idiom, not
// a lapse from "return structs": image.RegisterFormat takes constructors of a
// uniform signature returning image.Image, and image/png exports Decode with no
// concrete alternative (ratified as C14). This is also the only interface in
// subflux with real polymorphism — nine implementations plus the retry wrapper,
// where every other one has exactly one.
type Provider interface {
	// Name returns the provider identifier (e.g. "opensubtitles", "yifysubtitles").
	Name() subflux.ProviderID

	// Search finds subtitles matching the request.
	Search(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error)

	// Download fetches the subtitle content for the given search result.
	//
	// A subtitle the upstream no longer holds is reported as an error
	// wrapping subflux.ErrSubtitleAbsent, never as a successful download of zero
	// bytes: absence and a truncated or corrupt payload are different
	// operator problems, and returning nil bytes with a nil error made them
	// read the same. Implementations therefore never return (nil, nil) —
	// every return either carries subtitle content or names why it does not.
	Download(ctx context.Context, sub *subflux.Subtitle) ([]byte, error)
}

// FactoryFunc creates a provider from config settings.
// The context parameter enables cancellation during provider initialization
// (e.g. credential validation, API pings) and respects shutdown signals.
type FactoryFunc func(ctx context.Context, settings map[string]any) (Provider, error)

// Registry holds provider factories keyed by name.
type Registry struct {
	factories map[subflux.ProviderID]FactoryFunc
	schemas   map[subflux.ProviderID][]subflux.ProviderSchemaField
	labels    map[subflux.ProviderID]string
}

// NewRegistry creates an empty provider registry.
func NewRegistry() *Registry {
	return &Registry{
		factories: make(map[subflux.ProviderID]FactoryFunc),
		schemas:   make(map[subflux.ProviderID][]subflux.ProviderSchemaField),
		labels:    make(map[subflux.ProviderID]string),
	}
}

// Register adds a provider factory to the registry.
func (r *Registry) Register(name subflux.ProviderID, f FactoryFunc) {
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
func (r *Registry) RegisterSchema(name subflux.ProviderID, label string, fields []subflux.ProviderSchemaField) {
	r.labels[name] = label
	r.schemas[name] = fields
}

// ProviderNames returns all registered provider names in sorted order.
func (r *Registry) ProviderNames() []subflux.ProviderID {
	return slices.Sorted(maps.Keys(r.factories))
}

// Schema returns the label and settings fields for a provider.
//
// The fields are cloned: the registry is built once at boot and read by the
// settings-UI handler on every request, so a caller sorting or rewriting the
// returned slice in place would be editing what every later request renders.
// One level of clone is the whole copy — ProviderSchemaField holds five strings
// and a bool and no reference type, so the elements share nothing further.
func (r *Registry) Schema(name subflux.ProviderID) (string, []subflux.ProviderSchemaField) {
	return r.labels[name], slices.Clone(r.schemas[name])
}

// LoadAll creates all enabled providers from config.
// Providers are loaded in parallel for reduced startup latency when
// factories perform network validation. Results are sorted by name for
// deterministic ordering.
// Unknown provider names are skipped with a warning (a typo in config
// should not prevent all other providers from loading).
// Providers are returned unwrapped; download retry wrapping (WrapRetryAll)
// is applied by the composition root (main.go wiring).
//
// LoadAll reads only cfg.Enabled and cfg.Settings from each entry.
// Other ProviderCfg fields (if added) will not affect provider loading.
//
// Zero providers loading is NOT an error: a config with no enabled
// acquisition providers is a valid "embedded detection and coverage only"
// setup. The counts are WARN-logged so operators can still distinguish a
// typo (unknown>0) from a deliberate all-disabled state.
func (r *Registry) LoadAll(ctx context.Context, providers map[subflux.ProviderID]subflux.ProviderCfg) ([]Provider, error) {
	toLoad, disabled, unknown := r.classifyProviders(providers)

	result, errs := partitionResults(r.buildProviders(ctx, toLoad, providers))

	// Sort by name for deterministic ordering.
	slices.SortFunc(result, func(a, b Provider) int {
		return cmp.Compare(a.Name(), b.Name())
	})
	for _, p := range result {
		slog.Debug("loaded provider", "provider", p.Name())
	}

	if len(result) == 0 {
		if len(errs) > 0 {
			return nil, errors.Join(errs...)
		}
		slog.Warn("no acquisition providers loaded; embedded detection and coverage only",
			"configured", len(providers), "disabled", disabled, "unknown", unknown)
		return nil, nil
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
	provider Provider
	err      error
	name     subflux.ProviderID
}

// classifyProviders walks the configured providers in sorted order and splits
// them into the names to load (enabled and registered) plus the counts of
// disabled and unknown entries. Sorted iteration keeps logging deterministic.
func (r *Registry) classifyProviders(providers map[subflux.ProviderID]subflux.ProviderCfg) (toLoad []subflux.ProviderID, disabled, unknown int) {
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
func (r *Registry) buildProviders(ctx context.Context, toLoad []subflux.ProviderID, providers map[subflux.ProviderID]subflux.ProviderCfg) []loadResult {
	results := make([]loadResult, len(toLoad))
	var g errgroup.Group
	g.SetLimit(len(toLoad))
	for i, name := range toLoad {
		g.Go(func() error {
			if ctx.Err() != nil {
				results[i] = loadResult{name: name, err: ctx.Err()}
				return nil // don't cancel siblings; partial success
			}
			// The registered schema declaration is the single source of
			// setting defaults (P14): absent declared fields are filled from
			// their schema Default before the factory ever sees the map.
			settings := NormalizeSettings(r.schemas[name], providers[name].Settings)
			p, err := r.factories[name](ctx, settings)
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
func partitionResults(results []loadResult) (providers []Provider, errs []error) {
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
