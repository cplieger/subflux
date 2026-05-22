package confighandlers

import (
	"testing"
)

func FuzzRedactSecrets(f *testing.F) {
	f.Add([]byte("api_key: mysecret\n"))
	f.Add([]byte("totp_key: abc123\nopensubtitles_password: hunter2\n"))
	f.Add([]byte("no_secrets_here: true\n"))
	f.Add([]byte("api_key: \"\"\n"))
	f.Add([]byte("api_key: ''\n"))
	f.Add([]byte("api_key: value # comment\n"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		// RedactSecrets should not panic on arbitrary input.
		_ = RedactSecrets(data)
	})
}

func FuzzStripYAMLComment(f *testing.F) {
	f.Add([]byte("value # comment"))
	f.Add([]byte("value"))
	f.Add([]byte("# just a comment"))
	f.Add([]byte(""))
	f.Add([]byte("no_hash"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// StripYAMLComment should not panic on arbitrary input.
		_ = StripYAMLComment(data)
	})
}
