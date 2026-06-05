package server

import "testing"

func FuzzExtractPathSegment(f *testing.F) {
	f.Add("/api/v1/", "items/", "/suffix")
	f.Add("/prefix/", "hello", "")
	f.Add("", "", "")
	f.Add("/a/b/c", "/b/", "/c")

	f.Fuzz(func(t *testing.T, path, prefix, suffix string) {
		_ = extractPathSegment(path, prefix, suffix)
	})
}
