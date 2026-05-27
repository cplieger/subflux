package classify

import "testing"

func BenchmarkAlpha2FromAlpha3(b *testing.B) {
	cases := []struct {
		name string
		code string
	}{
		{name: "known_3char", code: "eng"},
		{name: "unknown_3char", code: "zzz"},
		{name: "already_2char", code: "en"},
		{name: "empty", code: ""},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			for range b.N {
				Alpha2FromAlpha3(tc.code)
			}
		})
	}
}

func BenchmarkLookupLangName(b *testing.B) {
	overrides := map[string]string{"pb": "Brazillian Portuguese"}

	cases := []struct {
		overrides map[string]string
		name      string
		code      string
	}{
		{name: "known_no_overrides", code: "en", overrides: nil},
		{name: "known_with_overrides", code: "en", overrides: overrides},
		{name: "override_hit", code: "pb", overrides: overrides},
		{name: "unknown", code: "xx", overrides: nil},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			for range b.N {
				LookupLangName(tc.code, tc.overrides)
			}
		})
	}
}
