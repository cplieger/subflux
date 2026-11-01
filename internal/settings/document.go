package settings

import (
	"regexp"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

// wholeReference is a scalar that is exactly one ${NAME}, yamlenv's grammar.
var wholeReference = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

// Document is a fresh deep copy, on every call, of the file as written,
// failing closed: a scalar keeps its text only where its section's parse
// marked it formschema.PlainTag or where it is wholly one ${NAME}; every
// other scalar is an empty scalar tagged formschema.UnchangedTag. Comments,
// anchors and aliases are dropped. It is nil when the file is absent or not
// one mapping. Revision names the same reading.
func (s Snapshot) Document() *yaml.Node {
	if s.p == nil || s.p.view == nil {
		return nil
	}
	return deepCopy(s.p.view)
}

// Revision names this reading of the file; a Save from a Document of another
// reading is refused with ErrConflict. It is "" for the zero Snapshot.
func (s Snapshot) Revision() string {
	if s.p == nil {
		return ""
	}
	return s.p.revision
}

// view builds Document's node from stored, the file's mapping, and parsed,
// its expanded copy carrying the section parsers' marks. The two trees have
// one shape, so a node's counterpart is at the same position; where they
// differ the scalar fails closed. A mapping pair whose key is not plain text
// is left out.
func view(stored, parsed *yaml.Node) *yaml.Node {
	switch stored.Kind {
	case yaml.ScalarNode:
		if stored.Style&yaml.TaggedStyle == 0 && (marked(parsed) || wholeReference.MatchString(stored.Value)) {
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: stored.Tag, Style: stored.Style, Value: stored.Value}
		}
	case yaml.MappingNode:
		n := &yaml.Node{Kind: yaml.MappingNode, Style: stored.Style & yaml.FlowStyle}
		for i := 0; i+1 < len(stored.Content); i += 2 {
			k := stored.Content[i]
			if k.Kind != yaml.ScalarNode || k.ShortTag() == tagMerge {
				continue
			}
			key := &yaml.Node{Kind: yaml.ScalarNode, Value: k.Value}
			n.Content = append(n.Content, key, view(stored.Content[i+1], counterpart(parsed, i+1)))
		}
		return n
	case yaml.SequenceNode:
		n := &yaml.Node{Kind: yaml.SequenceNode, Style: stored.Style & yaml.FlowStyle}
		for i, e := range stored.Content {
			n.Content = append(n.Content, view(e, counterpart(parsed, i)))
		}
		return n
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: formschema.UnchangedTag}
}

// marked reports whether a section parser marked n, a parse-copy scalar. A
// file's own !plain is refused by the shape walk, and view checks the stored
// node carries no explicit tag, so a mark here comes only from a parser.
func marked(n *yaml.Node) bool {
	return n != nil && n.Kind == yaml.ScalarNode && n.Tag == formschema.PlainTag
}

func counterpart(parsed *yaml.Node, i int) *yaml.Node {
	if parsed == nil || i >= len(parsed.Content) {
		return nil
	}
	return parsed.Content[i]
}
