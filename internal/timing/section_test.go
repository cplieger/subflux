package timing_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/timing"
	"go.yaml.in/yaml/v4"
)

func sectionNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func TestSectionDefaults(t *testing.T) {
	t.Parallel()
	want := timing.Config{Sync: true}
	if got, errs := timing.ParseConfig(nil); got != want || errs != nil {
		t.Errorf("absent section: %+v %v, want %+v", got, errs, want)
	}
	if got, errs := timing.ParseConfig(sectionNode(t, "{}")); got != want || errs != nil {
		t.Errorf("empty section: %+v %v, want %+v", got, errs, want)
	}
	set := timing.Config{AudioFallback: true}
	if got, errs := timing.ParseConfig(sectionNode(t, "sync: false\naudio_fallback: true\n")); got != set || errs != nil {
		t.Errorf("both set: %+v %v, want %+v", got, errs, set)
	}
}

func TestSectionRefusesUnknownKey(t *testing.T) {
	t.Parallel()
	for text, want := range map[string]formschema.FieldError{
		"sync_min_confidence: 0.6\n": {Path: "timing.sync_min_confidence", Message: "unknown key"},
		"sync: sometimes\n":          {Path: "timing.sync"},
	} {
		got, errs := timing.ParseConfig(sectionNode(t, text))
		if got != (timing.Config{}) || len(errs) != 1 || errs[0].Path != want.Path || want.Message != "" && errs[0].Message != want.Message {
			t.Errorf("%q: %+v %v, want the zero Config and %+v", text, got, errs, want)
		}
	}
	var keys []string
	for _, f := range timing.Schema.Fields {
		keys = append(keys, f.Key)
	}
	if !slices.Equal(keys, []string{"sync", "audio_fallback"}) {
		t.Errorf("schema keys %q, want sync and audio_fallback", keys)
	}
}
