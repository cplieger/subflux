package provider

import (
	"strconv"
	"testing"
)

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
		// ParseFlexInt handles untrusted JSON from provider APIs and must never
		// panic regardless of input.
		v, err := ParseFlexInt(data)
		if err != nil {
			return
		}
		// Round-trip + metamorphic invariant: any successfully parsed value
		// must re-parse to itself from its canonical decimal form, and the two
		// accepted representations (bare number and quoted string) must agree.
		canon := strconv.Itoa(v)
		if got, e := ParseFlexInt([]byte(canon)); e != nil || got != v {
			t.Fatalf("bare-number round-trip of %d = (%d, %v), want (%d, nil)", v, got, e, v)
		}
		if got, e := ParseFlexInt([]byte(`"` + canon + `"`)); e != nil || got != v {
			t.Fatalf("quoted-string round-trip of %d = (%d, %v), want (%d, nil)", v, got, e, v)
		}
	})
}
