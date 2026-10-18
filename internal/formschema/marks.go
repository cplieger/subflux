package formschema

import (
	"slices"

	"go.yaml.in/yaml/v3"
)

// MarkPlain retags n, and every scalar beneath it, PlainTag. A section owner
// calls it for each non-Secret scalar it accepts without Decode, after reading
// the values: yaml.v3's node.Decode refuses a !plain scalar as a bool or int.
// It never follows an alias. A sequence element holding a Secret field must
// carry an id key, so a Save can match it to its stored secret.
func MarkPlain(n *yaml.Node) {
	if n == nil {
		return
	}
	if n.Kind == yaml.ScalarNode {
		n.Tag = PlainTag
		return
	}
	for _, c := range n.Content {
		MarkPlain(c)
	}
}

// sharesNodes reports whether n or anything beneath it is anchored or an
// alias: a mark through one path would then unmask the same node at another.
func sharesNodes(n *yaml.Node) bool {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return true
	}
	return slices.ContainsFunc(n.Content, sharesNodes)
}
