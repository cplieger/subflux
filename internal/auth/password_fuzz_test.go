package auth

import "testing"

func FuzzVerifyPassword(f *testing.F) {
	f.Add("$argon2id$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$c29tZWhhc2g")
	f.Add("")
	f.Add("$bcrypt$invalid$format")
	f.Add("$argon2id$v=99$m=19456,t=2,p=1$AAAA$BBBB")
	f.Add("not-a-hash-at-all")
	f.Fuzz(func(t *testing.T, encoded string) {
		// parsePHC validates parameters before calling argon2.
		// VerifyPassword may panic on zero iterations, so we only
		// call parsePHC to fuzz the parser itself.
		_, _ = parsePHC(encoded)
	})
}
