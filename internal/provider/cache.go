package provider

import (
	"github.com/cplieger/subflux/internal/api"
)

// CacheClearer is declared here, at the consumer, rather than in a shared
// contract package: this package is the only one that asks the question. One
// method, because clearing is all it asks — the providers that hold a download
// cache (season-pack zips) offer it, the eight that hold nothing do not, and
// ClearCaches below decides per provider at runtime.
//
// EXPORTED against the unexported-by-default rule, and the reason is a real
// guard rather than convenience: a provider opts in from its own package with
// `var _ provider.CacheClearer = (*Provider)(nil)`, and that assertion is the
// only compile-time check on the opt-in. The dispatch here is a type
// assertion, so a renamed or dropped ClearCache would leave a provider
// silently never freeing its cache, with nothing red anywhere.
type CacheClearer interface {
	ClearCache()
}

// ClearCaches calls ClearCache on any provider that implements
// CacheClearer. Typically called at scan completion to free memory.
func ClearCaches(providers []api.Provider) {
	for _, p := range providers {
		if cc, ok := p.(CacheClearer); ok {
			cc.ClearCache()
		}
	}
}

// ResolveShowCounter finds the first provider implementing ShowSubtitleCounter.
// Called at the composition root to inject the resolved counter into LiveState.
func ResolveShowCounter(providers []api.Provider) api.ShowSubtitleCounter {
	for _, p := range providers {
		if c, ok := p.(api.ShowSubtitleCounter); ok {
			return c
		}
	}
	return nil
}
