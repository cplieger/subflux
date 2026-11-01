package formschema

import (
	"maps"
	"slices"
	"testing"
)

func TestMarkPlainThenNodeDecodeFails(t *testing.T) {
	n := section(t, "true")
	MarkPlain(n)
	var b bool
	if err := n.Decode(&b); err == nil {
		t.Fatal("node.Decode read a !plain scalar as a bool; a hand-walked section may then mark before reading")
	}
	m := section(t, "a: [x, {b: 1}]\nc: d\n")
	MarkPlain(m)
	for _, s := range []string{"x", "1", "d"} {
		if !hasMarkedScalar(m, s) {
			t.Errorf("scalar %q beneath the node is unmarked", s)
		}
	}
}

func TestDecodeReadsMarkedScalars(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "on", Kind: Bool}, {Key: "n", Kind: Int}, {Key: "d", Kind: DurationMs}, {Key: "l", Kind: StringList}, {Key: "e", Kind: Enum, Enum: []string{"a"}}}}
	m := section(t, "on: true\nn: 5\nd: 2s\nl: [x]\ne: a\n")
	first, errs := s.Decode(m)
	if len(errs) != 0 {
		t.Fatalf("errors: %v", errs)
	}
	again, errs := s.Decode(m)
	if len(errs) != 0 || !maps.EqualFunc(first, again, valueEqual) {
		t.Errorf("decoding the marked node: %v, %v; want %v", again, errs, first)
	}
}

func TestDecodeMarksNonSecretOnly(t *testing.T) {
	s := Schema{Fields: []Field{{Key: "key", Kind: Secret}, {Key: "url", Kind: String}, {Key: "n", Kind: Int}, {Key: "l", Kind: StringList}, {Key: "bad", Kind: Int}}}
	m := section(t, "key: s3cret\nurl: http://x\nn: 3\nl: [a, b]\nbad: [1]\nother: leaked\n")
	if _, errs := s.Decode(m); len(errs) != 2 {
		t.Fatalf("errors = %v, want bad and other", errs)
	}
	if valueNode(m, "key").Tag == PlainTag {
		t.Error("the Secret scalar was marked")
	}
	for _, k := range []string{"url", "n"} {
		if valueNode(m, k).Tag != PlainTag {
			t.Errorf("%s is unmarked although another field failed", k)
		}
	}
	for _, e := range valueNode(m, "l").Content {
		if e.Tag != PlainTag {
			t.Errorf("list element %q is unmarked", e.Value)
		}
	}
	if valueNode(m, "bad").Content[0].Tag == PlainTag {
		t.Error("a value of the wrong shape was marked")
	}
	if valueNode(m, "other").Tag == PlainTag {
		t.Error("an unknown key's value was marked")
	}
}

func valueEqual(a, b Value) bool {
	return a.kind == b.kind && a.text == b.text && a.b == b.b && a.i == b.i && a.d == b.d && slices.Equal(a.list, b.list)
}
