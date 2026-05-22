package classify

import "testing"

func BenchmarkAlpha2FromAlpha3(b *testing.B) {
	cases := []struct {
		name string
		code string
	}{
		{"known_3char", "eng"},
		{"unknown_3char", "zzz"},
		{"already_2char", "en"},
		{"empty", ""},
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
		name      string
		code      string
		overrides map[string]string
	}{
		{"known_no_overrides", "en", nil},
		{"known_with_overrides", "en", overrides},
		{"override_hit", "pb", overrides},
		{"unknown", "xx", nil},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			for range b.N {
				LookupLangName(tc.code, tc.overrides)
			}
		})
	}
}
