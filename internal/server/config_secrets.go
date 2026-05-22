package server

import (
	"slices"

	"subflux/internal/api"
	"subflux/internal/server/confighandlers"
)

// SecretKeyNames returns the list of YAML keys treated as secrets.
// Delegates to confighandlers.SecretKeyNames.
func SecretKeyNames() []string { return confighandlers.SecretKeyNames() }

// redactSecrets replaces secret values in YAML config with a placeholder.
// Delegates to confighandlers.RedactSecrets.
func redactSecrets(data []byte) []byte { return confighandlers.RedactSecrets(data) }

// mergeSecrets fills empty secret values in newData from the existing config file.
// Delegates to confighandlers.MergeSecrets.
func mergeSecrets(newData []byte, configPath string) []byte {
	return confighandlers.MergeSecrets(newData, configPath)
}

// enabledProviders returns the sorted names of enabled providers in a config.
func enabledProviders(cfg interface {
	ProviderConfigs() map[api.ProviderID]api.ProviderCfg
}) []api.ProviderID {
	var names []api.ProviderID
	for name, pcfg := range cfg.ProviderConfigs() {
		if pcfg.Enabled {
			names = append(names, name)
		}
	}
	slices.SortFunc(names, func(a, b api.ProviderID) int {
		if a < b {
			return -1
		}
		if a > b {
			return 1
		}
		return 0
	})
	return names
}

// Test-support wrappers for functions moved to confighandlers.
// These are unexported and only accessible from _test.go files in this package.
func stripYAMLComment(val []byte) []byte      { return confighandlers.StripYAMLComment(val) }
func isRedactedPlaceholder(val []byte) bool   { return confighandlers.IsRedactedPlaceholder(val) }
func findClosingQuote(val []byte, q byte) int { return confighandlers.FindClosingQuote(val, q) }
func secretContextKey(lines [][]byte, lineIdx int, key string) string {
	return confighandlers.SecretContextKey(lines, lineIdx, key)
}
func extractSecretValues(data []byte) map[string]string {
	return confighandlers.ExtractSecretValues(data)
}
