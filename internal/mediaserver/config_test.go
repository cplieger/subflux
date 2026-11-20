package mediaserver_test

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"github.com/cplieger/subflux/internal/mediaserver"
	"github.com/cplieger/subflux/internal/mediasource"
)

func kinds() []mediaserver.AdapterKind { return []mediaserver.AdapterKind{fakeKind(nil)} }

func TestParseConfigsRefusals(t *testing.T) {
	doc := `
- {id: Plex, type: fake, url: 'http://a'}
- {id: dup, type: fake, url: 'http://a'}
- {id: dup, type: fake, url: 'http://a'}
- {id: s, type: sonarr, url: 'http://a'}
- {id: rel, type: fake, url: 'http://a', path_mappings: [{from: tv, to: /media}]}
- {id: lf, type: fake, url: 'http://a', path_mappings: [{from: "/tv\nx", to: /media}]}
- {id: rep, type: fake, url: 'http://a', path_mappings: [{from: /tv, to: /m1}, {from: /tv, to: /m2}]}
- {id: extra, type: fake, url: 'http://a', colour: red}
- {id: bad, type: fake, url: bad}
- {id: onoff, type: fake, url: 'http://a', enabled: maybe}
- {id: tags, type: fake, url: 'http://a', exclude_tags: []}
`
	_, errs := mediaserver.ParseConfigs(parseYAML(t, doc), kinds())
	want := []string{
		"media_servers",
		"media_servers[0].id",
		"media_servers[2].id",
		"media_servers[3].type",
		"media_servers[4].path_mappings[0].from",
		"media_servers[5].path_mappings[0].from",
		"media_servers[6].path_mappings[1].from",
		"media_servers[7].colour",
		"media_servers[8]",
		"media_servers[9].enabled",
		"media_servers[10].exclude_tags",
	}
	for _, p := range want {
		if _, ok := errorAt(errs, p); !ok {
			t.Errorf("no error at %s", p)
		}
	}
	if len(errs) != len(want) {
		t.Errorf("%d errors, want %d: %v", len(errs), len(want), errs)
	}
	if msg, _ := errorAt(errs, "media_servers"); msg != "must list at most 8 media servers" {
		t.Errorf("count message %q", msg)
	}
	if msg, _ := errorAt(errs, "media_servers[3].type"); msg != "must be one of fake" {
		t.Errorf("unknown type message %q, want the known types listed", msg)
	}
	if msg, _ := errorAt(errs, "media_servers[8]"); msg != errBadURL.Error() {
		t.Errorf("New's refusal %q, want its error", msg)
	}
}

// TestEqualToRefused: two server paths mapped to one local path, so a local
// path would not tell which server path it came from.
func TestEqualToRefused(t *testing.T) {
	_, errs := mediaserver.ParseConfigs(parseYAML(t, `
- {id: p, type: fake, url: 'http://a', path_mappings: [{from: /srv-a, to: /media}, {from: /srv-b, to: /media}]}
`), kinds())
	if _, ok := errorAt(errs, "media_servers[0].path_mappings[1].to"); !ok || len(errs) != 1 {
		t.Errorf("errors %v, want one at path_mappings[1].to", errs)
	}
}

// TestSwappedInjectivity: Refresh maps a local path back to the server's, so
// two local paths must never map to one server path either.
func TestSwappedInjectivity(t *testing.T) {
	refused := map[string]string{
		"equal from":      `[{from: /srv, to: /a}, {from: /srv, to: /b}]`,
		"from not nested": `[{from: /srv, to: /a}, {from: /srv/tv, to: /b}]`,
	}
	for name, maps := range refused {
		t.Run(name, func(t *testing.T) {
			_, errs := mediaserver.ParseConfigs(parseYAML(t, `[{id: p, type: fake, url: 'http://a', path_mappings: `+maps+`}]`), kinds())
			if len(errs) != 1 || !strings.HasPrefix(errs[0].Path, "media_servers[0].path_mappings[1].") {
				t.Errorf("errors %v, want one at path_mappings[1]", errs)
			}
		})
	}
	accepted := map[string]string{
		"siblings":    `[{from: /srv/tv, to: /tv}, {from: /srv/movies, to: /movies}]`,
		"nested both": `[{from: /srv, to: /m}, {from: /srv/tv, to: /m/tv}]`,
	}
	for name, maps := range accepted {
		t.Run(name, func(t *testing.T) {
			got, errs := mediaserver.ParseConfigs(parseYAML(t, `[{id: p, type: fake, url: 'http://a', path_mappings: `+maps+`}]`), kinds())
			if len(errs) != 0 || len(got) != 1 || len(got[0].PathMappings) != 2 {
				t.Errorf("ParseConfigs = %v %v, want the entry with both mappings", got, errs)
			}
		})
	}
}

func TestParseConfigsAccepts(t *testing.T) {
	n := parseYAML(t, `
- id: plex
  type: fake
  url: http://plex:32400
  token: secret
  path_mappings:
    - {from: /data/tv, to: /media/tv}
- {id: off, type: fake, url: 'http://a', enabled: false}
`)
	got, errs := mediaserver.ParseConfigs(n, kinds())
	if len(errs) != 0 {
		t.Fatalf("errors %v", errs)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want the enabled one", len(got))
	}
	c := got[0]
	if c.ID != "plex" || c.Type != "fake" || c.Values.String("url") != "http://plex:32400" || c.Values.Secret("token") != "secret" {
		t.Errorf("entry %+v", c)
	}
	if len(c.PathMappings) != 1 || c.PathMappings[0] != (mediasource.PathMapping{From: "/data/tv", To: "/media/tv"}) {
		t.Errorf("mappings %v", c.PathMappings)
	}
	entry := n.Content[0]
	for i := 0; i+1 < len(entry.Content); i += 2 {
		switch k, v := entry.Content[i].Value, entry.Content[i+1]; k {
		case "id", "type":
			if v.Tag != formschema.PlainTag {
				t.Errorf("%s tagged %q, want plain", k, v.Tag)
			}
		case "path_mappings":
			if from := v.Content[0].Content[1]; from.Tag != formschema.PlainTag {
				t.Errorf("a mapping's from tagged %q, want plain", from.Tag)
			}
		}
	}
}

func TestParseConfigsAbsent(t *testing.T) {
	for name, doc := range map[string]string{"absent": "", "null": "null"} {
		t.Run(name, func(t *testing.T) {
			got, errs := mediaserver.ParseConfigs(parseYAML(t, doc), kinds())
			if got != nil || errs != nil {
				t.Errorf("ParseConfigs = %v %v, want no servers", got, errs)
			}
		})
	}
	if _, errs := mediaserver.ParseConfigs(parseYAML(t, "{plex: 1}"), kinds()); len(errs) != 1 || errs[0].Path != "media_servers" {
		t.Errorf("a mapping section: %v, want one error at media_servers", errs)
	}
}
