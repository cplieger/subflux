package confighandlers

import (
	"bytes"
	"testing"
)

func FuzzRedactSecretsIdempotent(f *testing.F) {
	f.Add([]byte("api_key: mysecret\n"))
	f.Add([]byte("totp_key: abc\nopensubtitles_password: pw\n"))
	f.Add([]byte("no_secrets: true\n"))
	f.Add([]byte("api_key: \"quoted value\"\n"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		once := RedactSecrets(data)
		twice := RedactSecrets(once)
		if !bytes.Equal(once, twice) {
			t.Errorf("RedactSecrets not idempotent:\n  once=%q\n twice=%q", once, twice)
		}
	})
}
