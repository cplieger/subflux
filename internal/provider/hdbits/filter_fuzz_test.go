package hdbits

import "testing"

func FuzzFlexIntUnmarshalJSON(f *testing.F) {
	f.Add([]byte(`42`))
	f.Add([]byte(`"123"`))
	f.Add([]byte(`null`))
	f.Add([]byte(`0`))
	f.Add([]byte(`-1`))
	f.Add([]byte(`"not_a_number"`))
	f.Add([]byte(`""`))
	f.Add([]byte(`99999999`))
	f.Fuzz(func(t *testing.T, data []byte) {
		var fi flexInt
		_ = fi.UnmarshalJSON(data)
	})
}
