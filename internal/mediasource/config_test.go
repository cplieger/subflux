package mediasource_test

import (
	"errors"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
	"go.yaml.in/yaml/v4"
)

// arrKind is an arr-shaped kind: url and api_key required, public_url
// optional, New refusing a url that is not http or https. news counts New
// calls and keeps the Deps each was given.
type arrKind struct {
	news []mediasource.Deps
	s    *script
}

var arrSchema = formschema.Schema{Fields: []formschema.Field{
	{Key: "url", Kind: formschema.String, Required: true},
	{Key: "api_key", Kind: formschema.Secret, Required: true},
	{Key: "public_url", Kind: formschema.String},
}}

func (k *arrKind) kind(typ string) mediasource.AdapterKind {
	return mediasource.AdapterKind{Type: typ, Schema: arrSchema, New: func(cfg mediasource.Config, deps mediasource.Deps) (mediasource.Adapter, error) {
		k.news = append(k.news, deps)
		raw := cfg.Values.String("url")
		if u, err := url.Parse(raw); err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return nil, errors.New("baseURL must be an absolute http or https URL")
		}
		if k.s == nil {
			return &script{caps: allCaps}, nil
		}
		return k.s, nil
	}}
}

func arrKinds(k *arrKind) []mediasource.AdapterKind {
	return []mediasource.AdapterKind{k.kind("sonarr"), k.kind("radarr")}
}

func parseYAML(t testing.TB, doc string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(doc), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Content) == 0 {
		return nil
	}
	return n.Content[0]
}

func errorAt(errs []formschema.FieldError, path string) (string, bool) {
	i := slices.IndexFunc(errs, func(e formschema.FieldError) bool { return e.Path == path })
	if i < 0 {
		return "", false
	}
	return errs[i].Message, true
}

func TestConfigIDUsesSourceIDCheck(t *testing.T) {
	var want media.SourceID
	wantMsg := want.UnmarshalText([]byte("Sonarr")).Error()
	_, errs := mediasource.ParseConfigs(parseYAML(t, "- {id: Sonarr, type: sonarr, url: 'http://s', api_key: k}"), arrKinds(&arrKind{}))
	if got, ok := errorAt(errs, "sources[0].id"); !ok || got != wantMsg {
		t.Errorf("error at sources[0].id = %q (%v), want SourceID.UnmarshalText's %q; all: %v", got, ok, wantMsg, errs)
	}
}

func TestParseConfigsRefusals(t *testing.T) {
	var doc strings.Builder
	doc.WriteString(`
- {id: Sonarr, type: sonarr, url: 'http://a', api_key: k}
- {id: dup, type: sonarr, url: 'http://a', api_key: k}
- {id: dup, type: sonarr, url: 'http://a', api_key: k}
- {id: p, type: plex, url: 'http://a', api_key: k}
- {id: rel, type: sonarr, url: 'http://a', api_key: k, path_mappings: [{from: tv, to: /media}]}
- {id: lf, type: sonarr, url: 'http://a', api_key: k, path_mappings: [{from: "/tv\nx", to: /media}]}
- {id: rep, type: sonarr, url: 'http://a', api_key: k, path_mappings: [{from: /tv, to: /m1}, {from: /tv, to: /m2}]}
- {id: alias, type: sonarr, url: 'http://a', api_key: k, path_mappings: [{from: /arr-a, to: /media}, {from: /arr-b, to: /media}]}
- {id: nest, type: sonarr, url: 'http://a', api_key: k, path_mappings: [{from: /data, to: /m}, {from: /data/tv, to: /m/shows}]}
- {id: long, type: sonarr, url: 'http://a', api_key: k, exclude_tags: [` + strings.Repeat("x", 65) + `]}
- {id: case, type: sonarr, url: 'http://a', api_key: k, exclude_tags: [Keep, keep]}
- {id: extra, type: sonarr, url: 'http://a', api_key: k, colour: red}
- {id: ftp, type: sonarr, url: 'ftp://x', api_key: k}
- {id: nokey, type: radarr, url: 'http://a'}
- {id: onoff, type: sonarr, url: 'http://a', api_key: k, enabled: maybe}
- {id: tomany, type: sonarr, url: 'http://a', api_key: k, path_mappings: [{from: /a, to: /b, via: /c}]}
- {id: e16, type: sonarr, url: 'http://a', api_key: k}
- {id: e17, type: sonarr, url: 'http://a', api_key: k}
`)
	_, errs := mediasource.ParseConfigs(parseYAML(t, doc.String()), arrKinds(&arrKind{}))
	want := []string{
		"sources",
		"sources[0].id",
		"sources[2].id",
		"sources[3].type",
		"sources[4].path_mappings[0].from",
		"sources[5].path_mappings[0].from",
		"sources[6].path_mappings[1].from",
		"sources[7].path_mappings[1].to",
		"sources[8].path_mappings[1].to",
		"sources[9].exclude_tags[0]",
		"sources[10].exclude_tags[1]",
		"sources[11].colour",
		"sources[12]",
		"sources[13].api_key",
		"sources[14].enabled",
		"sources[15].path_mappings[0].via",
	}
	for _, p := range want {
		if _, ok := errorAt(errs, p); !ok {
			t.Errorf("no error at %s", p)
		}
	}
	if len(errs) != len(want) {
		t.Errorf("%d errors, want %d: %v", len(errs), len(want), errs)
	}
	if msg, _ := errorAt(errs, "sources[3].type"); msg != "must be one of radarr, sonarr" {
		t.Errorf("unknown type message %q, want the known types listed", msg)
	}
	if msg, _ := errorAt(errs, "sources[12]"); !strings.Contains(msg, "baseURL") || strings.Contains(msg, "ftp://x") {
		t.Errorf("New's refusal %q, want its sentence without the URL", msg)
	}
}

