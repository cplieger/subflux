package auth

import "testing"

func FuzzValidatePasswordContext(f *testing.F) {
	f.Add("MyP@ssw0rd!", "admin")
	f.Add("subflux123", "user")
	f.Add("admin", "admin")
	f.Add("", "")
	f.Add("s u b f l u x", "nobody")

	f.Fuzz(func(t *testing.T, password, username string) {
		err := ValidatePasswordContext(password, username)
		// If password contains username (case-insensitive) and username is non-empty,
		// or password contains "subflux" (case-insensitive), error is expected.
		// We only verify no panic; semantics tested elsewhere.
		_ = err
	})
}

func FuzzValidatePasswordLength(f *testing.F) {
	f.Add("short", true)
	f.Add("areallylongpasswordthatshouldbefine", false)
	f.Add("", true)
	f.Add("12345678901234567890", true)

	f.Fuzz(func(t *testing.T, password string, passwordOnly bool) {
		err := ValidatePasswordLength(password, passwordOnly)
		_ = err
	})
}
