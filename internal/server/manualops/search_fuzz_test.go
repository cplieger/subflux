package manualops

import "testing"

// FuzzQueryIntNonNegative verifies that QueryInt always returns a non-negative
// value regardless of input (partition property).
func FuzzQueryIntNonNegative(f *testing.F) {
	f.Add("key", "42")
	f.Add("key", "-1")
	f.Add("key", "")
	f.Add("key", "abc")
	f.Add("key", "9999999999999999999")

	f.Fuzz(func(t *testing.T, key, value string) {
		q := fakeQuery{key: key, val: value}
		result := QueryInt(q, key)
		if result < 0 {
			t.Fatalf("QueryInt returned negative: %d for input %q", result, value)
		}
	})
}

// FuzzIsValidLangCodeDeterministic verifies that IsValidLangCode is a pure
// function: calling it twice on the same input yields the same result.
func FuzzIsValidLangCodeDeterministic(f *testing.F) {
	f.Add("en")
	f.Add("pt-BR")
	f.Add("")
	f.Add("../../etc/passwd")
	f.Add("a\x00b")

	f.Fuzz(func(t *testing.T, lang string) {
		r1 := IsValidLangCode(lang)
		r2 := IsValidLangCode(lang)
		if r1 != r2 {
			t.Fatalf("non-deterministic: %q gave %v then %v", lang, r1, r2)
		}
		// Additional invariant: if valid, length must be in [1, MaxLangCodeLen].
		if r1 && (len(lang) == 0 || len(lang) > MaxLangCodeLen) {
			t.Fatalf("valid but length %d out of bounds", len(lang))
		}
	})
}

type fakeQuery struct {
	key string
	val string
}

func (f fakeQuery) Get(k string) string {
	if k == f.key {
		return f.val
	}
	return ""
}