func TestParseConfigsAccepts(t *testing.T) {
	doc := `
- {id: sib, type: sonarr, url: 'http://a', api_key: k, path_mappings: [{from: /tv, to: /media/tv}, {from: /movies, to: /media/movies}]}
- {id: nested, type: radarr, url: 'http://b', api_key: k, path_mappings: [{from: /data, to: /m}, {from: /data/tv, to: /m/tv}]}
`
	cfgs, errs := mediasource.ParseConfigs(parseYAML(t, doc), arrKinds(&arrKind{}))
	if len(errs) > 0 || len(cfgs) != 2 {
		t.Fatalf("ParseConfigs = %d configs, errors %v; want both accepted", len(cfgs), errs)
	}
	if cfgs[1].ID != "nested" || cfgs[1].Type != "radarr" || cfgs[1].Index != 1 || len(cfgs[1].PathMappings) != 2 {
		t.Errorf("second config %+v, want nested radarr at index 1 with two mappings", cfgs[1])
	}
	if key := cfgs[0].Values.Secret("api_key"); key != "k" {
		t.Errorf("api_key %q, want the adapter's decoded value", key)
	}
	for _, n := range []string{"", "~", "sources: null"} {
		node := parseYAML(t, n)
		if n == "sources: null" {
			node = node.Content[1]
		}
		if cfgs, errs := mediasource.ParseConfigs(node, nil); cfgs != nil || errs != nil {
			t.Errorf("ParseConfigs(%q) = %v %v, want no sources", n, cfgs, errs)
		}
	}
	if _, errs := mediasource.ParseConfigs(parseYAML(t, "{a: b}"), nil); len(errs) != 1 || errs[0].Path != "sources" {
		t.Errorf("a mapping section: %v, want one error at sources", errs)
	}
}

func TestExcludeTagsDefaults(t *testing.T) {
	cases := map[string][]string{
		"":                               {"no-subflux"},
		", exclude_tags: null":           {"no-subflux"},
		", exclude_tags: []":             {},
		", exclude_tags: [' Anime ', X]": {"anime", "x"},
	}
	for tail, want := range cases {
		cfgs, errs := mediasource.ParseConfigs(parseYAML(t, "- {id: s, type: sonarr, url: 'http://a', api_key: k"+tail+"}"), arrKinds(&arrKind{}))
		if len(errs) > 0 || len(cfgs) != 1 || !slices.Equal(cfgs[0].ExcludeTags, want) {
			t.Errorf("exclude_tags%q = %+v %v, want one entry excluding %q", tail, cfgs, errs, want)
		}
	}
}

func TestEnvelopeNullsAreAbsent(t *testing.T) {
	cfgs, errs := mediasource.ParseConfigs(parseYAML(t, "- {id: s, type: sonarr, url: 'http://a', api_key: k, enabled: ~, path_mappings: null, exclude_tags: null}"), arrKinds(&arrKind{}))
	if len(errs) > 0 || len(cfgs) != 1 || cfgs[0].PathMappings != nil || !slices.Equal(cfgs[0].ExcludeTags, []string{"no-subflux"}) {
		t.Errorf("ParseConfigs = %+v %v, want one enabled entry with no mappings and the default tag", cfgs, errs)
	}
}

func TestEnabledFalseKeptInDocument(t *testing.T) {
	doc := `
- {id: off, type: sonarr, url: 'http://a', api_key: k, enabled: false, path_mappings: [{from: /tv, to: /media}], exclude_tags: [x]}
- {id: on, type: radarr, url: 'http://b', api_key: k, enabled: true}
`
	k := &arrKind{}
	root := parseYAML(t, doc)
	cfgs, errs := mediasource.ParseConfigs(root, arrKinds(k))
	if len(errs) > 0 || len(cfgs) != 1 || cfgs[0].ID != "on" || cfgs[0].Index != 1 {
		t.Fatalf("ParseConfigs = %+v %v, want only the enabled entry, at index 1", cfgs, errs)
	}
	out, err := yaml.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "enabled: !plain false") || !strings.Contains(string(out), "id: !plain off") {
		t.Errorf("document after parsing:\n%s\nwant the disabled entry kept, its envelope scalars plain", out)
	}
	if strings.Contains(string(out), "api_key: !plain") {
		t.Errorf("document marks the api_key plain:\n%s", out)
	}
	for i, d := range k.news {
		if d.HTTP != nil || d.Logger != nil {
			t.Errorf("New call %d got Deps %+v, want the zero Deps", i, d)
		}
	}
	if len(k.news) != 2 {
		t.Errorf("%d New calls, want one per entry, the disabled one included", len(k.news))
	}
	_, errs = mediasource.ParseConfigs(parseYAML(t, "- {id: off, type: sonarr, url: 'ftp://a', api_key: k, enabled: false}"), arrKinds(k))
	if _, ok := errorAt(errs, "sources[0]"); !ok {
		t.Errorf("a disabled entry with a bad url: %v, want it validated", errs)
	}
}
