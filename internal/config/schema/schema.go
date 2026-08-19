// Package schema generates the UI configuration schema for the web frontend.
// It reads default values from the config package and formats them for display.
package schema

import (
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
// registry by the caller.
func Sections(providerSchemas []subflux.ProviderSchema) []subflux.SchemaSection {
	return []subflux.SchemaSection{
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
			Providers: providerSchemas,
		},
		searchSection(),
		adaptiveSection(),
		postProcessSection(),
		authSection(),
		scoringSection(),
		backupSection(),
		loggingSection(),
	}
}
