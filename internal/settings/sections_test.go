package settings

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediasource"
	"github.com/cplieger/subflux/internal/mediasource/sonarr"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/rank"
)

func loadWith(t *testing.T, r Registries, text string) Snapshot {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return Load(path, r)
}

func TestSourcesAndScanSections(t *testing.T) {
	r := Registries{Sources: []mediasource.AdapterKind{sonarr.Kind}}
	t.Run("absent sections are no sources and the default schedule", func(t *testing.T) {
		want := mediasource.Schedule{Change: 30 * time.Second, Whole: 24 * time.Hour}
		a, ok := loadWith(t, r, validLanguages).Active()
		if !ok || a.Sources != nil || a.Scan != want {
			t.Errorf("Active = %v (%+v, %+v), want no sources and %+v", ok, a.Sources, a.Scan, want)
		}
	})
	t.Run("both sections parse with the registered kinds", func(t *testing.T) {
		a, ok := loadWith(t, r, validLanguages+
			"sources: [{id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k}]\n"+
			"scan: {change_interval: 1m, whole_interval: 6h}\n").Active()
		if !ok {
			t.Fatal("not Active")
		}
		if len(a.Sources) != 1 || a.Sources[0].ID != "tv" || a.Sources[0].Type != "sonarr" {
			t.Errorf("Sources = %+v, want the one sonarr entry tv", a.Sources)
		}
		if want := (mediasource.Schedule{Change: time.Minute, Whole: 6 * time.Hour}); a.Scan != want {
			t.Errorf("Scan = %+v, want %+v", a.Scan, want)
		}
	})
	t.Run("an unregistered type and an out-of-range interval fail at their paths", func(t *testing.T) {
		s := loadWith(t, Registries{}, validLanguages+
			"sources: [{id: tv, type: sonarr, url: 'http://sonarr:8989', api_key: k}]\n"+
			"scan: {change_interval: 1s}\n")
		if got, want := failurePaths(t, s), []string{"sources[0].type", "scan.change_interval"}; !slices.Equal(got, want) {
			t.Errorf("paths %v, want %v", got, want)
		}
	})
}

// unused is an adapter the tests never ask.
type unused struct{}

func (unused) Search(context.Context, provider.Query) ([]provider.Candidate, error) { return nil, nil }

func (unused) Fetch(context.Context, provider.Ref) ([]byte, error) {
	return nil, nil
}

// keyedEntry is a provider whose New refuses the key "wrong".
func keyedEntry() provider.Entry {
	return provider.Entry{
		ID: "keyed", Summary: "A provider the test builds.", Hosts: []string{"api.example.test"},
		Schema: formschema.Schema{Fields: []formschema.Field{{Key: "api_key", Kind: formschema.Secret, Required: true}}},
		New: func(_ provider.Client, v formschema.Values) (provider.Adapter, error) {
			if v.Secret("api_key") == "wrong" {
				return nil, errors.New("the key is refused")
			}
			return unused{}, nil
		},
	}
}

func TestProvidersAndSearchSections(t *testing.T) {
	r := Registries{Providers: []provider.Entry{keyedEntry()}}
	t.Run("both sections parse, search against the parsed policy", func(t *testing.T) {
		a, ok := loadWith(t, r, validLanguages+"providers: {keyed: {priority: 2, settings: {api_key: k}}}\nsearch: {min_fit: exact, min_fit_by_target: {en: same_source}}\n").Active()
		if !ok {
			t.Fatal("not Active")
		}
		if len(a.Providers) != 1 || a.Providers[0].ID != "keyed" || !a.Providers[0].Enabled || a.Providers[0].Priority != 2 {
			t.Errorf("Providers = %+v, want keyed enabled at priority 2", a.Providers)
		}
		if a.Search.MinFit != rank.Exact || len(a.Search.TargetMinFit) != 1 {
			t.Errorf("Search = %+v, want exact with one per-target fit", a.Search)
		}
	})
	t.Run("an adapter's New error fails the save, which writes nothing", func(t *testing.T) {
		path := fixture(t, validLanguages)
		s := NewService(path, r, Load(path, r), nil, nil, nil)
		_, errs, err := s.Save(t.Context(), node(t, validLanguages+"providers: {keyed: {settings: {api_key: wrong}}}\n"), s.Snapshot().Revision())
		if err != nil || !slices.Equal(errPaths(errs), []string{"providers.keyed.settings"}) {
			t.Fatalf("Save = %v, %v; want one error at providers.keyed.settings", errs, err)
		}
		if got := fileText(t, path); got != validLanguages {
			t.Errorf("the file changed to %q", got)
		}
	})
}

