package server

import (
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config"
)

// driftState is the drift-relevant projection of one config: the values whose
// removal or disabling invalidates stored search attempts.
type driftState struct {
	// Languages are the configured subtitle language codes.
	Languages []string
	// Providers are the enabled provider IDs.
	Providers []api.ProviderID
	// AdaptiveEnabled reports whether adaptive search backoff is on.
	AdaptiveEnabled bool
}

// driftInputs pairs the outgoing config's state with the incoming one.
//
// The two are named rather than positional because they are the same type and
// drift is directional: reading them the wrong way round reports the arriving
// languages and providers as the departing ones, and the cleanup that follows
// clears the search attempts of everything the operator just configured while
// leaving the removed entries' attempts behind. Nothing downstream can detect
// that inversion — both shapes are well-formed api.ConfigDrift values.
type driftInputs struct {
	// Old is the state of the config being replaced.
	Old driftState
	// New is the state of the config replacing it.
	New driftState
}

// detectDrift compares the old and new config state to determine what DB
// cleanup is needed. Duplicate entries in the old state are deduplicated;
// each removed item appears at most once in the result.
func detectDrift(in *driftInputs) api.ConfigDrift {
	return api.ConfigDrift{
		RemovedLanguages: removedItems(in.Old.Languages, in.New.Languages),
		RemovedProviders: removedProviderItems(in.Old.Providers, in.New.Providers),
		AdaptiveDisabled: in.Old.AdaptiveEnabled && !in.New.AdaptiveEnabled,
	}
}

// removedItems returns items in old that are not in current, deduplicated.
func removedItems(old, current []string) []string {
	currentSet := toSet(current)
	var removed []string
	for _, item := range uniqueStrings(old) {
		if _, ok := currentSet[item]; !ok {
			removed = append(removed, item)
		}
	}
	return removed
}

// removedProviderItems returns provider IDs in old that are not in current, deduplicated.
func removedProviderItems(old, current []api.ProviderID) []api.ProviderID {
	currentSet := make(map[api.ProviderID]struct{}, len(current))
	for _, item := range current {
		currentSet[item] = struct{}{}
	}
	seen := make(map[api.ProviderID]struct{}, len(old))
	var removed []api.ProviderID
	for _, item := range old {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		if _, ok := currentSet[item]; !ok {
			removed = append(removed, item)
		}
	}
	return removed
}

// toSet returns a set (map[string]struct{}) from a string slice.
func toSet(items []string) map[string]struct{} {
	s := make(map[string]struct{}, len(items))
	for _, item := range items {
		s[item] = struct{}{}
	}
	return s
}

// uniqueStrings returns items with duplicates removed, preserving order.
func uniqueStrings(items []string) []string {
	seen := make(map[string]struct{}, len(items))
	out := make([]string, 0, len(items))
	for _, item := range items {
		if _, ok := seen[item]; !ok {
			seen[item] = struct{}{}
			out = append(out, item)
		}
	}
	return out
}

// newDriftState projects the drift-relevant values out of a config, so the two
// sides of a comparison are built by one function and cannot disagree on which
// value goes where.
func newDriftState(cfg *config.Config) driftState {
	return driftState{
		Languages:       cfg.LanguageCodes(),
		Providers:       enabledProviders(cfg.Providers()),
		AdaptiveEnabled: cfg.Adaptive().Enabled,
	}
}
