package schema

import (
	"strconv"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config/defaults"
)

func TestSchema_returns_all_sections(t *testing.T) {
	t.Parallel()
	sections := Schema(nil)

	wantKeys := []string{
		"sonarr", "radarr", "media_roots", "trusted_proxies", "poll_interval",
		"languages", "embedded_subtitles", "providers", "search", "adaptive",
		"post_processing", "auth", "scoring", "backup", "logging",
	}
	if len(sections) != len(wantKeys) {
		t.Fatalf("Schema() returned %d sections, want %d", len(sections), len(wantKeys))
	}
	for i, want := range wantKeys {
		if sections[i].Key != want {
			t.Errorf("Schema()[%d].Key = %q, want %q", i, sections[i].Key, want)
		}
	}
}

// TestSchema_embedded_subtitles_defaults_declared_once pins the P14
// single-source property at its post-cutover home: the embedded policy
// defaults (true/true/false) are declared once in the defaults package and
// rendered into the embedded_subtitles schema section from there — the same
// constants newWithDefaults feeds the pre-defaulted config decode.
func TestSchema_embedded_subtitles_defaults_declared_once(t *testing.T) {
	t.Parallel()
	sections := Schema(nil)
	var emb *api.SchemaSection
	for i := range sections {
		if sections[i].Key == "embedded_subtitles" {
			emb = &sections[i]
			break
		}
	}
	if emb == nil {
		t.Fatal("Schema() lacks the embedded_subtitles section")
	}
	if emb.Type != "fields" {
		t.Errorf("embedded_subtitles.Type = %q, want fields (standard section renderer)", emb.Type)
	}
	want := map[string]string{
		"ignore_pgs":    strconv.FormatBool(defaults.EmbeddedIgnorePGS),
		"ignore_vobsub": strconv.FormatBool(defaults.EmbeddedIgnoreVobSub),
		"ignore_ass":    strconv.FormatBool(defaults.EmbeddedIgnoreASS),
	}
	if len(emb.Fields) != len(want) {
		t.Fatalf("embedded_subtitles has %d fields, want %d", len(emb.Fields), len(want))
	}
	for _, f := range emb.Fields {
		wantDefault, ok := want[f.Key]
		if !ok {
			t.Errorf("unexpected field %q in embedded_subtitles section", f.Key)
			continue
		}
		if f.Default != wantDefault {
			t.Errorf("embedded_subtitles.%s Default = %q, want %q", f.Key, f.Default, wantDefault)
		}
		if f.Type != "bool" {
			t.Errorf("embedded_subtitles.%s Type = %q, want bool", f.Key, f.Type)
		}
	}
}

func TestSchema_sonarr_section_has_required_fields(t *testing.T) {
	t.Parallel()
	sections := Schema(nil)
	sonarr := sections[0]
	if sonarr.Key != "sonarr" {
		t.Fatalf("sections[0].Key = %q, want sonarr", sonarr.Key)
	}
	if sonarr.EnableKey != "enabled" {
		t.Errorf("sonarr.EnableKey = %q, want %q", sonarr.EnableKey, "enabled")
	}
	if sonarr.RequiredGroup != "arr" {
		t.Errorf("sonarr.RequiredGroup = %q, want %q", sonarr.RequiredGroup, "arr")
	}
	if len(sonarr.Fields) != 3 {
		t.Errorf("sonarr has %d fields, want 3 (url, api_key, public_url)", len(sonarr.Fields))
	}
	if !sonarr.Fields[0].Required {
		t.Error("sonarr url field should be required")
	}
	if !sonarr.Fields[1].Required {
		t.Error("sonarr api_key field should be required")
	}
	if sonarr.Fields[1].Type != "secret" {
		t.Errorf("sonarr api_key field Type = %q, want %q", sonarr.Fields[1].Type, "secret")
	}
}

func TestSchema_providers_section_passes_through(t *testing.T) {
	t.Parallel()
	providers := []api.ProviderSchema{
		{Name: "opensubtitles", Label: "OpenSubtitles"},
		{Name: "yify", Label: "YIFY"},
	}
	sections := Schema(providers)

	var provSection *api.SchemaSection
	for i := range sections {
		if sections[i].Key == "providers" {
			provSection = &sections[i]
			break
		}
	}
	if provSection == nil {
		t.Fatal("Schema() missing providers section")
	}
	if len(provSection.Providers) != 2 {
		t.Errorf("providers section has %d providers, want 2", len(provSection.Providers))
	}
	if provSection.Providers[0].Name != "opensubtitles" {
		t.Errorf("providers[0].Name = %q, want %q", provSection.Providers[0].Name, "opensubtitles")
	}
}
