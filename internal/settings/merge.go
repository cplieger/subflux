package settings

import (
	"slices"
	"unicode/utf8"

	"github.com/cplieger/subflux/internal/formschema"
	"go.yaml.in/yaml/v3"
)

const (
	msgEnterValue = "no saved value is kept here; enter the value"
	msgEnterEntry = "a saved value is kept only in an entry whose id is saved once and sent once; enter the value"
	msgUnchanged  = "an unchanged value carries no text"
	msgUTF8       = "must be valid UTF-8"
	msgNoBody     = "the settings document is missing"
	msgUnreadable = "the entry cannot be read back as YAML"
	keyID         = "id"
)

// bodyShape is a Save or Merge body's node rule, refusing what the merge
// would drop or cannot encode: an anchor or alias, text on an unchanged
// scalar, invalid UTF-8. Every other tag is kept, re-encodes explicit, and
// the parse of the encoded result refuses it as the file's shape rules do.
func bodyShape(n *yaml.Node, path string) []formschema.FieldError {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return []formschema.FieldError{{Path: path, Message: msgShared}}
	}
	switch {
	case n.Tag == formschema.UnchangedTag && n.Kind == yaml.ScalarNode:
		if n.Value != "" {
			return []formschema.FieldError{{Path: path, Message: msgUnchanged}}
		}
	case n.Kind == yaml.ScalarNode && !utf8.ValidString(n.Value):
		return []formschema.FieldError{{Path: path, Message: msgUTF8}}
	}
	return nil
}

// mergeDocument is body, a whole document, with each unchanged scalar taken
// from stored, the file's mapping (nil when there is none). Save's parse of
// the result refuses a body that is not a mapping.
func mergeDocument(stored, body *yaml.Node) (*yaml.Node, []formschema.FieldError) {
	if body == nil {
		return nil, []formschema.FieldError{{Message: msgNoBody}}
	}
	if errs := walkShapes(body, "", bodyShape); len(errs) > 0 {
		return nil, errs
	}
	return mergeNode(body, stored, "")
}

// mergeNode copies b, without comments, taking each unchanged scalar from st,
// the stored node at b's position: a mapping's value by its key, and a
// sequence element only by an id held once in b's sequence and once in st's,
// so deleting or reordering entries cannot move a secret. Any other
// unchanged scalar is a field error at its path. b has passed bodyShape.
func mergeNode(b, st *yaml.Node, path string) (*yaml.Node, []formschema.FieldError) {
	switch b.Kind {
	case yaml.MappingNode:
		return mergeMapping(b, st, path)
	case yaml.SequenceNode:
		return mergeSequence(b, st, path)
	}
	if b.Tag != formschema.UnchangedTag {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: b.Tag, Style: b.Style, Value: b.Value}, nil
	}
	if st == nil || st.Kind != yaml.ScalarNode || st.Anchor != "" || st.Style&yaml.TaggedStyle != 0 {
		return nil, []formschema.FieldError{{Path: path, Message: msgEnterValue}}
	}
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: st.Tag, Style: st.Style, Value: st.Value}, nil
}

func mergeMapping(b, st *yaml.Node, path string) (*yaml.Node, []formschema.FieldError) {
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: b.Tag, Style: b.Style}
	var errs []formschema.FieldError
	for i := 0; i+1 < len(b.Content); i += 2 {
		k := b.Content[i]
		v, verrs := mergeNode(b.Content[i+1], mappingValue(st, k.Value), child(path, k.Value))
		errs = append(errs, verrs...)
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: k.Tag, Style: k.Style, Value: k.Value}, v)
	}
	return n, errs
}

func mergeSequence(b, st *yaml.Node, path string) (*yaml.Node, []formschema.FieldError) {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: b.Tag, Style: b.Style}
	var errs []formschema.FieldError
	for i, e := range b.Content {
		var se *yaml.Node
		if holdsUnchanged(e) {
			var ok bool
			if se, ok = elementByID(e, b, st); !ok {
				errs = append(errs, formschema.FieldError{Path: index(path, i), Message: msgEnterEntry})
				continue
			}
		}
		v, verrs := mergeNode(e, se, index(path, i))
		errs = append(errs, verrs...)
		n.Content = append(n.Content, v)
	}
	return n, errs
}

// mappingValue is st's value for key when st is a mapping holding key once.
func mappingValue(st *yaml.Node, key string) *yaml.Node {
	if st == nil || st.Kind != yaml.MappingNode {
		return nil
	}
	var v *yaml.Node
	for i := 0; i+1 < len(st.Content); i += 2 {
		if k := st.Content[i]; k.Kind == yaml.ScalarNode && k.Value == key {
			if v != nil {
				return nil
			}
			v = st.Content[i+1]
		}
	}
	return v
}

// elementByID is the element of st whose id is e's, when that id is held
// once in seq, e's sequence, and once in st.
func elementByID(e, seq, st *yaml.Node) (*yaml.Node, bool) {
	id, ok := entryID(e)
	if !ok || st == nil || st.Kind != yaml.SequenceNode || countID(seq, id) != 1 || countID(st, id) != 1 {
		return nil, false
	}
	for _, se := range st.Content {
		if sid, ok := entryID(se); ok && sid == id {
			return se, true
		}
	}
	return nil, false
}

// entryID is a mapping's id: a plain scalar value held once.
func entryID(n *yaml.Node) (string, bool) {
	v := mappingValue(n, keyID)
	if v == nil || v.Kind != yaml.ScalarNode || v.Tag == formschema.UnchangedTag || v.Style&yaml.TaggedStyle != 0 || v.Value == "" {
		return "", false
	}
	return v.Value, true
}

func countID(seq *yaml.Node, id string) int {
	n := 0
	for _, e := range seq.Content {
		if eid, ok := entryID(e); ok && eid == id {
			n++
		}
	}
	return n
}

func holdsUnchanged(n *yaml.Node) bool {
	return n.Kind == yaml.ScalarNode && n.Tag == formschema.UnchangedTag || slices.ContainsFunc(n.Content, holdsUnchanged)
}
