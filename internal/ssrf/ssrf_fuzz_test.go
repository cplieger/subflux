package ssrf

import "testing"

func FuzzValidateURL(f *testing.F) {
	f.Add("https://example.com/path")
	f.Add("http://localhost")
	f.Add("https://127.0.0.1")
	f.Add("https://[::1]/path")
	f.Add("")
	f.Add("not-a-url")
	f.Add("https://10.0.0.1/internal")
	f.Fuzz(func(t *testing.T, raw string) {
		_ = ValidateURL(raw)
	})
}
