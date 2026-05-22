package provider

import (
	"subflux/internal/api"
)

// ClearProviderCaches calls ClearCache on any provider that implements
// api.CacheClearer. Typically called at scan completion to free memory.
func ClearProviderCaches(providers []api.Provider) {
	for _, p := range providers {
		if cc, ok := p.(api.CacheClearer); ok {
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
