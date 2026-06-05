package confighandlers

import (
	"bytes"
	"testing"
)

// FuzzFindClosingQuote exercises the byte-level quote scanner with arbitrary
// input and quote characters.
//
// Bug class: off-by-one in escape handling could cause index-out-of-bounds
// panic or incorrect quote matching on untrusted YAML values.
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
			t.Fatalf("result %d out of range [-1, %d)", idx, len(val))
		}
		if idx >= 0 && val[idx] != q {
			t.Fatalf("char at result index is %q, want %q", val[idx], q)
		}
	})
}

// FuzzSecretContextKey exercises the YAML indent-walking logic with arbitrary
// line arrays and indices.
//
// Bug class: index-out-of-bounds panic when lineIdx is at edges or lines
// contain unexpected indentation patterns.
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
		// Must not panic.
		result := SecretContextKey(lines, lineIdx, key)
		if len(result) == 0 && key != "" {
			t.Fatal("expected non-empty result when key is non-empty")
		}
	})
}

// FuzzExtractSecretValues exercises YAML secret extraction from arbitrary bytes.
//
// Bug class: panic on malformed YAML-like input; keys in returned map should
// always be non-empty strings.
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
		}
	})
}
