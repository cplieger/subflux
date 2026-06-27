package classify

import (
	"testing"

	"pgregory.net/rapid"
)

func TestAlpha2FromAlpha3(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Already 2-char codes pass through.
		{"two char code en", "en", "en"},
		{"two char code fr", "fr", "fr"},
		{"two char code de", "de", "de"},

		// Standard 3-char to 2-char mappings.
		{"eng to en", "eng", "en"},
		{"fre to fr", "fre", "fr"},
		{"fra to fr", "fra", "fr"},
		{"ger to de", "ger", "de"},
		{"deu to de", "deu", "de"},
		{"spa to es", "spa", "es"},
		{"ita to it", "ita", "it"},
		{"por to pt", "por", "pt"},
		{"rus to ru", "rus", "ru"},
		{"jpn to ja", "jpn", "ja"},
		{"chi to zh", "chi", "zh"},
		{"zho to zh", "zho", "zh"},
		{"kor to ko", "kor", "ko"},
		{"ara to ar", "ara", "ar"},
		{"hin to hi", "hin", "hi"},
		{"tha to th", "tha", "th"},
		{"vie to vi", "vie", "vi"},
		{"pol to pl", "pol", "pl"},
		{"nld to nl", "nld", "nl"},
		{"dut to nl", "dut", "nl"},
		{"swe to sv", "swe", "sv"},
		{"nor to no", "nor", "no"},
		{"nob to no", "nob", "no"},
		{"dan to da", "dan", "da"},
		{"fin to fi", "fin", "fi"},
		{"tur to tr", "tur", "tr"},
		{"hun to hu", "hun", "hu"},
		{"ces to cs", "ces", "cs"},
		{"cze to cs", "cze", "cs"},
		{"ron to ro", "ron", "ro"},
		{"rum to ro", "rum", "ro"},
		{"bul to bg", "bul", "bg"},
		{"hrv to hr", "hrv", "hr"},
		{"slk to sk", "slk", "sk"},
		{"slo to sk", "slo", "sk"},
		{"slv to sl", "slv", "sl"},
		{"ukr to uk", "ukr", "uk"},
		{"ell to el", "ell", "el"},
		{"gre to el", "gre", "el"},
		{"heb to he", "heb", "he"},
		{"ind to id", "ind", "id"},
		{"msa to ms", "msa", "ms"},
		{"may to ms", "may", "ms"},
		{"cat to ca", "cat", "ca"},
		{"eus to eu", "eus", "eu"},
		{"baq to eu", "baq", "eu"},
		{"glg to gl", "glg", "gl"},
		{"srp to sr", "srp", "sr"},
		{"bos to bs", "bos", "bs"},
		{"lit to lt", "lit", "lt"},
		{"lav to lv", "lav", "lv"},
		{"est to et", "est", "et"},
		{"nno to nn", "nno", "nn"},
		{"isl to is", "isl", "is"},
		{"ice to is", "ice", "is"},
		{"mkd to mk", "mkd", "mk"},
		{"mac to mk", "mac", "mk"},
		{"sqi to sq", "sqi", "sq"},
		{"alb to sq", "alb", "sq"},
		{"mlt to mt", "mlt", "mt"},
		{"cym to cy", "cym", "cy"},
		{"wel to cy", "wel", "cy"},
		{"gle to ga", "gle", "ga"},
		{"kat to ka", "kat", "ka"},
		{"geo to ka", "geo", "ka"},
		{"hye to hy", "hye", "hy"},
		{"arm to hy", "arm", "hy"},
		{"aze to az", "aze", "az"},
		{"kaz to kk", "kaz", "kk"},
		{"uzb to uz", "uzb", "uz"},
		{"mon to mn", "mon", "mn"},
		{"mya to my", "mya", "my"},
		{"bur to my", "bur", "my"},
		{"khm to km", "khm", "km"},
		{"lao to lo", "lao", "lo"},
		{"tam to ta", "tam", "ta"},
		{"tel to te", "tel", "te"},
		{"kan to kn", "kan", "kn"},
		{"mal to ml", "mal", "ml"},
		{"mar to mr", "mar", "mr"},
		{"ben to bn", "ben", "bn"},
		{"guj to gu", "guj", "gu"},
		{"pan to pa", "pan", "pa"},
		{"urd to ur", "urd", "ur"},
		{"nep to ne", "nep", "ne"},
		{"sin to si", "sin", "si"},
		{"afr to af", "afr", "af"},
		{"swa to sw", "swa", "sw"},
		{"amh to am", "amh", "am"},
		{"som to so", "som", "so"},
		{"hau to ha", "hau", "ha"},
		{"yor to yo", "yor", "yo"},
		{"ibo to ig", "ibo", "ig"},
		{"zul to zu", "zul", "zu"},
		{"xho to xh", "xho", "xh"},

		// Internal codes (not ISO 639).
		{"pob to pb (Brazilian Portuguese)", "pob", "pb"},

		// Case insensitivity.
		{"uppercase ENG", "ENG", "en"},
		{"mixed case Fre", "Fre", "fr"},

		// Unknown codes.
		{"unknown 3-char", "xyz", ""},
		{"unknown 4-char", "abcd", ""},
		{"empty string", "", ""},
		{"single char returns empty", "e", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := Alpha2FromAlpha3(tt.input)
			if got != tt.want {
				t.Errorf("Alpha2FromAlpha3(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// PBT: Alpha2FromAlpha3 result is always empty or exactly 2 characters.
func TestAlpha2FromAlpha3_result_length_invariant(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		code := rapid.StringMatching(`[a-zA-Z]{0,5}`).Draw(t, "code")

		got := Alpha2FromAlpha3(code)

		if got != "" && len(got) != 2 {
			t.Errorf("Alpha2FromAlpha3(%q) = %q (len %d), want len 0 or 2",
				code, got, len(got))
		}
	})
}

// PBT: Alpha2FromAlpha3 is idempotent for known codes; applying twice
// gives the same result as applying once.
func TestAlpha2FromAlpha3_idempotent_known_codes(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		keys := make([]string, 0, len(alpha3to2))
		for k := range alpha3to2 {
			keys = append(keys, k)
		}
		code := rapid.SampledFrom(keys).Draw(t, "alpha3")

		once := Alpha2FromAlpha3(code)
		twice := Alpha2FromAlpha3(once)

		if once != twice {
			t.Errorf("Alpha2FromAlpha3 not idempotent: Alpha2FromAlpha3(%q) = %q, Alpha2FromAlpha3(%q) = %q",
				code, once, once, twice)
		}
	})
}

