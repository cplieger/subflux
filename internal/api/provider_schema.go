package api

import "slices"

// BuildProviderSchemas converts the registry's provider metadata into
// ProviderSchema entries for the UI. Names in exclude are omitted.
func BuildProviderSchemas(reg ProviderRegistry, exclude ...string) []ProviderSchema {
	names := reg.ProviderNames()
	schemas := make([]ProviderSchema, 0, len(names))
	for _, name := range names {
		nameStr := string(name)
		if slices.Contains(exclude, nameStr) {
			continue
		}
		label, fields := reg.Schema(name)
		if label == "" {
			label = nameStr
		}
		ps := ProviderSchema{
			Name:          nameStr,
			Label:         label,
			AlwaysEnabled: name == ProviderNameEmbedded,
		}
		for _, f := range fields {
			ps.Settings = append(ps.Settings, SchemaField{
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
