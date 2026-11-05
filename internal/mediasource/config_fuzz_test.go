package mediasource_test

import (
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/mediasource"
	"go.yaml.in/yaml/v4"
)

func scalar(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

func mappingNode(kv ...*yaml.Node) *yaml.Node {
	return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: kv}
}

// FuzzParseConfigs_paths: every refusal of two fuzzed mappings sits at a
// path_mappings path; every accepted mapping maps its own From to its To
// through the Set, and no two probe paths under the Froms map to one local
// path, so an accepted mapping means what it says and stays injective.
func FuzzParseConfigs_paths(f *testing.F) {
	f.Add("/tv", "/media/tv", "/movies", "/media/movies")
	f.Add("/data", "/m", "/data/tv", "/m/tv")
	f.Add("/data", "/m", "/data/tv", "/m/shows")
	f.Add("/arr-a", "/media", "/arr-b", "/media")
	f.Add("/a", "/m", "/b", "/m/sub")
	f.Add("tv", "/x", "/tv\n", "/y")
	f.Add("/a/../b", "/c", "/\u202e", "/d")
	f.Fuzz(func(t *testing.T, from1, to1, from2, to2 string) {
		maps := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{
			mappingNode(scalar("from"), scalar(from1), scalar("to"), scalar(to1)),
			mappingNode(scalar("from"), scalar(from2), scalar("to"), scalar(to2)),
		}}
		entry := mappingNode(scalar("id"), scalar("s"), scalar("type"), scalar("sonarr"), scalar("url"), scalar("http://a"),
			scalar("api_key"), scalar("k"), scalar("path_mappings"), maps)
		section := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{entry}}
		cfgs, errs := mediasource.ParseConfigs(section, arrKinds(&arrKind{}))
		for _, e := range errs {
			if !strings.HasPrefix(e.Path, "sources[0].path_mappings[") {
				t.Fatalf("error %v outside the mappings", e)
			}
		}
		if len(errs) > 0 {
			return
		}
		set, err := mediasource.NewSet(arrKinds(&arrKind{}), cfgs, mediasource.Deps{})
		if err != nil {
			t.Fatal(err)
		}
		ms := cfgs[0].PathMappings
		probes := map[string]bool{}
		for _, a := range ms {
			if got, err := set.LocalPath("s", a.From); err != nil || got != a.To {
				t.Fatalf("LocalPath(%q) = %q %v, want its own to %q", a.From, got, err, a.To)
			}
			probes[path.Join(a.From, "f")] = true
			for _, b := range ms {
				if rel, err := filepath.Rel(a.To, b.To); err == nil && !strings.HasPrefix(rel, "..") {
					probes[path.Join(a.From, rel, "f")] = true
				}
			}
		}
		seen := map[string]string{}
		for p := range probes {
			local, err := set.LocalPath("s", p)
			if err != nil {
				continue
			}
			if q, dup := seen[local]; dup {
				t.Fatalf("%q and %q both map to %q", q, p, local)
			}
			seen[local] = p
		}
	})
}
