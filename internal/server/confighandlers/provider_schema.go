package confighandlers

import (
	"slices"

	"github.com/cplieger/subflux/internal/subflux"
)

// SchemaRegistry is the provider metadata the settings UI is built from: the
// registered names, and each one's label and fields. 2 of the registry's 3
// methods — instantiating providers (LoadAll) is the composition root's job and
// no HTTP handler does it.
type SchemaRegistry interface {
	// ProviderNames returns all registered provider names in priority order.
	ProviderNames() []subflux.ProviderID
	// Schema returns the UI label and settings fields for a named provider.
	Schema(name subflux.ProviderID) (label string, fields []subflux.ProviderSchemaField)
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
			Name:  nameStr,
			Label: label,
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
