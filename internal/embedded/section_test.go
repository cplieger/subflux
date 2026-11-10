package embedded_test

import (
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/embedded"
	"go.yaml.in/yaml/v4"
)

func section(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(src), &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Content[0]
}

func TestSectionDefaults(t *testing.T) {
	want := []string{"pgs", "vobsub"}
	for name, n := range map[string]*yaml.Node{"absent section": nil, "absent key": section(t, "{}\n")} {
		c, errs := embedded.ParseConfig(n)
		if len(errs) > 0 || !slices.Equal(c.Ignored(), want) {
			t.Errorf("%s: Ignored = %q, errors %v; want %q", name, c.Ignored(), errs, want)
		}
	}
	if got := (embedded.Config{}).Ignored(); len(got) != 0 {
		t.Errorf("zero Config ignores %q, want nothing", got)
	}
	c, errs := embedded.ParseConfig(section(t, "ignore_codecs: []\n"))
	if len(errs) > 0 || len(c.Ignored()) != 0 {
		t.Errorf("an empty list: Ignored = %q, errors %v; want nothing ignored", c.Ignored(), errs)
	}
	c, errs = embedded.ParseConfig(section(t, "ignore_codecs: [vobsub, dvbsub, vobsub]\n"))
	if want := []string{"dvbsub", "vobsub"}; len(errs) > 0 || !slices.Equal(c.Ignored(), want) {
		t.Errorf("Ignored = %q, errors %v; want %q, sorted and each once", c.Ignored(), errs, want)
	}
	got := c.Ignored()
	got[0] = "srt"
	if c.Ignored()[0] != "dvbsub" {
		t.Error("Ignored shares the Config's slice")
	}
}

func TestIgnoreCodecsClosedSet(t *testing.T) {
	all, err := yaml.Marshal(map[string][]string{"ignore_codecs": embedded.Codecs})
	if err != nil {
		t.Fatal(err)
	}
	c, errs := embedded.ParseConfig(section(t, string(all)))
	if want := slices.Sorted(slices.Values(embedded.Codecs)); len(errs) > 0 || !slices.Equal(c.Ignored(), want) {
		t.Errorf("every codec: Ignored = %q, errors %v; want %q", c.Ignored(), errs, want)
	}
	_, errs = embedded.ParseConfig(section(t, "ignore_codecs: [pgs, bluray, PGS]\n"))
	var paths []string
	for _, e := range errs {
		paths = append(paths, e.Path)
	}
	if want := []string{"embedded_subtitles.ignore_codecs[1]", "embedded_subtitles.ignore_codecs[2]"}; !slices.Equal(paths, want) {
		t.Errorf("codecs outside the set: errors %v, want paths %q", errs, want)
	}
	for _, def := range embedded.DefaultIgnored {
		if !slices.Contains(embedded.Codecs, def) {
			t.Errorf("default %q is outside Codecs", def)
		}
	}
	for _, text := range embedded.TextCodecs {
		if !slices.Contains(embedded.Codecs, text) {
			t.Errorf("text codec %q is outside Codecs", text)
		}
	}
}
