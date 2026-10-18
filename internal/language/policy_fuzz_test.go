package language

import (
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

func FuzzParsePolicy_paths(f *testing.F) {
	for _, doc := range []string{
		validSection, chainSection, "languages:\n", "languages: [en]\n", "languages: &a {default: *a}\n",
		"languages:\n  default:\n    - tag: !!int 1\n      variants: [hi, hi, sdh]\n",
		"languages:\n  rules:\n    - audio: ja\n      targets: {tag: en}\n    - audio: ja\n",
		"languages:\n  ? [a]\n  : 1\n  default:\n    - ? {b: c}\n      : d\n",
		"languages:\n  close_varieties: ~\n  default: [{tag: cht}, {tag: pb}, {tag: zh}, {tag: \"\"}]\n",
	} {
		f.Add(doc)
	}
	f.Fuzz(func(t *testing.T, doc string) {
		var root yaml.Node
		if yaml.Unmarshal([]byte(doc), &root) != nil || len(root.Content) == 0 {
			return
		}
		n := root.Content[0]
		if n.Kind == yaml.MappingNode && len(n.Content) == 2 && n.Content[0].Value == sectionKey {
			n = n.Content[1]
		}
		p, errs := ParsePolicy(n)
		for _, e := range errs {
			if e.Path != sectionKey && !strings.HasPrefix(e.Path, sectionKey+".") {
				t.Fatalf("error path %q is not under %s", e.Path, sectionKey)
			}
		}
		if len(errs) > 0 && p.Digest() != zeroDigest {
			t.Fatalf("a refused section answered a non-zero Policy")
		}
		walkScalars(n, func(s *yaml.Node) {
			if s.Tag != formschema.PlainTag {
				t.Fatalf("scalar %q left tagged %q", s.Value, s.Tag)
			}
		})
	})
}
