package classify

import (
	"strings"

	"github.com/cplieger/subflux/internal/langcode"
)

// Alpha2FromAlpha3 resolves any published language code onto subflux's internal
// code space: ISO 639-1 two-letter codes plus "pb" for Brazilian Portuguese.
//
// It accepts ISO 639-1, both ISO 639-2 variants, ISO 639-3 and BCP 47 tags, in
// any letter case. It returns "" for anything that names no language subflux can
// represent, which includes a language with no ISO 639-1 assignment (Cantonese,
// Filipino) — a three-letter code in a two-letter namespace matches nothing.
//
// Two behaviours are worth knowing at the call sites, because a hand-written
// table used to decide them differently. A region is consulted before it is
// discarded, so "pt-BR" resolves to "pb" rather than "pt". And an unrecognized
// two-letter input is now rejected rather than passed through: the old table
// returned any two letters unchanged, which let "xx" pose as a language.
//
// The name is kept for its committed fuzz corpus
// (testdata/fuzz/FuzzAlpha2FromAlpha3) even though the function now resolves
// more than alpha-3.
func Alpha2FromAlpha3(code string) string {
	return langcode.Canonical(code)
}

// SanitizeImdbID strips the "tt" prefix and leading zeros from an IMDB ID,
// returning the bare numeric string expected by most subtitle APIs.
func SanitizeImdbID(id string) string {
	return strings.TrimLeft(strings.TrimPrefix(id, "tt"), "0")
}

// LangRegistry is the canonical ISO 639-1 → English language name mapping.
// Provider sub-packages use this as the single source of truth, applying
// per-provider overrides only for non-standard names (e.g. "Brazillian
// Portuguese" for SubSource API compat).
var LangRegistry = map[string]string{
	"en": "English", "fr": "French", "es": "Spanish", "de": "German",
	"it": "Italian", "pt": "Portuguese", "nl": "Dutch", "ru": "Russian",
	"ar": "Arabic", "ja": "Japanese", "zh": "Chinese", "ko": "Korean",
	"sv": "Swedish", "no": "Norwegian", "da": "Danish", "fi": "Finnish",
	"pl": "Polish", "cs": "Czech", "hu": "Hungarian", "ro": "Romanian",
	"tr": "Turkish", "el": "Greek", "he": "Hebrew", "th": "Thai",
	"vi": "Vietnamese", "id": "Indonesian", "bg": "Bulgarian",
	"hr": "Croatian", "sr": "Serbian", "sl": "Slovenian",
	"sk": "Slovak", "uk": "Ukrainian", "ca": "Catalan",
	"eu": "Basque", "gl": "Galician", "fa": "Persian",
	"ms": "Malay", "sq": "Albanian", "bs": "Bosnian",
	"hy": "Armenian", "az": "Azerbaijani", "bn": "Bengali",
	"mk": "Macedonian", "hi": "Hindi", "ta": "Tamil",
	"te": "Telugu", "ml": "Malayalam", "kn": "Kannada",
	"mr": "Marathi", "ur": "Urdu", "ne": "Nepali",
	"si": "Sinhalese", "af": "Afrikaans", "sw": "Swahili",
	"lt": "Lithuanian", "lv": "Latvian", "et": "Estonian",
	"is": "Icelandic", "ga": "Irish", "cy": "Welsh",
	"ka": "Georgian", "mn": "Mongolian", "km": "Khmer",
	"lo": "Lao", "my": "Burmese",
	langcode.BrazilianPortuguese: "Brazilian Portuguese",
}

// LangNameToISO2 is the reverse of LangRegistry: English name → ISO-2 code.
// Built once at init time from LangRegistry.
var LangNameToISO2 map[string]string

func init() {
	LangNameToISO2 = make(map[string]string, len(LangRegistry))
	for code, name := range LangRegistry {
		LangNameToISO2[name] = code
	}
}

// LookupLangName returns the English language name for an ISO-2 code,
// applying provider-specific overrides if provided. Returns empty string
// if the code is unknown.
func LookupLangName(code string, overrides map[string]string) string {
	code = Alpha2FromAlpha3(code)
	if overrides != nil {
		if v, ok := overrides[code]; ok {
			return v
		}
	}
	if v, ok := LangRegistry[code]; ok {
		return v
	}
	return ""
}

// LookupLangCode returns the ISO-2 code for an English language name,
// applying provider-specific overrides if provided. Returns empty string
// if the name is unknown.
func LookupLangCode(name string, overrides map[string]string) string {
	if overrides != nil {
		if v, ok := overrides[name]; ok {
			return v
		}
	}
	if v, ok := LangNameToISO2[name]; ok {
		return v
	}
	return ""
}
