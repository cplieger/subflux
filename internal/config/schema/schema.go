// Package schema generates the UI configuration schema for the web frontend.
// It reads default values from the config package and formats them for display.
package schema

import (
	"subflux/internal/api"
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

// Schema returns the full configuration schema for the UI.
// Order matches config.example.yaml for consistency.
// providerSchemas is built from the provider registry by the caller.
func Schema(providerSchemas []api.ProviderSchema) []api.SchemaSection {
	return []api.SchemaSection{
		sonarrSection(),
		radarrSection(),
		mediaRootsSection(),
		pollIntervalSection(),
		languagesSection(),
		{Key: sectionProviders, Title: "Providers", Type: sectionProviders,
			Providers: providerSchemas},
		searchSection(),
		adaptiveSection(),
		postProcessSection(),
		authSection(),
		scoringSection(),
		loggingSection(),
	}
}
