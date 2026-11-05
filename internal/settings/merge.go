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
// scalar, a tag (bodyTagged), invalid UTF-8.
func bodyShape(n *yaml.Node, path string) []formschema.FieldError {
	if n.Kind == yaml.AliasNode || n.Anchor != "" {
		return []formschema.FieldError{{Path: path, Message: msgShared}}
	}
	switch {
	case n.Tag == formschema.UnchangedTag && n.Kind == yaml.ScalarNode:
		if n.Value != "" {
			return []formschema.FieldError{{Path: path, Message: msgUnchanged}}
		}
	case bodyTagged(n):
		return []formschema.FieldError{{Path: path, Message: msgTagged}}
	case n.Kind == yaml.ScalarNode && !utf8.ValidString(n.Value):
		return []formschema.FieldError{{Path: path, Message: msgUTF8}}
	}
	return nil
}

// bodyTagged reports a tag written explicit, the non-specific ! (which v4
// writes back unresolved), or a collection tagged as another kind. A scalar's
// own tag is checked against the written file by retagged.
func bodyTagged(n *yaml.Node) bool {
	if n.Style&yaml.TaggedStyle != 0 || n.Tag == "!" {
		return true
	}
	return n.Kind != yaml.ScalarNode && n.Tag != "" && n.ShortTag() != (&yaml.Node{Kind: n.Kind}).ShortTag()
}

// settleTags refuses, at its path, each tagged scalar of merged whose reread
// (merged written by encode and parsed again) has another tag: v4 drops a
// mismatched !!int or !!float (internal/libyaml/desolver.go), and both
// majors quote text holding a ':' in a flow collection. v3 writes a mismatch
// explicit, which the file's own shape walk refuses. A mismatch inside a flow
// collection first rewrites the flow collections on its path block style,
// which keeps a file's own timestamp a timestamp.
func settleTags(merged *yaml.Node, path string) []formschema.FieldError {
	bad, ok := mismatches(merged, path)
	if !ok || len(bad) == 0 {
		return nil
	}
	reflowed := false
	for _, b := range bad {
		for _, c := range b.flow {
			c.Style &^= yaml.FlowStyle
			reflowed = true
		}
	}
	if reflowed {
		if bad, ok = mismatches(merged, path); !ok {
			return nil
		}
	}
	errs := make([]formschema.FieldError, 0, len(bad))
	for _, b := range bad {
		errs = append(errs, formschema.FieldError{Path: b.path, Message: msgTagged})
	}
	return errs
}

// mismatches is retagged over merged's reread. ok is false when merged does
// not read back, which the caller's own parse of the written file reports.
func mismatches(merged *yaml.Node, path string) ([]mismatch, bool) {
	back, err := reread(merged)
	if err != nil {
		return nil, false
	}
	return retagged(merged, back, path, nil), true
}

// readsAnother reports whether back, scalar m reread, reads another type than
// m's tag, or than null for an untagged empty plain m, which the encoder quotes
// in flow.
func readsAnother(m, back *yaml.Node) bool {
	if m.Tag == "" && m.Style == 0 && m.Value == "" {
		return back.ShortTag() != tagNull
	}
	return m.Tag != "" && m.ShortTag() != back.ShortTag()
}

// mismatch is a scalar retagged found, with the collections from the
// outermost flow one holding it down to its parent (none in block context).
type mismatch struct {
	path string
	flow []*yaml.Node
}

func retagged(m, back *yaml.Node, path string, flow []*yaml.Node) []mismatch {
	if m == nil || back == nil || m.Kind != back.Kind {
		return nil
	}
	if m.Kind != yaml.ScalarNode && (len(flow) > 0 || m.Style&yaml.FlowStyle != 0) {
		flow = append(slices.Clip(flow), m)
	}
	var bad []mismatch
	switch m.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(m.Content) && i+1 < len(back.Content); i += 2 {
			at := child(path, m.Content[i].Value)
			bad = append(bad, retagged(m.Content[i], back.Content[i], at, flow)...)
			bad = append(bad, retagged(m.Content[i+1], back.Content[i+1], at, flow)...)
		}
	case yaml.SequenceNode:
		for i := range min(len(m.Content), len(back.Content)) {
			bad = append(bad, retagged(m.Content[i], back.Content[i], index(path, i), flow)...)
		}
	case yaml.ScalarNode:
		if readsAnother(m, back) {
			bad = append(bad, mismatch{flow: flow, path: path})
		}
	}
	return bad
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
	merged, errs := mergeNode(body, stored, "")
	if len(errs) > 0 {
		return nil, errs
	}
	if errs := settleTags(merged, ""); len(errs) > 0 {
		return nil, errs
	}
	return merged, nil
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
		n.Content = append(n.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: k.Tag, Style: k.Style, Value: k.Value}, inFlow(n, v))
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
		n.Content = append(n.Content, inFlow(n, v))
	}
	return n, errs
}

// inFlow is v as parent's child: inside a flow collection an empty plain null
// becomes ~, because the encoder writes an empty plain scalar there as a
// quoted empty string, which reads back a string.
func inFlow(parent, v *yaml.Node) *yaml.Node {
	if v == nil || parent.Style&yaml.FlowStyle == 0 || v.Kind != yaml.ScalarNode || v.Value != "" || v.Style != 0 {
		return v
	}
	if v.Tag == "" || v.ShortTag() == tagNull {
		v.Value = "~"
	}
	return v
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
