package mediaserver

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediasource"
	"go.yaml.in/yaml/v4"
)

// Adapter answers for one configured server. New makes no request; every
// method is safe for concurrent use.
type Adapter interface {
	// Refresh asks the server to rescan every item holding the file at the
	// server-side path; a path no item holds is a success.
	Refresh(ctx context.Context, serverPath string) error
	// Labels answers how the server labels the subtitles beside the file at
	// the server-side path, that file's own only; a path no item holds
	// answers none.
	Labels(ctx context.Context, serverPath string) ([]Label, error)
	// Ping is one authenticated read proving the URL and credential.
	Ping(context.Context) error
	// Shutdown cancels the adapter's calls and background work, refusing any
	// later call, and returns once they have ended or ctx is done.
	Shutdown(context.Context) error
}

// AdapterKind registers one server type.
type AdapterKind struct {
	New    func(Config, mediasource.Deps) (Adapter, error)
	Type   string
	Schema formschema.Schema
}

// Config is one parsed media_servers entry. Each mapping's From is the
// server's path and its To the local one.
type Config struct {
	Values       formschema.Values
	ID           string
	Type         string
	PathMappings []mediasource.PathMapping
}

const (
	section         = "media_servers"
	maxServers      = 8
	maxServerID     = 32
	keyID           = "id"
	keyType         = "type"
	keyEnabled      = "enabled"
	keyPathMappings = "path_mappings"
	msgRequired     = "required"
	msgScalar       = "must be a single value"
)

// ParseConfigs reads the media_servers section, n being its node or nil when
// absent, by the sources section's rules (mediasource.ParseConfigs) with at
// most 8 entries and no exclude_tags, its mappings also injective with from
// and to swapped. It reports every refusal at once at its absolute path; an
// entry with enabled: false is validated and left out of the result.
func ParseConfigs(n *yaml.Node, kinds []AdapterKind) ([]Config, []formschema.FieldError) {
	if n == nil || formschema.IsNull(n) {
		return nil, nil
	}
	if n.Kind != yaml.SequenceNode {
		return nil, []formschema.FieldError{{Path: section, Message: "must be a list of media server entries"}}
	}
	var errs []formschema.FieldError
	if len(n.Content) > maxServers {
		errs = append(errs, formschema.FieldError{Path: section, Message: fmt.Sprintf("must list at most %d media servers", maxServers)})
	}
	var out []Config
	first := map[string]int{}
	for i, e := range n.Content {
		cfg, enabled, eerrs := parseEntry(e, kinds)
		if j, dup := first[cfg.ID]; dup && cfg.ID != "" {
			eerrs = append(eerrs, formschema.FieldError{Path: keyID, Message: fmt.Sprintf("repeats the id of %s[%d]", section, j)})
		} else if cfg.ID != "" {
			first[cfg.ID] = i
		}
		errs = append(errs, formschema.Prefix(fmt.Sprintf("%s[%d]", section, i), eerrs)...)
		if enabled && len(eerrs) == 0 {
			out = append(out, cfg)
		}
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return out, nil
}

func parseEntry(e *yaml.Node, kinds []AdapterKind) (Config, bool, []formschema.FieldError) {
	if e.Kind != yaml.MappingNode {
		return Config{}, false, []formschema.FieldError{{Message: "must be a mapping"}}
	}
	var cfg Config
	enabled := true
	envelope, adapter := splitEntry(e)
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
	var merrs []formschema.FieldError
	cfg.PathMappings, merrs = parseMappings(envelope[keyPathMappings])
	errs = append(errs, merrs...)
	for _, v := range envelope {
		formschema.MarkPlain(v)
	}
	if kind == nil {
		return cfg, enabled, errs
	}
	cfg.Type = kind.Type
	vals, verrs := kind.Schema.Decode(adapter)
	errs = append(errs, verrs...)
	cfg.Values = vals
	if len(errs) == 0 {
		if _, err := kind.New(cfg, mediasource.Deps{}); err != nil {
			errs = append(errs, formschema.FieldError{Message: err.Error()})
		}
	}
	return cfg, enabled, errs
}

// splitEntry parts a mapping entry into its envelope values, by key, and the
// adapter's own keys, as a mapping for its schema's Decode.
func splitEntry(e *yaml.Node) (envelope map[string]*yaml.Node, adapter *yaml.Node) {
	envelope = map[string]*yaml.Node{}
	adapter = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(e.Content); i += 2 {
		k, v := e.Content[i], e.Content[i+1]
		switch k.Value {
		case keyID, keyType, keyEnabled, keyPathMappings:
			envelope[k.Value] = v
		default:
			adapter.Content = append(adapter.Content, k, v)
		}
	}
	return envelope, adapter
}

func parseID(v *yaml.Node) (string, *formschema.FieldError) {
	switch {
	case v == nil || formschema.IsNull(v):
		return "", &formschema.FieldError{Path: keyID, Message: msgRequired}
	case v.Kind != yaml.ScalarNode:
		return "", &formschema.FieldError{Path: keyID, Message: msgScalar}
	case !validID(v.Value):
		return "", &formschema.FieldError{Path: keyID, Message: fmt.Sprintf("must be 1 to %d characters of a-z, 0-9 and -, such as plex", maxServerID)}
	}
	return v.Value, nil
}

func validID(s string) bool {
	if s == "" || len(s) > maxServerID {
		return false
	}
	return !strings.ContainsFunc(s, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-'
	})
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

// parseMappings is mediasource.ParseMappings plus the reverse check Refresh
// needs: no two local paths may map back to one server path.
func parseMappings(v *yaml.Node) ([]mediasource.PathMapping, []formschema.FieldError) {
	ms, errs := mediasource.ParseMappings(v)
	if len(errs) > 0 {
		return nil, errs
	}
	if j := mediasource.Injective(swapped(ms)); j >= 0 {
		return nil, []formschema.FieldError{{Path: fmt.Sprintf("%s[%d].from", keyPathMappings, j), Message: "overlaps an earlier mapping's from without nesting exactly under it, so two local paths would map to one server path"}}
	}
	return ms, nil
}
