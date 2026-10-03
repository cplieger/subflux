package schema

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/subflux"
)

// The settings form saves each schema field under the key path the schema
// gives it, and the loader refuses any key config.Config does not declare. So
// every path the schema can emit has to name a yaml tag on the struct, or a
// settings save fails with "unknown configuration key".

// schemaPaths lists every key path a settings save can write, mirroring the
// client's emitters: scoring weights sit under "weights", poll_interval is a
// top-level scalar, list and languages sections are one value each, and a
// provider writes enabled, priority and settings.<key>.
func schemaPaths(sections []subflux.SchemaSection) [][]string {
	var paths [][]string
	var leaves func(prefix []string, fields []subflux.SchemaField)
	leaves = func(prefix []string, fields []subflux.SchemaField) {
		for i := range fields {
			p := append(append([]string{}, prefix...), fields[i].Key)
			if fields[i].Type == fieldNested {
				leaves(p, fields[i].Fields)
				continue
			}
			paths = append(paths, p)
		}
	}
	for i := range sections {
		s := &sections[i]
		switch {
		case s.Type == sectionProviders:
			for _, p := range s.Providers {
				base := []string{s.Key, p.Name}
				paths = append(paths, append(append([]string{}, base...), "enabled"),
					append(append([]string{}, base...), "priority"))
				leaves(append(base, "settings"), p.Settings)
			}
		case s.Type == fieldList, s.Type == fieldLanguages, s.Key == keyPollInterval:
			paths = append(paths, []string{s.Key})
		case s.Key == "scoring":
			leaves([]string{s.Key, "weights"}, s.Fields)
		default:
			if s.EnableKey != "" {
				paths = append(paths, []string{s.Key, s.EnableKey})
			}
			leaves([]string{s.Key}, s.Fields)
		}
	}
	return paths
}

// yamlPathErr walks path through t by yaml tag and reports the first segment
// that does not resolve, or "" when the whole path does. A string-keyed map
// accepts any key and an interface accepts any remainder, which is how provider
// settings are declared.
func yamlPathErr(t reflect.Type, path []string) string {
	for i, seg := range path {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		switch t.Kind() {
		case reflect.Interface:
			return ""
		case reflect.Map:
			t = t.Elem()
			continue
		case reflect.Struct:
			f, ok := fieldByYAMLTag(t, seg)
			if !ok {
				return strings.Join(path[:i+1], ".")
			}
			t = f.Type
		default:
			return strings.Join(path[:i+1], ".")
		}
	}
	return ""
}

func fieldByYAMLTag(t reflect.Type, name string) (reflect.StructField, bool) {
	for f := range t.Fields() {
		tag, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if tag == name {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

func TestSchemaPaths_resolve_to_config_yaml_tags(t *testing.T) {
	t.Parallel()
	sections := Sections([]subflux.ProviderSchema{{
		Name: "probe",
		Settings: []subflux.SchemaField{
			{Key: "api_key", Type: fieldSecret},
			{Key: "use_hash", Type: fieldBool},
		},
	}})
	paths := schemaPaths(sections)
	if len(paths) == 0 {
		t.Fatal("schemaPaths(Sections) found no paths; the walker matched nothing")
	}
	cfgType := reflect.TypeFor[config.Config]()
	for _, p := range paths {
		if bad := yamlPathErr(cfgType, p); bad != "" {
			t.Errorf("schema path %q does not resolve on config.Config: no yaml key %q",
				strings.Join(p, "."), bad)
		}
	}
}

// TestSchemaPaths_walker_refuses_an_undeclared_key keeps the resolver honest:
// a walker that accepted everything would pass the test above vacuously.
func TestSchemaPaths_walker_refuses_an_undeclared_key(t *testing.T) {
	t.Parallel()
	cfgType := reflect.TypeFor[config.Config]()
	for _, tc := range []struct {
		path []string
		want string
	}{
		{path: []string{"auth", "oidc.client_secret"}, want: "auth.oidc.client_secret"},
		{path: []string{"auth", "oidc", "nope"}, want: "auth.oidc.nope"},
		{path: []string{"sonarr", "url", "deeper"}, want: "sonarr.url.deeper"},
	} {
		if got := yamlPathErr(cfgType, tc.path); got != tc.want {
			t.Errorf("yamlPathErr(Config, %q) = %q, want %q", strings.Join(tc.path, "."), got, tc.want)
		}
	}
}

func TestSections_flags_every_secret_typed_field(t *testing.T) {
	t.Parallel()
	sections := Sections([]subflux.ProviderSchema{{
		Name:     "probe",
		Settings: []subflux.SchemaField{{Key: "token", Type: fieldSecret}},
	}})
	var secrets []string
	var walk func(prefix string, fields []subflux.SchemaField)
	walk = func(prefix string, fields []subflux.SchemaField) {
		for _, f := range fields {
			path := prefix + "." + f.Key
			if f.Type == fieldSecret {
				secrets = append(secrets, path)
				if !f.Secret {
					t.Errorf("schema field %s has type secret but Secret = false", path)
				}
			}
			walk(path, f.Fields)
		}
	}
	for _, s := range sections {
		walk(s.Key, s.Fields)
		for _, p := range s.Providers {
			walk(s.Key+"."+p.Name+".settings", p.Settings)
		}
	}
	for _, want := range []string{
		"sonarr.api_key", "radarr.api_key", "auth.oidc.client_secret", "providers.probe.settings.token",
	} {
		if !slices.Contains(secrets, want) {
			t.Errorf("secret-typed fields = %v, want %s among them", secrets, want)
		}
	}
}

func TestSections_does_not_mutate_the_provider_schemas_it_is_given(t *testing.T) {
	t.Parallel()
	in := []subflux.ProviderSchema{{
		Name:     "probe",
		Settings: []subflux.SchemaField{{Key: "token", Type: fieldSecret}},
	}}
	_ = Sections(in)
	if in[0].Settings[0].Secret {
		t.Error("Sections set Secret on the caller's provider schema, want the caller's slice untouched")
	}
}
