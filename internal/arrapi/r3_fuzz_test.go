package arrapi

import (
	"strconv"
	"strings"
	"testing"
)

func FuzzIdPath(f *testing.F) {
	f.Add("/api/v3/series/", 123, "")
	f.Add("/api/v3/episode/", 0, "/file")
	f.Add("", -1, "")
	f.Add("/x/", 999999, "/y")

	f.Fuzz(func(t *testing.T, base string, id int, suffix string) {
		result := idPath(base, id, suffix)
		// Must start with base
		if !strings.HasPrefix(result, base) {
			t.Errorf("idPath(%q, %d, %q) = %q, missing base prefix", base, id, suffix, result)
		}
		// Must end with suffix
		if !strings.HasSuffix(result, suffix) {
			t.Errorf("idPath(%q, %d, %q) = %q, missing suffix", base, id, suffix, result)
		}
		// Middle part must be the string representation of id
		middle := result[len(base) : len(result)-len(suffix)]
		want := strconv.Itoa(id)
		if middle != want {
			t.Errorf("idPath(%q, %d, %q) = %q, middle=%q want %q", base, id, suffix, result, middle, want)
		}
	})
}

func FuzzStatusErrorIsTransient(f *testing.F) {
	f.Add(200, "/api/test", "ok")
	f.Add(429, "/api/rate", "too many")
	f.Add(500, "/api/err", "internal")
	f.Add(404, "/api/miss", "not found")
	f.Add(503, "/api/down", "")

	f.Fuzz(func(t *testing.T, code int, path, body string) {
		if code < 100 || code > 599 {
			return
		}
		e := newStatusError(code, path, body)
		transient := e.IsTransient()

		// Property: transient iff code >= 500 || code == 429
		wantTransient := code >= 500 || code == 429
		if transient != wantTransient {
			t.Errorf("IsTransient() = %v for code %d, want %v", transient, code, wantTransient)
		}

		// Error message must contain the path
		if !strings.Contains(e.Error(), path) {
			t.Errorf("Error() = %q, does not contain path %q", e.Error(), path)
		}

		// If body is non-empty, message must contain it
		if body != "" && !strings.Contains(e.Error(), body) {
			t.Errorf("Error() = %q, does not contain body %q", e.Error(), body)
		}
	})
}
