package scanning

import "testing"

// extractSegment returns the single path segment that follows prefix, and ""
// for every non-segment case: the prefix is absent, nothing follows the
// prefix, or the remainder spans more than one segment.
func TestExtractSegment(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		path   string
		prefix string
		want   string
	}{
		{"valid single segment", "/api/scan/series/123", "/api/scan/series/", "123"},
		{"empty after prefix", "/api/scan/series/", "/api/scan/series/", ""},
		{"prefix absent", "/other/path", "/api/scan/series/", ""},
		{"sub-path rejected", "/api/scan/series/123/extra", "/api/scan/series/", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractSegment(tc.path, tc.prefix); got != tc.want {
				t.Errorf("extractSegment(%q, %q) = %q, want %q",
					tc.path, tc.prefix, got, tc.want)
			}
		})
	}
}
