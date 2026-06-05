package main

import (
	"strings"
	"testing"
)

func FuzzIsIdentReferenced(f *testing.F) {
	f.Add("const foo = bar(baz)", "foo")
	f.Add("const foobar = 1", "foo")
	f.Add("x.foo.y", "foo")
	f.Add("", "foo")
	f.Add("foo", "")
	f.Add("afoo foob foo", "foo")

	f.Fuzz(func(t *testing.T, body, ident string) {
		if ident == "" {
			return
		}
		result := isIdentReferenced(body, ident)

		// If body doesn't contain ident at all, must be false
		if !strings.Contains(body, ident) {
			if result {
				t.Errorf("isIdentReferenced(%q, %q) = true but body doesn't contain ident", body, ident)
			}
		}
	})
}

func FuzzIsIdentChar(f *testing.F) {
	f.Add(byte('a'))
	f.Add(byte('Z'))
	f.Add(byte('0'))
	f.Add(byte('_'))
	f.Add(byte('$'))
	f.Add(byte(' '))
	f.Add(byte('.'))

	f.Fuzz(func(t *testing.T, c byte) {
		result := isIdentChar(c)
		// Property: must be true for [a-zA-Z0-9_$] and false otherwise
		expected := (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '$'
		if result != expected {
			t.Errorf("isIdentChar(%q) = %v, want %v", c, result, expected)
		}
	})
}

func FuzzDecoderName(f *testing.F) {
	f.Add("SearchResponse")
	f.Add("Stats")
	f.Add("")
	f.Add("a")

	f.Fuzz(func(t *testing.T, typeName string) {
		result := decoderName(typeName)
		// Must start with "decode" prefix
		if !strings.HasPrefix(result, "decode") {
			t.Errorf("decoderName(%q) = %q, missing 'decode' prefix", typeName, result)
		}
	})
}
