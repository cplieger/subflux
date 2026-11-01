package settings

import (
	"strconv"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

// The YAML core-schema tags the walks read.
const (
	tagMap   = "!!map"
	tagMerge = "!!merge"
	tagNull  = "!!null"
)

const (
	msgTagged  = "YAML tags are not supported; remove the tag"
	msgShared  = "YAML anchors and aliases are not supported; repeat the value or use ${SUBFLUX_...}"
	msgTwice   = "key is set twice"
	msgKeyText = "keys must be plain names"
)

// shapes is one shape walk's result: its field errors, the sections whose
// key or subtree holds one, and whether a top-level key names no section it
// can tell (a merge key or a non-text key), so no section is known absent.
type shapes struct {
	tainted map[string]bool
	errs    []formschema.FieldError
	opaque  bool
}

// checkShapes refuses, at their paths, an explicitly tagged node, a key held
// twice in one mapping, and an anchor, alias or merge key: yamlenv expands
// only untagged scalars and a tag would forge a mark, yaml.v3's Node keeps
// duplicates, and a shared node would let a mark through one path unmask a
// secret at another. It follows no alias.
func checkShapes(root *yaml.Node) shapes {
	s := shapes{tainted: map[string]bool{}}
	s.errs = fileShape(root, "")
	seen := map[string]bool{}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		name, ok := sectionName(k)
		if !ok {
			s.opaque = true
			s.errs = append(s.errs, keyShape(k, "", nil, fileShape)...)
			continue
		}
		errs := append(keyShape(k, name, seen, fileShape), walkShapes(v, name, fileShape)...)
		if len(errs) > 0 {
			s.errs = append(s.errs, errs...)
			s.tainted[name] = true
		}
	}
	return s
}

// sectionName is the section a top-level key names: its text, or an alias's
// target text. A merge key or a collection key names none.
func sectionName(k *yaml.Node) (string, bool) {
	switch {
	case k.Kind == yaml.ScalarNode && k.ShortTag() != tagMerge:
		return k.Value, true
	case k.Kind == yaml.AliasNode && k.Alias != nil && k.Alias.Kind == yaml.ScalarNode:
		return k.Alias.Value, true
	}
	return "", false
}

// nodeRule refuses one node's own shape at path.
type nodeRule func(n *yaml.Node, path string) []formschema.FieldError

// walkShapes refuses, under rule, n and everything beneath it, and repeated,
// merge and non-text keys.
func walkShapes(n *yaml.Node, path string, rule nodeRule) []formschema.FieldError {
	errs := rule(n, path)
	switch n.Kind {
	case yaml.MappingNode:
		seen := map[string]bool{}
		for i := 0; i+1 < len(n.Content); i += 2 {
			k := n.Content[i]
			if k.Kind != yaml.ScalarNode || k.ShortTag() == tagMerge {
				errs = append(errs, keyShape(k, path, nil, rule)...)
				continue
			}
			p := child(path, k.Value)
			errs = append(errs, keyShape(k, p, seen, rule)...)
			errs = append(errs, walkShapes(n.Content[i+1], p, rule)...)
		}
	case yaml.SequenceNode:
		for i, e := range n.Content {
			errs = append(errs, walkShapes(e, index(path, i), rule)...)
		}
	}
	return errs
}

// fileShape refuses n's own tag, anchor or alias; an alias is not entered.
func fileShape(n *yaml.Node, path string) []formschema.FieldError {
	var errs []formschema.FieldError
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		errs = append(errs, formschema.FieldError{Path: path, Message: msgShared})
	}
	if n.Style&yaml.TaggedStyle != 0 {
		errs = append(errs, formschema.FieldError{Path: path, Message: msgTagged})
	}
	return errs
}

// keyShape refuses a mapping key at path: a merge key, an alias, a tag, a
// collection, a repeat of a text in seen (nil for a key with no text), or a
// shape rule refuses.
func keyShape(k *yaml.Node, path string, seen map[string]bool, rule nodeRule) []formschema.FieldError {
	switch {
	case k.Kind == yaml.ScalarNode && k.ShortTag() == tagMerge:
		return []formschema.FieldError{{Path: child(path, k.Value), Message: msgShared}}
	case k.Kind == yaml.AliasNode:
		return []formschema.FieldError{{Path: path, Message: msgShared}}
	case k.Kind != yaml.ScalarNode:
		return []formschema.FieldError{{Path: path, Message: msgKeyText}}
	}
	errs := rule(k, path)
	if seen[k.Value] {
		errs = append(errs, formschema.FieldError{Path: path, Message: msgTwice})
	}
	seen[k.Value] = true
	return errs
}

func index(path string, i int) string { return path + "[" + strconv.Itoa(i) + "]" }

func child(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// deepCopy copies n's tree, each node once, so an alias in the copy points at
// the copy of its anchor and an alias bomb copies in linear time.
func deepCopy(n *yaml.Node) *yaml.Node {
	return copyNode(n, map[*yaml.Node]*yaml.Node{})
}

func copyNode(n *yaml.Node, memo map[*yaml.Node]*yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	if c, ok := memo[n]; ok {
		return c
	}
	c := *n
	memo[n] = &c
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, e := range n.Content {
		c.Content[i] = copyNode(e, memo)
	}
	c.Alias = copyNode(n.Alias, memo)
	return &c
}
