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
