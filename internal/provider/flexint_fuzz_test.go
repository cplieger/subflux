package provider

import "testing"

func FuzzParseFlexInt(f *testing.F) {
	f.Add([]byte(`42`))
	f.Add([]byte(`"123"`))
	f.Add([]byte(`""`))
	f.Add([]byte(`0`))
	f.Add([]byte(`-1`))
	f.Add([]byte(`null`))
	f.Add([]byte(`"abc"`))
	f.Add([]byte(`99999999999`))
	f.Add([]byte(`"0"`))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		// ParseFlexInt must never panic regardless of input.
		_, _ = ParseFlexInt(data)
	})
}
