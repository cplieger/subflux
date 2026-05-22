package classify

import "strings"

// alpha3to2 maps ISO 639-3/639-2 codes to ISO 639-1 codes.
var alpha3to2 = map[string]string{
	"eng": "en", "fre": "fr", "fra": "fr", "ger": "de", "deu": "de",
	"spa": "es", "ita": "it", "por": "pt", "rus": "ru", "jpn": "ja",
	"chi": "zh", "zho": "zh", "kor": "ko", "ara": "ar", "hin": "hi",
	"tha": "th", "vie": "vi", "pol": "pl", "nld": "nl", "dut": "nl",
	"swe": "sv", "nor": "no", "nob": "no", "nno": "nn", "dan": "da",
	"fin": "fi", "tur": "tr", "hun": "hu", "ces": "cs", "cze": "cs",
	"ron": "ro", "rum": "ro", "bul": "bg", "hrv": "hr", "slk": "sk",
	"slo": "sk", "slv": "sl", "ukr": "uk", "ell": "el", "gre": "el",
	"heb": "he", "ind": "id", "msa": "ms", "may": "ms", "cat": "ca",
	"eus": "eu", "baq": "eu", "glg": "gl", "srp": "sr", "bos": "bs",
	"lit": "lt", "lav": "lv", "est": "et",
	"isl": "is", "ice": "is", "mkd": "mk", "mac": "mk",
	"sqi": "sq", "alb": "sq", "mlt": "mt",
	"cym": "cy", "wel": "cy", "gle": "ga",
	"kat": "ka", "geo": "ka", "hye": "hy", "arm": "hy",
	"aze": "az", "kaz": "kk", "uzb": "uz", "mon": "mn",
	"mya": "my", "bur": "my", "khm": "km", "lao": "lo",
	"tam": "ta", "tel": "te", "kan": "kn", "mal": "ml", "mar": "mr",
	"ben": "bn", "guj": "gu", "pan": "pa", "urd": "ur", "nep": "ne",
	"sin": "si", "afr": "af", "swa": "sw", "amh": "am", "som": "so",
	"hau": "ha", "yor": "yo", "ibo": "ig", "zul": "zu", "xho": "xh",
	// pb = Brazilian Portuguese (internal code, not ISO).
	"pob": "pb",
}

// Alpha2FromAlpha3 converts an ISO 639-2/3 code to ISO 639-1.
// Returns the input unchanged if it is already 2 characters.
// Returns empty string if the code is unknown.
func Alpha2FromAlpha3(code string) string {
	code = strings.ToLower(code)
	if len(code) == 2 {
		return code
	}
	if v, ok := alpha3to2[code]; ok {
		return v
	}
	return ""
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
	"lo": "Lao", "my": "Burmese", "pb": "Brazilian Portuguese",
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
