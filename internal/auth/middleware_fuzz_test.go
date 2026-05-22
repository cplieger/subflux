package auth

import "testing"

func FuzzValidateRedirectURI(f *testing.F) {
	f.Add("/")
	f.Add("/dashboard")
	f.Add("//evil.com")
	f.Add("https://evil.com")
	f.Add("/path?q=1#frag")
	f.Add("")
	f.Add("javascript:alert(1)")

	f.Fuzz(func(t *testing.T, uri string) {
		result := ValidateRedirectURI(uri)
		// Must always return a safe relative path or "/".
		if result == "" {
			t.Error("ValidateRedirectURI returned empty string")
		}
		if len(result) > 0 && result[0] != '/' {
			t.Errorf("ValidateRedirectURI(%q) = %q, does not start with /", uri, result)
		}
		if len(result) > 1 && result[1] == '/' {
			t.Errorf("ValidateRedirectURI(%q) = %q, starts with // (open redirect)", uri, result)
		}
	})
}
