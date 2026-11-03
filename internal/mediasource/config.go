package mediasource

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
	"go.yaml.in/yaml/v3"
)

// The envelope keys every entry carries; any other key is the adapter's.
const (
	keyID           = "id"
	keyType         = "type"
	keyEnabled      = "enabled"
	keyPathMappings = "path_mappings"
	keyExcludeTags  = "exclude_tags"
)

// ParseConfigs reads the sources section, n being its node or
// nil when absent, and reports every refusal at once at its absolute path.
// Adapter keys go to the kind's Schema.Decode and the whole entry to the
// kind's New with a zero Deps. An entry with enabled: false is validated
// and left out of the result. Every envelope scalar is marked plain. The
// settings loader refuses anchors, aliases and tags before any section
// parses, so n holds none.
func ParseConfigs(n *yaml.Node, kinds []AdapterKind) ([]Config, []formschema.FieldError) {
	if n == nil || formschema.IsNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, []formschema.FieldError{{Path: "sources", Message: "must be a list of source entries"}}
	}
	var errs []formschema.FieldError
	if len(n.Content) > maxSources {
		errs = append(errs, formschema.FieldError{Path: "sources", Message: fmt.Sprintf("must list at most %d sources", maxSources)})
	}
	var out []Config
	first := map[media.SourceID]int{}
	for i, e := range n.Content {
		cfg, enabled, eerrs := parseEntry(e, kinds)
		if j, dup := first[cfg.ID]; dup && cfg.ID != "" {
			eerrs = append(eerrs, formschema.FieldError{Path: keyID, Message: fmt.Sprintf("repeats the id of sources[%d]", j)})
		} else if cfg.ID != "" {
			first[cfg.ID] = i
		}
		errs = append(errs, formschema.Prefix(fmt.Sprintf("sources[%d]", i), eerrs)...)
		if enabled && len(eerrs) == 0 {
			cfg.Index = i
			out = append(out, cfg)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

// parseEntry reads one entry, its errors relative to it.
func parseEntry(e *yaml.Node, kinds []AdapterKind) (Config, bool, []formschema.FieldError) {
	if e.Kind != yaml.MappingNode {
		return Config{}, false, []formschema.FieldError{{Message: "must be a mapping"}}
	}
	var cfg Config
	enabled := true
	envelope := map[string]*yaml.Node{}
	adapter := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(e.Content); i += 2 {
		k, v := e.Content[i], e.Content[i+1]
		switch k.Value {
		case keyID, keyType, keyEnabled, keyPathMappings, keyExcludeTags:
			envelope[k.Value] = v
			formschema.MarkPlain(v)
		default:
			adapter.Content = append(adapter.Content, k, v)
		}
	}
	var errs []formschema.FieldError
	var err *formschema.FieldError
	if cfg.ID, err = parseID(envelope[keyID]); err != nil {
		errs = append(errs, *err)
	}
	kind, err := parseType(envelope[keyType], kinds)
	if err != nil {
		errs = append(errs, *err)
	}
	if v := envelope[keyEnabled]; v != nil && !formschema.IsNull(v) {
		b, ok := formschema.ParseBool(v.Value)
		if v.Kind != yaml.ScalarNode || !ok {
			errs = append(errs, formschema.FieldError{Path: keyEnabled, Message: "must be true or false"})
		}
		enabled = b
	}
	var merrs, terrs []formschema.FieldError
	cfg.PathMappings, merrs = parseMappings(envelope[keyPathMappings])
	cfg.ExcludeTags, terrs = parseExcludeTags(envelope[keyExcludeTags])
	errs = append(append(errs, merrs...), terrs...)
	if kind == nil {
		return cfg, enabled, errs
	}
	cfg.Type = kind.Type
	vals, verrs := kind.Schema.Decode(adapter)
	errs = append(errs, verrs...)
	cfg.Values = vals
	if len(errs) == 0 {
		if _, err := kind.New(cfg, Deps{}); err != nil {
			errs = append(errs, formschema.FieldError{Message: err.Error()})
		}
	}
	return cfg, enabled, errs
}

const (
	msgRequired = "required"
	msgScalar   = "must be a single value"
)

func parseID(v *yaml.Node) (media.SourceID, *formschema.FieldError) {
	if v == nil || formschema.IsNull(v) {
		return "", &formschema.FieldError{Path: keyID, Message: msgRequired}
	}
	if v.Kind != yaml.ScalarNode {
		return "", &formschema.FieldError{Path: keyID, Message: msgScalar}
	}
	var id media.SourceID
	if err := id.UnmarshalText([]byte(v.Value)); err != nil {
		return "", &formschema.FieldError{Path: keyID, Message: err.Error()}
	}
	return id, nil
}

func parseType(v *yaml.Node, kinds []AdapterKind) (*AdapterKind, *formschema.FieldError) {
	if v == nil || formschema.IsNull(v) {
		return nil, &formschema.FieldError{Path: keyType, Message: msgRequired}
	}
	if v.Kind == yaml.ScalarNode {
		if i := slices.IndexFunc(kinds, func(k AdapterKind) bool { return k.Type == v.Value }); i >= 0 {
			return &kinds[i], nil
		}
	}
	known := make([]string, len(kinds))
	for i, k := range kinds {
		known[i] = k.Type
	}
	slices.Sort(known)
	return nil, &formschema.FieldError{Path: keyType, Message: "must be one of " + strings.Join(known, ", ")}
}

func parseMappings(v *yaml.Node) ([]PathMapping, []formschema.FieldError) {
	if v == nil || formschema.IsNull(v) {
		return nil, nil
	}
	if v.Kind != yaml.SequenceNode {
		return nil, []formschema.FieldError{{Path: keyPathMappings, Message: "must be a list of {from, to} mappings"}}
	}
	if len(v.Content) > maxMappings {
		return nil, []formschema.FieldError{{Path: keyPathMappings, Message: fmt.Sprintf("must list at most %d mappings", maxMappings)}}
	}
	var errs []formschema.FieldError
	out := make([]PathMapping, 0, len(v.Content))
	froms := map[string]int{}
	for j, m := range v.Content {
		at := fmt.Sprintf("%s[%d]", keyPathMappings, j)
		pm, merrs := parseMapping(m, at)
		if len(merrs) > 0 {
			errs = append(errs, merrs...)
			continue
		}
		if k, dup := froms[pm.From]; dup {
			errs = append(errs, formschema.FieldError{Path: at + ".from", Message: fmt.Sprintf("repeats the from of %s[%d]", keyPathMappings, k)})
			continue
		}
		froms[pm.From] = j
		out = append(out, pm)
	}
	if len(errs) > 0 {
		return nil, errs
	}
	if j := injective(out); j >= 0 {
		return nil, []formschema.FieldError{{Path: fmt.Sprintf("%s[%d].to", keyPathMappings, j), Message: "overlaps an earlier mapping's to without nesting exactly under it, so two source paths would map to one local path"}}
	}
	return out, nil
}

func parseMapping(m *yaml.Node, at string) (PathMapping, []formschema.FieldError) {
	if m.Kind != yaml.MappingNode {
		return PathMapping{}, []formschema.FieldError{{Path: at, Message: "must be a {from, to} mapping"}}
	}
	vals := map[string]string{}
	var errs []formschema.FieldError
	for i := 0; i+1 < len(m.Content); i += 2 {
		k, v := m.Content[i].Value, m.Content[i+1]
		switch {
		case k != "from" && k != "to":
			errs = append(errs, formschema.FieldError{Path: at + "." + k, Message: "unknown key"})
		case v.Kind != yaml.ScalarNode || formschema.IsNull(v):
			errs = append(errs, formschema.FieldError{Path: at + "." + k, Message: msgScalar})
		case !validPath(v.Value):
			errs = append(errs, formschema.FieldError{Path: at + "." + k, Message: "must be an absolute, clean, single-line path of at most 4096 bytes"})
		default:
			vals[k] = v.Value
		}
	}
	for _, k := range []string{"from", "to"} {
		if _, ok := vals[k]; !ok && !slices.ContainsFunc(errs, func(e formschema.FieldError) bool { return e.Path == at+"."+k }) {
			errs = append(errs, formschema.FieldError{Path: at + "." + k, Message: msgRequired})
		}
	}
	return PathMapping{From: vals["from"], To: vals["to"]}, errs
}

// parseExcludeTags reads exclude_tags: absent or null is the default label,
// [] none; labels are kept trimmed and lower-cased, arrapi's match rule.
func parseExcludeTags(v *yaml.Node) ([]string, []formschema.FieldError) {
	if v == nil || formschema.IsNull(v) {
		return []string{defaultExcludeTag}, nil
	}
	if v.Kind != yaml.SequenceNode {
		return nil, []formschema.FieldError{{Path: keyExcludeTags, Message: "must be a list of tag labels"}}
	}
	if len(v.Content) > maxExcludeTags {
		return nil, []formschema.FieldError{{Path: keyExcludeTags, Message: fmt.Sprintf("must list at most %d labels", maxExcludeTags)}}
	}
	var errs []formschema.FieldError
	out := make([]string, 0, len(v.Content))
	for j, e := range v.Content {
		at := fmt.Sprintf("%s[%d]", keyExcludeTags, j)
		l := normLabel(e.Value)
		switch {
		case e.Kind != yaml.ScalarNode || formschema.IsNull(e):
			errs = append(errs, formschema.FieldError{Path: at, Message: msgScalar})
		case l == "" || len(strings.TrimSpace(e.Value)) > maxTagLabel:
			errs = append(errs, formschema.FieldError{Path: at, Message: fmt.Sprintf("must be 1 to %d characters", maxTagLabel)})
		case slices.Contains(out, l):
			errs = append(errs, formschema.FieldError{Path: at, Message: "repeats another label, ignoring case"})
		default:
			out = append(out, l)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}
