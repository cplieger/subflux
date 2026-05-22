package api

// DetectDrift compares old and new config state to determine what DB
// cleanup is needed. Parameters are the extracted sets from each config.
// Duplicate entries in old slices are deduplicated; each removed item
// appears at most once in the result.
func DetectDrift(
	oldLangs, newLangs []string,
	oldProviders, newProviders []ProviderID,
	oldAdaptiveEnabled, newAdaptiveEnabled bool,
) ConfigDrift {
	return ConfigDrift{
		RemovedLanguages: removedItems(oldLangs, newLangs),
		RemovedProviders: removedProviderItems(oldProviders, newProviders),
		AdaptiveDisabled: oldAdaptiveEnabled && !newAdaptiveEnabled,
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
func removedProviderItems(old, current []ProviderID) []ProviderID {
	currentSet := make(map[ProviderID]struct{}, len(current))
	for _, item := range current {
		currentSet[item] = struct{}{}
	}
	seen := make(map[ProviderID]struct{}, len(old))
	var removed []ProviderID
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

// Empty returns true if no cleanup is needed.
func (d *ConfigDrift) Empty() bool {
	return len(d.RemovedLanguages) == 0 &&
		len(d.RemovedProviders) == 0 &&
		!d.AdaptiveDisabled
}
