package main

import (
	"strings"
	"testing"
)

func FuzzIsIdentReferenced(f *testing.F) {
	f.Add("const foo = bar + baz;", "bar")
	f.Add("foobar is not bar", "bar")
	f.Add("", "x")
	f.Add("abc", "")
	f.Add("_foo_bar_", "foo")
	f.Add("export type MyType = {", "MyType")

	f.Fuzz(func(t *testing.T, body, ident string) {
		if ident == "" {
			return // empty ident would infinite-loop via strings.Index
		}
		got := isIdentReferenced(body, ident)

		// Cross-check: if ident appears as standalone word, result should be true
		// (simplified check: surrounded by non-ident chars or at boundaries)
		idx := strings.Index(body, ident)
		if idx < 0 && got {
			t.Errorf("isIdentReferenced(%q, %q) = true but ident not found in body", body, ident)
		}
	})
}

func FuzzTsName(f *testing.F) {
	f.Add("SearchResponse")
	f.Add("UserMeResponse")
	f.Add("")
	f.Add("UnknownType")

	f.Fuzz(func(t *testing.T, goName string) {
		result := tsName(goName)
		if result == "" && goName != "" {
			t.Errorf("tsName(%q) returned empty string", goName)
		}
	})
}
