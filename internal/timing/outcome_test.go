package timing_test

import (
	"encoding"
	"go/ast"
	"go/parser"
	"go/token"
	"maps"
	"os"
	"slices"
	"strings"
	"testing"
	"unicode"

	"github.com/cplieger/subflux/internal/timing"
)

// textCodec is one type's text codec over its uint8 values.
type textCodec struct {
	typ       string
	consts    map[string]uint8
	marshal   func(uint8) ([]byte, error)
	unmarshal func(start uint8, b []byte) (uint8, error)
}

func codecOf[T interface {
	~uint8
	encoding.TextMarshaler
}, P interface {
	*T
	encoding.TextUnmarshaler
}](typ string, consts map[string]T) textCodec {
	c := textCodec{typ: typ, consts: map[string]uint8{}}
	for name, v := range consts {
		c.consts[name] = uint8(v)
	}
	c.marshal = func(v uint8) ([]byte, error) { return T(v).MarshalText() }
	c.unmarshal = func(start uint8, b []byte) (uint8, error) {
		v := T(start)
		err := P(&v).UnmarshalText(b)
		return uint8(v), err
	}
	return c
}

// textCodecs maps each constant's Go name to its value; TestTextCodecsCoverConstants
// holds every table to the constants the package declares.
var textCodecs = []textCodec{
	codecOf("Action", map[string]timing.Action{
		"Skip": timing.Skip, "ActionReference": timing.ActionReference,
		"ReferenceThenAudio": timing.ReferenceThenAudio, "RigidReference": timing.RigidReference,
	}),
	codecOf("SkipReason", map[string]timing.SkipReason{
		"SkipReasonNone": timing.SkipReasonNone, "Hash": timing.Hash, "SameRelease": timing.SameRelease,
		"Forced": timing.Forced, "Disabled": timing.Disabled,
	}),
	codecOf("Method", map[string]timing.Method{
		"MethodNone": timing.MethodNone, "MethodReference": timing.MethodReference, "Audio": timing.Audio,
	}),
	codecOf("Verdict", map[string]timing.Verdict{
		"Trusted": timing.Trusted, "Verified": timing.Verified, "Unverified": timing.Unverified,
	}),
	codecOf("Format", map[string]timing.Format{"SRT": timing.SRT, "ASS": timing.ASS, "SSA": timing.SSA}),
}

// declaredConsts returns, per type name, the constants the package's
// non-test files declare of it, iota continuations included.
func declaredConsts(t *testing.T) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.CONST {
				continue
			}
			typ := ""
			for _, spec := range gen.Specs {
				vs := spec.(*ast.ValueSpec)
				switch {
				case vs.Type != nil:
					typ = ""
					if id, ok := vs.Type.(*ast.Ident); ok {
						typ = id.Name
					}
				case len(vs.Values) > 0:
					typ = "" // an untyped constant ends the iota run
				}
				for _, id := range vs.Names {
					if typ != "" && id.IsExported() {
						out[typ] = append(out[typ], id.Name)
					}
				}
			}
		}
	}
	return out
}

// textName is a constant's text: its Go name, less its type's prefix, in
// snake_case (ActionReference is "reference", SRT is "srt").
func textName(typ, name string) string {
	if rest, ok := strings.CutPrefix(name, typ); ok && rest != "" && unicode.IsUpper(rune(rest[0])) {
		name = rest
	}
	var b strings.Builder
	for i, r := range name {
		if unicode.IsUpper(r) && i > 0 && unicode.IsLower(rune(name[i-1])) {
			b.WriteByte('_')
		}
		b.WriteRune(unicode.ToLower(r))
	}
	return b.String()
}

func TestTextCodecsCoverConstants(t *testing.T) {
	t.Parallel()
	declared := declaredConsts(t)
	for _, c := range textCodecs {
		got := slices.Sorted(maps.Keys(c.consts))
		want := slices.Sorted(slices.Values(declared[c.typ]))
		if len(want) == 0 || !slices.Equal(got, want) {
			t.Errorf("%s: table %v, package declares %v", c.typ, got, want)
		}
	}
}

func TestTextCodecsRoundTrip(t *testing.T) {
	t.Parallel()
	for _, c := range textCodecs {
		t.Run(c.typ, func(t *testing.T) {
			t.Parallel()
			for name, v := range c.consts {
				text, err := c.marshal(v)
				if err != nil {
					t.Fatalf("%s.MarshalText: %v", name, err)
				}
				if want := textName(c.typ, name); string(text) != want {
					t.Errorf("%s.MarshalText = %q, want %q", name, text, want)
				}
				if got, err := c.unmarshal(^v, text); err != nil || got != v {
					t.Errorf("UnmarshalText(%q) = %d, %v; want %d", text, got, err, v)
				}
			}
		})
	}
}

func TestTextCodecsRefuseUnnamedValues(t *testing.T) {
	t.Parallel()
	for _, c := range textCodecs {
		t.Run(c.typ, func(t *testing.T) {
			t.Parallel()
			named := slices.Collect(maps.Values(c.consts))
			for v := range 256 {
				if slices.Contains(named, uint8(v)) {
					continue
				}
				text, err := c.marshal(uint8(v))
				if err == nil || !strings.Contains(err.Error(), c.typ) {
					t.Errorf("%s(%d).MarshalText = %q, %v; want an error naming %s", c.typ, v, text, err, c.typ)
				}
			}
		})
	}
}

func TestTextCodecsRefuseUnknownText(t *testing.T) {
	t.Parallel()
	for _, c := range textCodecs {
		t.Run(c.typ, func(t *testing.T) {
			t.Parallel()
			some := slices.Min(slices.Collect(maps.Values(c.consts)))
			known, err := c.marshal(some)
			if err != nil {
				t.Fatal(err)
			}
			for _, text := range []string{"", "bogus", strings.ToUpper(string(known)), string(known) + " ", "\x00"} {
				got, err := c.unmarshal(some, []byte(text))
				if err == nil || !strings.Contains(err.Error(), c.typ) {
					t.Errorf("UnmarshalText(%q) error = %v; want one naming %s", text, err, c.typ)
				}
				if got != some {
					t.Errorf("UnmarshalText(%q) changed the value to %d", text, got)
				}
			}
		})
	}
}
