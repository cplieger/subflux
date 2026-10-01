package confighandlers

import (
	"context"
	"slices"

	"github.com/cplieger/subflux/internal/subflux"
)

// SchemaRegistry is what the config surface asks of the provider registry: the
// registered names, each one's label and fields, and which of them can validate
// their credentials. 4 of the registry's 8 methods — instantiating providers
// (LoadAll) and the three registration calls are the composition root's job.
type SchemaRegistry interface {
	// ProviderNames returns all registered provider names in priority order.
	ProviderNames() []subflux.ProviderID
	// Schema returns the UI label and settings fields for a named provider.
	Schema(name subflux.ProviderID) (label string, fields []subflux.ProviderSchemaField)
	// CredentialCheck reports whether the provider offers a credential check.
	// One owner for the fact: the schema renders a test control from it and
	// HandleTestConnection refuses a request naming a provider it answers
	// false for.
	CredentialCheck(name subflux.ProviderID) bool
	// CheckCredentials builds the named provider from settings and reports
	// whether its credentials are accepted. A nil error means they are; a
	// *subflux.AuthError means they were refused.
	CheckCredentials(ctx context.Context, name subflux.ProviderID, settings map[string]any) error
}

// BuildProviderSchemas converts the registry's provider metadata into
// ProviderSchema entries for the UI. Names in exclude are omitted.
//
// It lived in internal/subflux, which implements no registry and consumes no
// schema; this package is its only caller, and the interface it reads through
// is declared just above.
func BuildProviderSchemas(reg SchemaRegistry, exclude ...string) []subflux.ProviderSchema {
	names := reg.ProviderNames()
	schemas := make([]subflux.ProviderSchema, 0, len(names))
	for _, name := range names {
		nameStr := string(name)
		if slices.Contains(exclude, nameStr) {
			continue
		}
		label, fields := reg.Schema(name)
		if label == "" {
			label = nameStr
		}
		ps := subflux.ProviderSchema{
			Name:     nameStr,
			Label:    label,
			ConnTest: reg.CredentialCheck(name),
		}
		for _, f := range fields {
			ps.Settings = append(ps.Settings, subflux.SchemaField{
				Key:     f.Key,
				Label:   f.Label,
				Type:    f.Type,
				Default: f.Default,
				Help:    f.Help,
				Secret:  f.Secret,
			})
		}
		schemas = append(schemas, ps)
	}
	return schemas
}
