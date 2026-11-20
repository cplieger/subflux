package wirespec_test

import (
	"encoding"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/wirespec"
)

func jsonName(f reflect.StructField) string { return tagName(f.Name, string(f.Tag)) }

func tagName(name, tag string) string {
	if n, _, _ := strings.Cut(reflect.StructTag(tag).Get("json"), ","); n != "" {
		return n
	}
	return name
}

// checked type-checks files as the package path with the source importer.
func checked(t *testing.T, fset *token.FileSet, path string, files []*ast.File) (*types.Package, types.Importer) {
	t.Helper()
	imp := importer.ForCompiler(fset, "source", nil)
	pkg, err := (&types.Config{Importer: imp}).Check(path, fset, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pkg, imp
}

type durationCheck struct {
	textual *types.Interface
	seen    map[types.Type]bool
	faults  []string
}

func newDurationCheck(t *testing.T, imp types.Importer) *durationCheck {
	t.Helper()
	encoding, err := imp.Import("encoding")
	if err != nil {
		t.Fatal(err)
	}
	textual := encoding.Scope().Lookup("TextUnmarshaler").Type().Underlying().(*types.Interface)
	return &durationCheck{textual: textual, seen: map[types.Type]bool{}}
}

func isNamed(t types.Type, pkg, name string) bool {
	n, ok := t.(*types.Named)
	return ok && n.Obj().Pkg() != nil && n.Obj().Pkg().Path() == pkg && n.Obj().Name() == name
}

func (c *durationCheck) walk(t types.Type, path string) {
	for t = types.Unalias(t); ; {
		if isNamed(t, "time", "Duration") {
			c.faults = append(c.faults, path+" is a time.Duration")
			return
		}
		var elem types.Type
		switch u := t.Underlying().(type) {
		case *types.Pointer:
			elem = u.Elem()
		case *types.Slice:
			elem = u.Elem()
		case *types.Array:
			elem = u.Elem()
		case *types.Map:
			elem = u.Elem()
		}
		if elem == nil {
			break
		}
		t = types.Unalias(elem)
	}
	st, ok := t.Underlying().(*types.Struct)
	if !ok || types.Implements(types.NewPointer(t), c.textual) || isNamed(t, "time", "Time") || c.seen[t] {
		return
	}
	c.seen[t] = true
	for i := range st.NumFields() {
		f := st.Field(i)
		if !f.Exported() {
			continue
		}
		name := tagName(f.Name(), st.Tag(i))
		at := path + "." + name
		if types.Identical(f.Type().Underlying(), types.Typ[types.Int64]) && strings.HasSuffix(f.Name(), "Ms") && !strings.HasSuffix(name, "_ms") {
			c.faults = append(c.faults, at+" counts milliseconds without an _ms name")
		}
		c.walk(f.Type(), at)
	}
}

func (c *durationCheck) walkExported(pkg *types.Package) {
	for _, name := range pkg.Scope().Names() {
		if obj, ok := pkg.Scope().Lookup(name).(*types.TypeName); ok && obj.Exported() {
			c.walk(obj.Type(), name)
		}
	}
}

func snippetFaults(t *testing.T, src string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "snippet.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg, imp := checked(t, fset, "snippet", []*ast.File{f})
	c := newDurationCheck(t, imp)
	c.walkExported(pkg)
	return c.faults
}

func TestDurationFieldsAreMilliseconds(t *testing.T) {
	fset := token.NewFileSet()
	names, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var files []*ast.File
	for _, name := range names {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	pkg, imp := checked(t, fset, "github.com/cplieger/subflux/internal/wirespec", files)
	c := newDurationCheck(t, imp)
	c.walkExported(pkg)
	if len(c.seen) == 0 {
		t.Fatal("the walk reached no struct type")
	}
	for _, f := range c.faults {
		t.Error(f)
	}
	t.Run("a Duration field is caught", func(t *testing.T) {
		got := snippetFaults(t, "package p\nimport \"time\"\ntype nested struct{ Wait time.Duration }\ntype Doc struct{ Inner []*nested }\n")
		if len(got) != 1 {
			t.Errorf("faults = %v, want the nested Duration", got)
		}
	})
	t.Run("an Ms count without _ms is caught", func(t *testing.T) {
		got := snippetFaults(t, "package p\ntype Doc struct {\n\tDelayMs int64 `json:\"delay\"`\n}\n")
		if len(got) != 1 {
			t.Errorf("faults = %v, want the misnamed DelayMs", got)
		}
	})
}

func TestHexText(t *testing.T) {
	const h16 = "000102030405060708090a0b0c0d0e0f"
	var h wirespec.Hex16
	if err := h.UnmarshalText([]byte(h16)); err != nil {
		t.Fatalf("UnmarshalText = %v", err)
	}
	if h != (wirespec.Hex16{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}) {
		t.Errorf("decoded %v", h)
	}
	if b, err := h.MarshalText(); err != nil || string(b) != h16 {
		t.Errorf("MarshalText = %q, %v", b, err)
	}
	for name, in := range map[string]string{
		"uppercase":  strings.ToUpper(h16),
		"too short":  h16[:30],
		"too long":   h16 + "00",
		"not hex":    "zz" + h16[2:],
		"empty":      "",
		"one nibble": h16 + "0",
	} {
		t.Run("Hex16 refuses "+name, func(t *testing.T) {
			got := wirespec.Hex16{0xff}
			if err := got.UnmarshalText([]byte(in)); err == nil {
				t.Errorf("UnmarshalText(%q) accepted it", in)
			}
			if got != (wirespec.Hex16{0xff}) {
				t.Errorf("a refusal changed the value to %v", got)
			}
		})
	}
}

type textEnum interface {
	encoding.TextMarshaler
}

func TestEnumText(t *testing.T) {
	cases := map[string]struct {
		values []textEnum
		names  []string
		zero   textEnum
		decode func([]byte) (textEnum, error)
	}{
		"Scope": {
			[]textEnum{wirespec.Public, wirespec.Read, wirespec.Operate, wirespec.Admin},
			[]string{"public", "read", "operate", "admin"},
			wirespec.Scope(0),
			func(b []byte) (textEnum, error) { var v wirespec.Scope; err := v.UnmarshalText(b); return v, err },
		},
		"TestSection": {
			[]textEnum{wirespec.TestSources, wirespec.TestMediaServers, wirespec.TestProviders},
			[]string{"sources", "media_servers", "providers"},
			wirespec.TestSection(0),
			func(b []byte) (textEnum, error) { var v wirespec.TestSection; err := v.UnmarshalText(b); return v, err },
		},
		"TimingKind": {
			[]textEnum{wirespec.TimingIdentity, wirespec.TimingShift, wirespec.TimingLinear, wirespec.TimingPiecewise},
			[]string{"identity", "shift", "linear", "piecewise"},
			wirespec.TimingKind(0),
			func(b []byte) (textEnum, error) { var v wirespec.TimingKind; err := v.UnmarshalText(b); return v, err },
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			for i, v := range c.values {
				b, err := v.MarshalText()
				if err != nil || string(b) != c.names[i] {
					t.Errorf("MarshalText(%v) = %q, %v; want %q", v, b, err, c.names[i])
				}
				back, err := c.decode([]byte(c.names[i]))
				if err != nil || back != v {
					t.Errorf("UnmarshalText(%q) = %v, %v; want %v", c.names[i], back, err, v)
				}
			}
			if _, err := c.zero.MarshalText(); err == nil {
				t.Error("the zero value marshalled")
			}
			for _, bad := range []string{"", "Admin", c.names[0] + " "} {
				if _, err := c.decode([]byte(bad)); err == nil {
					t.Errorf("UnmarshalText(%q) accepted it", bad)
				}
			}
		})
	}
	if wirespec.Public >= wirespec.Read || wirespec.Read >= wirespec.Operate || wirespec.Operate >= wirespec.Admin {
		t.Error("scopes are not ordered public < read < operate < admin")
	}
}

func TestFieldErrorText(t *testing.T) {
	err := &wirespec.FieldError{Path: "ms", Message: "is required by a shift timing"}
	if got := err.Error(); got != "ms is required by a shift timing" {
		t.Errorf("Error() = %q", got)
	}
}
