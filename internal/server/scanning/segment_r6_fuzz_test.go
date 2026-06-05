package scanning

import (
	"strings"
	"testing"
)

// FuzzExtractSegment verifies that extractSegment always returns either
// an empty string or a substring of path that does not contain "/".
//
// Bug class: path traversal — if extractSegment returned a value containing
// "/" or that was not part of the original path, downstream handlers could
// resolve unintended filesystem or API paths.
func FuzzExtractSegment(f *testing.F) {
	f.Add("/api/scan/series/12345", "/api/scan/series/")
	f.Add("/api/scan/movie/tt0903747", "/api/scan/movie/")
	f.Add("", "")
	f.Add("/a/b/c", "/a/")
	f.Add("/prefix/value", "/prefix/")
	f.Add("/no-match", "/other/")

	f.Fuzz(func(t *testing.T, path, prefix string) {
		got := extractSegment(path, prefix)
		if got == "" {
			return
		}
		if strings.Contains(got, "/") {
			t.Fatalf("extractSegment(%q, %q) = %q contains '/'", path, prefix, got)
		}
		if !strings.Contains(path, got) {
			t.Fatalf("extractSegment(%q, %q) = %q is not a substring of path", path, prefix, got)
		}
	})
}
