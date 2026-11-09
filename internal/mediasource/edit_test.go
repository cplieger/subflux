package mediasource_test

import (
	"testing"

	"github.com/cplieger/subflux/internal/mediasource"
	"go.yaml.in/yaml/v4"
)

// entries parses text, a sources section, with kinds.
func entries(t *testing.T, kinds []mediasource.AdapterKind, text string) []mediasource.Config {
	t.Helper()
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatal(err)
	}
	cfgs, errs := mediasource.ParseConfigs(doc.Content[0], kinds)
	if len(errs) > 0 {
		t.Fatal(errs)
	}
	return cfgs
}

func TestCompareEntry(t *testing.T) {
	k := &arrKind{}
	kinds := []mediasource.AdapterKind{k.kind("sonarr"), k.kind("radarr")}
	const prev = "[{id: a, type: sonarr, url: 'http://s:1', api_key: k1}]"
	cases := []struct {
		name, cur string
		want      mediasource.EntryEdit
	}{
		{"identical", prev, mediasource.EntrySame},
		{"moved behind another entry", "[{id: b, type: radarr, url: 'http://r:1', api_key: k}, {id: a, type: sonarr, url: 'http://s:1', api_key: k1}]", mediasource.EntrySame},
		{"a Secret value", "[{id: a, type: sonarr, url: 'http://s:1', api_key: k2}]", mediasource.EntryRekeyed},
		{"the url", "[{id: a, type: sonarr, url: 'http://s:2', api_key: k1}]", mediasource.EntryChanged},
		{"the url and a Secret value", "[{id: a, type: sonarr, url: 'http://s:2', api_key: k2}]", mediasource.EntryChanged},
		{"an optional value set", "[{id: a, type: sonarr, url: 'http://s:1', api_key: k1, public_url: 'https://tv'}]", mediasource.EntryChanged},
		{"the type", "[{id: a, type: radarr, url: 'http://s:1', api_key: k1}]", mediasource.EntryChanged},
		{"a path mapping", "[{id: a, type: sonarr, url: 'http://s:1', api_key: k1, path_mappings: [{from: /tv, to: /media}]}]", mediasource.EntryChanged},
		{"the exclude tags", "[{id: a, type: sonarr, url: 'http://s:1', api_key: k1, exclude_tags: []}]", mediasource.EntryChanged},
	}
	p := entries(t, kinds, prev)[0]
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cur := entries(t, kinds, c.cur)
			at := len(cur) - 1
			if got := mediasource.CompareEntry(kinds, &p, &cur[at]); got != c.want {
				t.Errorf("CompareEntry = %d, want %d", got, c.want)
			}
		})
	}
	t.Run("a type kinds lacks", func(t *testing.T) {
		cur := entries(t, kinds, prev)[0]
		if got := mediasource.CompareEntry(kinds[1:], &p, &cur); got != mediasource.EntryChanged {
			t.Errorf("CompareEntry = %d, want EntryChanged", got)
		}
	})
}