func TestSearchSectionNeedsLanguages(t *testing.T) {
	const badLanguages = "languages: {default: [{tag: 'not a tag'}]}\n"
	t.Run("a search section beside a failed languages section reports one error", func(t *testing.T) {
		s := loadWith(t, Registries{}, badLanguages+"search: {min_fit_by_target: {en: exact}}\n")
		got := failurePaths(t, s)
		if !slices.Contains(got, "search") || slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, "search.") }) {
			t.Errorf("paths %v, want one at search and none under it", got)
		}
	})
	t.Run("an absent search section adds no error", func(t *testing.T) {
		if got := failurePaths(t, loadWith(t, Registries{}, badLanguages)); slices.ContainsFunc(got, func(p string) bool { return strings.HasPrefix(p, "search") }) {
			t.Errorf("paths %v, want none at search", got)
		}
	})
	t.Run("a search section beside a parsed one is checked against it", func(t *testing.T) {
		if got, want := failurePaths(t, loadWith(t, Registries{}, validLanguages+"search: {min_fit_by_target: {fr: exact}}\n")), []string{"search.min_fit_by_target.fr"}; !slices.Equal(got, want) {
			t.Errorf("paths %v, want %v", got, want)
		}
	})
}

func TestSectionSchemasCoverFlatSections(t *testing.T) {
	schemas := SectionSchemas()
	keys := make([]string, 0, len(schemas))
	for _, s := range schemas {
		keys = append(keys, s.Key)
	}
	// Platform-auth design 4.3: every section decoded with a schema, in table
	// order; languages, sources and providers are hand-walked.
	want := []string{"server", "auth", "scan", "search", "post_processing", "timing", "embedded_subtitles", "backup"}
	if !slices.Equal(keys, want) {
		t.Fatalf("sections %v, want %v", keys, want)
	}
	unknownAt := func(t *testing.T, text, path string) bool {
		t.Helper()
		errs, _ := loadText(t, validLanguages+text).Failure()
		return slices.ContainsFunc(errs, func(e formschema.FieldError) bool { return e.Path == path && e.Message == "unknown key" })
	}
	for _, s := range schemas {
		t.Run(s.Key+" accepts each served key and refuses others", func(t *testing.T) {
			for _, f := range s.Schema.Fields {
				if unknownAt(t, s.Key+": {"+f.Key+": x}\n", s.Key+"."+f.Key) {
					t.Errorf("%s.%s is served but refused as an unknown key", s.Key, f.Key)
				}
			}
			if !unknownAt(t, s.Key+": {not_served: x}\n", s.Key+".not_served") {
				t.Errorf("%s.not_served is not refused as an unknown key", s.Key)
			}
		})
	}
	t.Run("only auth.disable_auth is BootOnly", func(t *testing.T) {
		var boot []string
		for _, s := range schemas {
			for _, f := range s.Schema.Fields {
				if f.BootOnly {
					boot = append(boot, s.Key+"."+f.Key)
				}
			}
		}
		if !slices.Equal(boot, []string{"auth.disable_auth"}) {
			t.Errorf("BootOnly fields %v, want [auth.disable_auth]", boot)
		}
	})
}
