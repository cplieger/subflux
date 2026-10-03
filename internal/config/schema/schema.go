// Package schema generates the UI configuration schema for the web frontend.
// It reads default values from the config package and formats them for display.
package schema

import (
	"slices"

	"github.com/cplieger/subflux/internal/subflux"
)

// Schema field type constants used across section builders.
const (
	fieldText     = "text"
	fieldNumber   = "number"
	fieldBool     = "bool"
	fieldDuration = "duration"
	fieldSecret   = "secret"
	fieldSelect   = "select"
	fieldFields   = "fields"
	fieldNested   = "nested"

	// Section-level constants.
	fieldList        = "list"
	fieldLanguages   = "languages"
	sectionProviders = "providers"
	groupArr         = "arr"
	keyEnabled       = "enabled"
	keySonarr        = "sonarr"
	keyPollInterval  = "poll_interval"
	keyLanguages     = "languages"
	defaultTrue      = "true"
	placeholderMedia = "/media"
)

// Sections returns the full configuration schema for the UI, in the order
// config.example.yaml uses. providerSchemas is built from the provider
// registry by the caller. Every field of type secret comes back with Secret
// set, at any depth, because redaction and the save-side merge key on the flag.
func Sections(providerSchemas []subflux.ProviderSchema) []subflux.SchemaSection {
	providers := slices.Clone(providerSchemas)
	for i := range providers {
		providers[i].Settings = withSecretFlags(providers[i].Settings)
	}
	sections := []subflux.SchemaSection{
		sonarrSection(),
		radarrSection(),
		mediaRootsSection(),
		trustedProxiesSection(),
		allowedHostsSection(),
		pollIntervalSection(),
		languagesSection(),
		embeddedSection(),
		{
			Key: sectionProviders, Title: "Providers", Type: sectionProviders,
			Providers: providers,
		},
		searchSection(),
		adaptiveSection(),
		postProcessSection(),
		authSection(),
		scoringSection(),
		backupSection(),
		loggingSection(),
	}
	for i := range sections {
		sections[i].Fields = withSecretFlags(sections[i].Fields)
	}
	return sections
}

// withSecretFlags copies at every level because provider settings are the
// registry's own slices, which building a schema must not mutate.
func withSecretFlags(fields []subflux.SchemaField) []subflux.SchemaField {
	out := slices.Clone(fields)
	for i := range out {
		out[i].Secret = out[i].Secret || out[i].Type == fieldSecret
		out[i].Fields = withSecretFlags(out[i].Fields)
	}
	return out
}
