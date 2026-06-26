package opensubtitles

import "testing"

func TestToOSLang(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "pt maps to pt-PT", input: "pt", want: "pt-PT"},
		{name: "pb maps to pt-BR", input: "pb", want: "pt-BR"},
		{name: "zh maps to zh-CN", input: "zh", want: "zh-CN"},
		{name: "en passes through", input: "en", want: "en"},
		{name: "fr passes through", input: "fr", want: "fr"},
		{name: "empty passes through", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := toOSLang(tt.input)
			if got != tt.want {
				t.Errorf("toOSLang(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestFromOSLang(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "pt-PT maps to pt", input: "pt-PT", want: "pt"},
		{name: "pt-BR maps to pb", input: "pt-BR", want: "pb"},
		{name: "zh-CN maps to zh", input: "zh-CN", want: "zh"},
		{name: "ea maps to es", input: "ea", want: "es"},
		{name: "en passes through", input: "en", want: "en"},
		{name: "fr passes through", input: "fr", want: "fr"},
		{name: "empty passes through", input: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := fromOSLang(tt.input)
			if got != tt.want {
				t.Errorf("fromOSLang(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

func TestLangMapping_round_trip(t *testing.T) {
	t.Parallel()

	// Every key in langToOS should round-trip through toOSLang → fromOSLang.
	for iso, os := range langToOS {
		got := fromOSLang(toOSLang(iso))
		if got != iso {
			t.Errorf("fromOSLang(toOSLang(%q)) = %q, want %q (via %q)",
				iso, got, iso, os)
		}
	}

	// Every value in langFromOS that also has a reverse entry in langToOS
	// should round-trip. Entries like "ea"→"es" are one-way legacy mappings
	// (OpenSubtitles uses "ea" for Spanish, but we send "es" which passes
	// through unchanged), so we only check keys whose ISO code maps back.
	for os, iso := range langFromOS {
		if _, hasReverse := langToOS[iso]; !hasReverse {
			continue
		}
		got := toOSLang(fromOSLang(os))
		if got != os {
			t.Errorf("toOSLang(fromOSLang(%q)) = %q, want %q (via %q)",
				os, got, os, iso)
		}
	}
}
