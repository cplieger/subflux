package confighandlers

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

// FuzzRedactSecrets_idempotent verifies redaction is stable: redacting an
// already-redacted document yields byte-identical output, so a re-render of a
// GET /api/config response never drifts.
func FuzzRedactSecrets_idempotent(f *testing.F) {
	f.Add([]byte("api_key: mysecret\n"))
	f.Add([]byte("totp_key: abc123\nopensubtitles_password: hunter2\n"))
	f.Add([]byte("no_secrets_here: true\n"))
	f.Add([]byte("api_key: \"\"\n"))
	f.Add([]byte("api_key: ''\n"))
	f.Add([]byte("api_key: value # comment\n"))
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

// FuzzRedactSecrets_secretNeverLeaks is the core security invariant behind the
// GET /api/config redaction: a real secret value placed under a secret key must
// never survive verbatim in the redacted output. The fuzzed bytes are folded
// into a distinctive, guaranteed-redactable marker ("SX"-prefixed, single
// line) so the leak check can never collide with the key name or the
// placeholder, while still driving arbitrary value content through the parser.
func FuzzRedactSecrets_secretNeverLeaks(f *testing.F) {
	f.Add("hunter2")
	f.Add("p@ss w0rd")
	f.Add("abc#notacomment")
	f.Add("value # trailing")
	f.Add(`"quoted"`)
	f.Add("")

	f.Fuzz(func(t *testing.T, raw string) {
		// "SX" never appears in the key "api_key" or the placeholder
		// `"********"`, and guarantees a non-empty, non-quote-empty value, so
		// the value is always redactable and always distinctive.
		secret := "SX" + strings.NewReplacer("\n", "", "\r", "").Replace(raw)

		doc := []byte("api_key: " + secret + "\n")
		out := RedactSecrets(doc)
		if !bytes.Equal(out, []byte("api_key: \"********\"\n")) {
			t.Fatalf("RedactSecrets(api_key: %q) = %q, want fully redacted line", secret, out)
		}
		if bytes.Contains(out, []byte(secret)) {
			t.Errorf("RedactSecrets leaked secret %q in output %q", secret, out)
		}
	})
}

// FuzzStripYAMLComment_idempotent verifies the inline-comment stripper never
// grows its input and is stable under reapplication.
func FuzzStripYAMLComment_idempotent(f *testing.F) {
	f.Add([]byte("value # comment"))
	f.Add([]byte("value"))
	f.Add([]byte("# just a comment"))
	f.Add([]byte(""))
	f.Add([]byte("no_hash"))
	f.Add([]byte(`"quoted # not a comment"`))
	f.Add([]byte(`"abc" # trailing`))

	f.Fuzz(func(t *testing.T, data []byte) {
		once := StripYAMLComment(data)
		if len(once) > len(data) {
			t.Fatalf("StripYAMLComment grew input: in=%q out=%q", data, once)
		}
		twice := StripYAMLComment(once)
		if !bytes.Equal(once, twice) {
			t.Errorf("StripYAMLComment not idempotent:\n  once=%q\n twice=%q", once, twice)
		}
	})
}

// FuzzFindClosingQuote verifies the byte-level quote scanner never reports an
// out-of-range index and, when it finds a quote, the byte at that index really
// is the quote character.
func FuzzFindClosingQuote(f *testing.F) {
	f.Add([]byte(`"hello"`), byte('"'))
	f.Add([]byte(`'it\'s'`), byte('\''))
	f.Add([]byte(`""`), byte('"'))
	f.Add([]byte(`"\\""`), byte('"'))
	f.Add([]byte{}, byte('"'))
	f.Add([]byte(`"unterminated`), byte('"'))

	f.Fuzz(func(t *testing.T, val []byte, q byte) {
		idx := FindClosingQuote(val, q)
		if idx < -1 || idx >= len(val) {
			t.Fatalf("FindClosingQuote(%q, %q) = %d, out of range [-1, %d)", val, q, idx, len(val))
		}
		if idx >= 0 && val[idx] != q {
			t.Fatalf("FindClosingQuote(%q, %q) = %d, byte there is %q", val, q, idx, val[idx])
		}
	})
}

// FuzzSecretContextKey verifies the YAML indent walker never panics and always
// returns a path whose final segment is the key it was asked about.
func FuzzSecretContextKey(f *testing.F) {
	f.Add([]byte("root:\n  child:\n    api_key: secret\n"), uint8(2), "api_key")
	f.Add([]byte("key: val\n"), uint8(0), "key")
	f.Add([]byte("\n\n\n"), uint8(0), "x")
	f.Add([]byte("a:\n  b:\n    c:\n      d: v\n"), uint8(3), "d")

	f.Fuzz(func(t *testing.T, data []byte, lineIdxRaw uint8, key string) {
		lines := bytes.Split(data, []byte("\n"))
		if len(lines) == 0 {
			return
		}
		lineIdx := int(lineIdxRaw) % len(lines)
		result := SecretContextKey(lines, lineIdx, key)
		// The key is always the last path segment, so it must be a suffix.
		if !strings.HasSuffix(result, key) {
			t.Fatalf("SecretContextKey(...) = %q does not end with key %q", result, key)
		}
		if key != "" && result == "" {
			t.Fatalf("SecretContextKey(...) = %q, want non-empty for key %q", result, key)
		}
	})
}

// FuzzExtractSecretValues verifies extraction never panics and that every
// returned entry has a non-empty value and a key whose final segment is a
// recognised secret-key name.
func FuzzExtractSecretValues(f *testing.F) {
	f.Add([]byte("api_key: mysecret\n"))
	f.Add([]byte("opensubtitles_password: hunter2\napi_key: abc\n"))
	f.Add([]byte("no_secrets: true\n"))
	f.Add([]byte{})
	f.Add([]byte("api_key: \"\"\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		secrets := ExtractSecretValues(data)
		for k, v := range secrets {
			if k == "" {
				t.Fatal("empty key in secrets map")
			}
			if v == "" {
				t.Fatalf("empty value for key %q", k)
			}
			lastSeg := k[strings.LastIndex(k, ".")+1:]
			if !slices.Contains(secretKeyNames, lastSeg) {
				t.Fatalf("secret key %q has unrecognised final segment %q", k, lastSeg)
			}
		}
	})
}
