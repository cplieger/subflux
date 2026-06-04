package cliparse

import (
	"strings"
	"testing"
)

func FuzzParseArgs(f *testing.F) {
	f.Add("--host localhost --port 8080")
	f.Add("--download --lang en")
	f.Add("")
	f.Add("--key=value")
	f.Add("--flag")

	f.Fuzz(func(t *testing.T, input string) {
		args := strings.Fields(input)
		params, _ := ParseArgs(args)
		if params == nil {
			t.Fatal("ParseArgs returned nil map")
		}
	})
}

func FuzzValidate(f *testing.F) {
	f.Add("--host localhost --port 8080", "host", "localhost", "port", "8080")
	f.Add("--unknown foo", "", "", "", "")
	f.Add("--hst localhost", "", "", "", "")

	spec := &Spec{
		Name: "test",
		Flags: []Flag{
			{Name: "host", Type: "string"},
			{Name: "port", Type: "int"},
			{Name: "timeout", Type: "duration"},
			{Name: "verbose", Type: "bool"},
		},
	}

	f.Fuzz(func(t *testing.T, rawArgs, k1, v1, k2, v2 string) {
		args := strings.Fields(rawArgs)
		params := make(map[string]string)
		if k1 != "" {
			params[k1] = v1
		}
		if k2 != "" {
			params[k2] = v2
		}
		// Must not panic.
		_ = Validate(args, params, spec)
	})
}

func FuzzEditDistance(f *testing.F) {
	f.Add("host", "hst")
	f.Add("", "abc")
	f.Add("abc", "")
	f.Add("same", "same")

	f.Fuzz(func(t *testing.T, a, b string) {
		d := editDistance(a, b)
		if d < 0 {
			t.Fatalf("editDistance(%q, %q) = %d, want >= 0", a, b, d)
		}
		// Symmetry property.
		if d != editDistance(b, a) {
			t.Fatalf("editDistance not symmetric: (%q,%q)=%d vs (%q,%q)=%d", a, b, d, b, a, editDistance(b, a))
		}
	})
}
