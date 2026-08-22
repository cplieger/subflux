package subdl

import (
	"testing"

	"github.com/cplieger/subflux/internal/subflux"
	"pgregory.net/rapid"
)

func TestFactory_requires_api_key(t *testing.T) {
	t.Parallel()
	if _, err := Factory(t.Context(), nil); err == nil {
		t.Fatal("Factory(t.Context(), nil) expected error")
	}
	if _, err := Factory(t.Context(), map[string]any{"api_key": ""}); err == nil {
		t.Fatal("Factory(empty key) expected error")
	}
}

func TestFactory_with_api_key(t *testing.T) {
	t.Parallel()
	p, err := Factory(t.Context(), map[string]any{"api_key": "test"})
	if err != nil {
		t.Fatalf("Factory() unexpected error: %v", err)
	}
	if p.Name() != subflux.ProviderNameSubDL {
		t.Errorf("Name() = %q, want %q", p.Name(), subflux.ProviderNameSubDL)
	}
}

// TestSearch_skipsWithoutAnIdentifier asserts a request carrying no IMDB id, no
// TMDB id and no title is skipped locally: with nothing to search for there is
// no query to send, so the provider answers "no results" without a request.
func TestSearch_skipsWithoutAnIdentifier(t *testing.T) {
	t.Parallel()
	p, err := Factory(t.Context(), map[string]any{"api_key": "test"})
	if err != nil {
		t.Fatalf("Factory() unexpected error: %v", err)
	}

	subs, err := p.Search(t.Context(), &subflux.SearchRequest{Languages: []string{"en"}})
	if err != nil {
		t.Fatalf("Search(no identifiers) unexpected error: %v", err)
	}
	if subs != nil {
		t.Errorf("Search(no identifiers) = %+v, want nil", subs)
	}
}

// --- language mapping ---

func TestIso2ToSubDL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{name: "English", input: "en", want: "EN"},
		{name: "French", input: "fr", want: "FR"},
		{name: "Persian", input: "fa", want: "FA"},
		{name: "Brazilian Portuguese override", input: "pb", want: "BR_PT"},
		{name: "alpha3 English", input: "eng", want: "EN"},
		{name: "alpha3 French", input: "fre", want: "FR"},
		{name: "unknown", input: "xx", want: ""},
		{name: "empty", input: "", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := iso2ToSubDL(tt.input); got != tt.want {
				t.Errorf("iso2ToSubDL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestSubdlToISO2(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, input, want string
	}{
		{name: "english", input: "EN", want: "en"},
		{name: "french", input: "FR", want: "fr"},
		{name: "persian", input: "FA", want: "fa"},
		{name: "brazilian portuguese override", input: "BR_PT", want: "pb"},
		{name: "lowercase input", input: "en", want: "en"},
		{name: "unknown", input: "XX", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := subdlToISO2(tt.input); got != tt.want {
				t.Errorf("subdlToISO2(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestLanguageMapping_roundtrip(t *testing.T) {
	t.Parallel()
	// Collect all valid ISO-2 codes from the map.
	codes := make([]string, 0, len(iso2ToSubDLMap))
	for k := range iso2ToSubDLMap {
		codes = append(codes, k)
	}

	rapid.Check(t, func(t *rapid.T) {
		idx := rapid.IntRange(0, len(codes)-1).Draw(t, "idx")
		code := codes[idx]

		sdl := iso2ToSubDL(code)
		if sdl == "" {
			t.Fatalf("iso2ToSubDL(%q) = empty, want non-empty", code)
		}
		back := subdlToISO2(sdl)
		if back != code {
			t.Fatalf("subdlToISO2(iso2ToSubDL(%q)) = %q, want %q", code, back, code)
		}
	})
}
