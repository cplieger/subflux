package provider_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/provider"
	"go.yaml.in/yaml/v4"
)

// configNode is text's one document node.
func configNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

var regionField = formschema.Field{Key: "region", Label: "Region", Kind: formschema.String, Default: "eu"}

// configEntries are two entries: keyed, whose New refuses the key "wrong"
// quoting it and counts its calls, and open, with no settings.
func configEntries(builds *int) []provider.Entry {
	keyed := entry(&scripted{}, []string{host}, keyField, regionField)
	keyed.ID = "keyed"
	inner := keyed.New
	keyed.New = func(c provider.Client, v formschema.Values) (provider.Adapter, error) {
		*builds++
		if k := v.Secret("api_key"); k == "wrong" {
			return nil, errors.New("the key " + k + " is not 32 hex characters")
		}
		return inner(c, v)
	}
	open := entry(&scripted{}, []string{host})
	open.ID = "open"
	return []provider.Entry{keyed, open}
}

func paths(errs []formschema.FieldError) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Path
	}
	return out
}

func TestAdapterNewErrorIsFieldError(t *testing.T) {
	var builds int
	cfgs, errs := provider.ParseConfigs(configNode(t, "keyed: {settings: {api_key: wrong}}\n"), configEntries(&builds))
	if cfgs != nil || !slices.Equal(paths(errs), []string{"providers.keyed.settings"}) {
		t.Fatalf("ParseConfigs = %v, %v; want one error at providers.keyed.settings", cfgs, errs)
	}
	if msg := errs[0].Message; !strings.Contains(msg, "is not 32 hex characters") || strings.Contains(msg, "wrong") {
		t.Errorf("message %q, want the adapter's refusal with its key redacted", msg)
	}
	if builds != 1 {
		t.Errorf("New ran %d times, want once", builds)
	}
}

func TestParseConfigs(t *testing.T) {
	t.Run("an absent or null section is no entry", func(t *testing.T) {
		for _, n := range []*yaml.Node{nil, configNode(t, "~\n")} {
			if cfgs, errs := provider.ParseConfigs(n, configEntries(new(int))); cfgs != nil || errs != nil {
				t.Errorf("ParseConfigs = %v, %v; want nothing", cfgs, errs)
			}
		}
	})
	t.Run("a listed entry is enabled at priority 0 with its defaults, in file order", func(t *testing.T) {
		var builds int
		cfgs, errs := provider.ParseConfigs(configNode(t, "open:\nkeyed: {priority: 3, settings: {api_key: k}}\n"), configEntries(&builds))
		if errs != nil || len(cfgs) != 2 {
			t.Fatalf("ParseConfigs = %v, %v", cfgs, errs)
		}
		if c := cfgs[0]; c.ID != "open" || !c.Enabled || c.Priority != 0 || len(c.Values) != 0 {
			t.Errorf("first config %+v, want open, enabled, priority 0, no values", c)
		}
		if c := cfgs[1]; c.ID != "keyed" || !c.Enabled || c.Priority != 3 || c.Values.Secret("api_key") != "k" || c.Values.String("region") != "eu" {
			t.Errorf("second config %+v, want keyed at priority 3 with key k and region eu", c)
		}
		if builds != 1 {
			t.Errorf("New ran %d times, want once for the one credentialed entry", builds)
		}
	})
	t.Run("a disabled entry keeps its values, needs no Required key and is never built", func(t *testing.T) {
		var builds int
		cfgs, errs := provider.ParseConfigs(configNode(t, "keyed: {enabled: false, settings: {region: us}}\n"), configEntries(&builds))
		if errs != nil || len(cfgs) != 1 {
			t.Fatalf("ParseConfigs = %v, %v", cfgs, errs)
		}
		if c := cfgs[0]; c.Enabled || c.Values.String("region") != "us" || c.Values.Secret("api_key") != "" {
			t.Errorf("config %+v, want disabled with region us and no key", c)
		}
		if builds != 0 {
			t.Errorf("New ran %d times for a disabled entry", builds)
		}
	})
	t.Run("every scalar but a Secret's is marked plain", func(t *testing.T) {
		n := configNode(t, "keyed: {enabled: true, priority: 2, settings: {api_key: k, region: us}}\n")
		if _, errs := provider.ParseConfigs(n, configEntries(new(int))); errs != nil {
			t.Fatal(errs)
		}
		env := n.Content[1]
		settings := env.Content[5]
		tags := []string{env.Content[1].Tag, env.Content[3].Tag, settings.Content[1].Tag, settings.Content[3].Tag}
		if want := []string{formschema.PlainTag, formschema.PlainTag, "!!str", formschema.PlainTag}; !slices.Equal(tags, want) {
			t.Errorf("tags of enabled, priority, api_key, region = %v, want %v", tags, want)
		}
	})
}

func TestParseConfigsRefusals(t *testing.T) {
	cases := []struct{ name, text, path string }{
		{"a section that is no mapping", "[keyed]\n", "providers"},
		{"an unknown provider", "bogus: {}\n", "providers.bogus"},
		{"a provider set twice", "open: {}\nopen: {priority: 1}\n", "providers.open"},
		{"an entry that is no mapping", "open: [true]\n", "providers.open"},
		{"an unknown envelope key", "open: {enabled: true, api_key: k}\n", "providers.open.api_key"},
		{"settings set twice", "keyed: {settings: {api_key: k}, settings: {api_key: j}}\n", "providers.keyed.settings"},
		{"enabled not a boolean", "open: {enabled: maybe}\n", "providers.open.enabled"},
		{"a priority over its bound", "open: {priority: 101}\n", "providers.open.priority"},
		{"a negative priority", "open: {priority: -1}\n", "providers.open.priority"},
		{"an enabled entry missing its Required key", "keyed: {settings: {region: us}}\n", "providers.keyed.settings.api_key"},
		{"a settings key its schema lacks", "open: {settings: {token: t}}\n", "providers.open.settings.token"},
		{"settings that are no mapping", "open: {settings: k}\n", "providers.open.settings"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfgs, errs := provider.ParseConfigs(configNode(t, c.text), configEntries(new(int)))
			if cfgs != nil || !slices.Equal(paths(errs), []string{c.path}) {
				t.Errorf("ParseConfigs = %v, %v; want one error at %s", cfgs, errs, c.path)
			}
		})
	}
	t.Run("every entry's refusals are reported at once", func(t *testing.T) {
		_, errs := provider.ParseConfigs(configNode(t, "bogus: {}\nopen: {priority: 101}\nkeyed: {settings: {api_key: wrong}}\n"), configEntries(new(int)))
		if want := []string{"providers.bogus", "providers.open.priority", "providers.keyed.settings"}; !slices.Equal(paths(errs), want) {
			t.Errorf("errors at %v, want %v", paths(errs), want)
		}
	})
}