func TestSanitizeImdbID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"standard tt prefix", "tt1234567", "1234567"},
		{"leading zeros stripped", "tt0012345", "12345"},
		{"no prefix", "1234567", "1234567"},
		{"only tt prefix", "tt", ""},
		{"empty string", "", ""},
		{"tt with all zeros", "tt0000001", "1"},
		{"tt with single digit", "tt1", "1"},
		{"no tt prefix with leading zeros", "0001234", "1234"},
		{"large ID", "tt12345678", "12345678"},
		{"all zeros after prefix", "tt0000000", ""},
		{"single zero after prefix", "tt0", ""},
		{"bare zero", "0", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := SanitizeImdbID(tt.input)
			if got != tt.want {
				t.Errorf("SanitizeImdbID(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// PBT: SanitizeImdbID is idempotent; sanitizing twice gives the same result.
func TestSanitizeImdbID_idempotent(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		id := rapid.StringMatching(`(tt)?[0-9]{0,10}`).Draw(t, "imdb_id")

		once := SanitizeImdbID(id)
		twice := SanitizeImdbID(once)

		if once != twice {
			t.Errorf("SanitizeImdbID not idempotent: SanitizeImdbID(%q) = %q, SanitizeImdbID(%q) = %q",
				id, once, once, twice)
		}
	})
}

// PBT: SanitizeImdbID result never contains "tt" prefix or leading zeros.
func TestSanitizeImdbID_no_prefix_no_leading_zeros(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		id := rapid.StringMatching(`(tt)?0*[1-9][0-9]{0,9}`).Draw(t, "imdb_id")

		got := SanitizeImdbID(id)

		if got != "" && got[0] == '0' {
			t.Errorf("SanitizeImdbID(%q) = %q, starts with leading zero", id, got)
		}
		if len(got) >= 2 && got[:2] == "tt" {
			t.Errorf("SanitizeImdbID(%q) = %q, still has tt prefix", id, got)
		}
	})
}

func TestLookupLangName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		code      string
		overrides map[string]string
		want      string
	}{
		{name: "iso2 known code", code: "en", want: "English"},
		{name: "iso3 known code is canonicalized first", code: "eng", want: "English"},
		{name: "internal pb code", code: "pb", want: "Brazilian Portuguese"},
		{name: "unknown code", code: "zz", want: ""},
		{name: "empty code", code: "", want: ""},
		{name: "override wins over registry", code: "en", overrides: map[string]string{"en": "ZZZ"}, want: "ZZZ"},
		{name: "override miss falls through to registry", code: "fr", overrides: map[string]string{"en": "ZZZ"}, want: "French"},
		{name: "override keyed by canonicalized code", code: "fre", overrides: map[string]string{"fr": "Frenchy"}, want: "Frenchy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := LookupLangName(tt.code, tt.overrides); got != tt.want {
				t.Errorf("LookupLangName(%q, %v) = %q, want %q", tt.code, tt.overrides, got, tt.want)
			}
		})
	}
}

func TestLookupLangCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		langName  string
		overrides map[string]string
		want      string
	}{
		{name: "known name", langName: "English", want: "en"},
		{name: "internal Brazilian Portuguese name", langName: "Brazilian Portuguese", want: "pb"},
		{name: "unknown name", langName: "Klingon", want: ""},
		{name: "empty name", langName: "", want: ""},
		{name: "override wins over registry", langName: "English", overrides: map[string]string{"English": "zz"}, want: "zz"},
		{name: "override miss falls through to registry", langName: "French", overrides: map[string]string{"English": "zz"}, want: "fr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := LookupLangCode(tt.langName, tt.overrides); got != tt.want {
				t.Errorf("LookupLangCode(%q, %v) = %q, want %q", tt.langName, tt.overrides, got, tt.want)
			}
		})
	}
}
