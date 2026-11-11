package provider

import (
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v4"
)

// The providers section maps each provider id to its entry; the entry's
// adapter keys sit under settings, beside the envelope's own.
const (
	sectionKey  = "providers"
	keyEnabled  = "enabled"
	keyPriority = "priority"
	keySettings = "settings"
)

// MaxPriority bounds Config.Priority.
const MaxPriority = 100

// envelopeSchema is the keys every providers entry carries beside settings,
// whose keys are its Entry's Schema.
var envelopeSchema = formschema.Schema{Fields: []formschema.Field{
	{
		Key: keyEnabled, Label: "Enabled", Kind: formschema.Bool, Default: "true",
		Help: "Search this site. A listed site is on unless this is false.",
	},
	{
		Key: keyPriority, Label: "Priority", Kind: formschema.Int, Min: 0, Max: MaxPriority, Default: "0",
		Help: "Breaks a tie between equally ranked subtitles from two sites. Lower is more trusted.",
	},
}}

// ParseConfigs reads the providers section, n being its node or nil when
// absent, and reports every refusal at once at its absolute path: a key no
// entry has as its ID, a key set twice, an envelope or settings value its
// schema refuses, and an enabled entry Build refuses, so an adapter's New
// error is a field error before any commit. A disabled entry keeps its
// values with no Required key enforced and is never built. Every scalar a
// schema reads is marked plain except a Secret's. The configs are in file
// order, one per listed entry, disabled ones included.
func ParseConfigs(n *yaml.Node, entries []Entry) ([]Config, []formschema.FieldError) {
	if n == nil || formschema.IsNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, []formschema.FieldError{{Path: sectionKey, Message: "must be a mapping from provider id to its settings"}}
	}
	var out []Config
	var errs []formschema.FieldError
	seen := map[ProviderID]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode {
			errs = append(errs, formschema.FieldError{Path: sectionKey, Message: "keys must be provider ids"})
			continue
		}
		path := sectionKey + "." + k.Value
		j := slices.IndexFunc(entries, func(e Entry) bool { return string(e.ID) == k.Value })
		switch {
		case j < 0:
			errs = append(errs, formschema.FieldError{Path: path, Message: "unknown provider; must be one of " + knownIDs(entries)})
			continue
		case seen[entries[j].ID]:
			errs = append(errs, formschema.FieldError{Path: path, Message: "key is set twice"})
			continue
		}
		seen[entries[j].ID] = true
		cfg, eerrs := parseEntry(v, &entries[j])
		if len(eerrs) > 0 {
			errs = append(errs, formschema.Prefix(path, eerrs)...)
			continue
		}
		out = append(out, cfg)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// parseEntry reads one entry, its errors relative to it; a null entry is
// one with every default.
func parseEntry(v *yaml.Node, e *Entry) (Config, []formschema.FieldError) {
	if formschema.IsNull(v) {
		v = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	if v.Kind != yaml.MappingNode {
		return Config{}, []formschema.FieldError{{Message: "must be a mapping"}}
	}
	envelope, settings, twice := splitEntry(v)
	env, errs := envelopeSchema.Decode(envelope)
	if twice {
		errs = append(errs, formschema.FieldError{Path: keySettings, Message: "key is set twice"})
	}
	cfg := Config{ID: e.ID, Enabled: env.Bool(keyEnabled), Priority: int(env.Int(keyPriority))}
	schema := e.Schema
	if !cfg.Enabled {
		schema = optional(schema)
	}
	var serrs []formschema.FieldError
	cfg.Values, serrs = schema.Decode(settings)
	errs = append(errs, formschema.Prefix(keySettings, serrs)...)
	if len(errs) == 0 && cfg.Enabled {
		if _, err := Build(*e, cfg); err != nil {
			errs = append(errs, formschema.FieldError{Path: keySettings, Message: err.Error()})
		}
	}
	return cfg, errs
}

// splitEntry parts an entry into its envelope keys, as a mapping for
// envelopeSchema's Decode, which refuses any other key, and its settings
// node, nil when absent; twice reports a second settings key.
func splitEntry(v *yaml.Node) (envelope, settings *yaml.Node, twice bool) {
	envelope = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(v.Content); i += 2 {
		k, val := v.Content[i], v.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.Value != keySettings {
			envelope.Content = append(envelope.Content, k, val)
			continue
		}
		if settings != nil {
			twice = true
			continue
		}
		settings = val
	}
	return envelope, settings, twice
}

// optional is s with no field Required: a disabled entry keeps what it holds.
func optional(s formschema.Schema) formschema.Schema {
	fields := slices.Clone(s.Fields)
	for i := range fields {
		fields[i].Required = false
	}
	return formschema.Schema{Fields: fields}
}

func knownIDs(entries []Entry) string {
	ids := make([]string, len(entries))
	for i := range entries {
		ids[i] = string(entries[i].ID)
	}
	slices.Sort(ids)
	return strings.Join(ids, ", ")
}
