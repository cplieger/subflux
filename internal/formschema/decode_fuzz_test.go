package formschema

import (
	"testing"

	"go.yaml.in/yaml/v3"
)

func FuzzDecode_secretNeverMarked(f *testing.F) {
	for _, seed := range []string{
		"key: s3cret\nname: x\n",
		"name: &a s3cret\nkey: *a\n",
		"key: [a, b]\nlist: [c]\n",
		"key: {x: y}\nname: [1, 2]\n",
		"list: &l [a]\nkey: *l\n",
		"key: ~\nn: 0x1F\n",
		"- key\n",
		"key: 1\nkey: 2\nname: 3\n",
	} {
		f.Add([]byte(seed))
	}
	s := Schema{Fields: []Field{
		{Key: "key", Kind: Secret},
		{Key: "name", Kind: String},
		{Key: "n", Kind: Int},
		{Key: "list", Kind: StringList},
		{Key: "on", Kind: Bool, Default: "true"},
	}}
	f.Fuzz(func(t *testing.T, src []byte) {
		var doc yaml.Node
		if yaml.Unmarshal(src, &doc) != nil || len(doc.Content) == 0 {
			return
		}
		root := doc.Content[0]
		var secrets []*yaml.Node
		if root.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(root.Content); i += 2 {
				if root.Content[i].Value == "key" {
					secrets = append(secrets, root.Content[i+1])
				}
			}
		}
		s.Decode(root)
		for _, v := range secrets {
			if hasMarkedScalar(v, "") {
				t.Fatalf("a Secret field's scalar carries %s", PlainTag)
			}
		}
	})
}

// hasMarkedScalar reports a PlainTag scalar beneath n, following aliases;
// value "" matches any text.
func hasMarkedScalar(n *yaml.Node, value string) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.AliasNode {
		return hasMarkedScalar(n.Alias, value)
	}
	if n.Kind == yaml.ScalarNode && n.Tag == PlainTag && (value == "" || n.Value == value) {
		return true
	}
	for _, c := range n.Content {
		if hasMarkedScalar(c, value) {
			return true
		}
	}
	return false
}
