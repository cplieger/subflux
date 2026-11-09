// Package sentineltest checks an owner package's Codes table against the
// sentinels the package declares, for that package's tests.
package sentineltest

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"slices"
	"strings"
	"testing"
)

// Code is the element type of every owner's Codes table.
type Code = struct {
	Err  error
	Code string
}

// CodeOf is the code of the first entry of codes that err matches by
// errors.Is, "" when none does: the lookup api's error mapping makes.
func CodeOf(codes []Code, err error) string {
	i := slices.IndexFunc(codes, func(c Code) bool { return errors.Is(err, c.Err) })
	if i < 0 {
		return ""
	}
	return codes[i].Code
}

// Placed fails t unless the exported Err* vars declared in the non-test Go
// files of the working directory, which go test sets to the package's, are
// exactly the keys of sentinels, and each is either an entry of codes or a
// key of outside, never both. outside maps a name to why it is left out.
func Placed(t testing.TB, codes []Code, sentinels map[string]error, outside map[string]string) {
	t.Helper()
	declared := declaredSentinels(t)
	for _, name := range declared {
		v, ok := sentinels[name]
		if !ok {
			t.Errorf("%s is declared but missing from the test's sentinels", name)
			continue
		}
		_, left := outside[name]
		coded := slices.ContainsFunc(codes, func(c Code) bool { return c.Err == v })
		if coded == left {
			t.Errorf("%s: in Codes %v, left outside %v; want exactly one", name, coded, left)
		}
	}
	for name := range sentinels {
		if !slices.Contains(declared, name) {
			t.Errorf("the test's sentinels name %s, which the package does not declare", name)
		}
	}
	for name := range outside {
		if _, ok := sentinels[name]; !ok {
			t.Errorf("outside names %s, which is not a sentinel", name)
		}
	}
}

// declaredSentinels parses the files, since Go cannot list a package's vars
// by reflection.
func declaredSentinels(t testing.TB) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var declared []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), e.Name(), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		declared = append(declared, sentinelsOf(file)...)
	}
	if len(declared) == 0 {
		t.Fatal("found no exported Err* var in the package; the parse is broken")
	}
	return declared
}

// sentinelsOf is file's exported package-level Err* vars.
func sentinelsOf(file *ast.File) []string {
	var names []string
	for _, decl := range file.Decls {
		if gen, ok := decl.(*ast.GenDecl); ok && gen.Tok == token.VAR {
			names = append(names, exportedErrs(gen.Specs)...)
		}
	}
	return names
}

func exportedErrs(specs []ast.Spec) []string {
	var names []string
	for _, spec := range specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, id := range vs.Names {
			if id.IsExported() && strings.HasPrefix(id.Name, "Err") {
				names = append(names, id.Name)
			}
		}
	}
	return names
}
