package api

import (
	"log/slog"
	"strings"
)

// loggedUnknownLangs dedupes unmapped audio language name logs so each
// new value is only reported once per process.
var loggedUnknownLangs = newLogOnce(256)

// logUnknownLang emits a one-shot DEBUG log for an unmapped audio
// language name, so operators can extend langNameMap without drowning
// in per-episode noise.
func logUnknownLang(raw string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return
	}
	if loggedUnknownLangs.first(raw) {
		slog.Debug("audio lang: unmapped name (ignored)", "name", raw)
	}
}

// ParseAudioLangs splits a comma/slash-separated audio languages string
// into deduplicated ISO 639-1 codes.
func ParseAudioLangs(raw string) []string {
	if !strings.ContainsAny(raw, "/,") {
		// Single language, no separator — fast path avoids map allocation.
		trimmed := strings.TrimSpace(raw)
		if code := LangNameToISO(trimmed); code != "" {
			return []string{code}
		}
		logUnknownLang(trimmed)
		return nil
	}

	var codes []string
	seen := make(map[string]bool)
	for part := range strings.FieldsFuncSeq(raw, func(r rune) bool {
		return r == '/' || r == ','
	}) {
		trimmed := strings.TrimSpace(part)
		code := LangNameToISO(trimmed)
		if code == "" {
			logUnknownLang(trimmed)
			continue
		}
		if !seen[code] {
			codes = append(codes, code)
			seen[code] = true
		}
	}
	if len(codes) == 0 {
		return nil
	}
	return codes
}

// langNameMap maps lowercase language names (as returned by Sonarr/Radarr)
// to ISO 639-1 codes.
var langNameMap = map[string]string{
	"english": "en", "french": "fr", "german": "de",
	"spanish": "es", "italian": "it", "portuguese": "pt",
	"russian": "ru", "japanese": "ja", "chinese": "zh",
	"korean": "ko", "arabic": "ar", "hindi": "hi",
	"thai": "th", "vietnamese": "vi", "polish": "pl",
	"dutch": "nl", "swedish": "sv", "norwegian": "no",
	"danish": "da", "finnish": "fi", "turkish": "tr",
	"hungarian": "hu", "czech": "cs", "romanian": "ro",
	"bulgarian": "bg", "croatian": "hr", "serbian": "sr",
	"slovak": "sk", "slovenian": "sl", "ukrainian": "uk",
	"greek": "el", "hebrew": "he", "indonesian": "id",
	"malay": "ms", "catalan": "ca", "galician": "gl",
	"bosnian": "bs", "lithuanian": "lt", "latvian": "lv",
	"estonian": "et", "persian": "fa", "bengali": "bn",
	"tamil": "ta", "telugu": "te", "urdu": "ur",
	"icelandic": "is", "macedonian": "mk", "albanian": "sq",
	"welsh": "cy", "irish": "ga",
	// Regional variants returned by Sonarr/Radarr.
	"flemish": "nl", "portuguese (brazil)": "pt", "spanish (latino)": "es",
	// Additional Sonarr/Radarr languages.
	"malayalam": "ml", "kannada": "kn", "afrikaans": "af",
	"marathi": "mr", "tagalog": "tl", "romansh": "rm",
	"mongolian": "mn", "georgian": "ka",
	"amharic": "am", "azerbaijani": "az", "belarusian": "be",
	"burmese": "my", "khmer": "km", "lao": "lo",
	"nepali": "ne", "pashto": "ps", "punjabi": "pa",
	"sinhalese": "si", "sinhala": "si", "somali": "so",
	"swahili": "sw", "uzbek": "uz", "yoruba": "yo", "zulu": "zu",
}

// LangNameToISO converts a language name (as returned by Sonarr/Radarr)
// to an ISO 639-1 code. Accepts full names ("english") or 2-letter ASCII codes.
// Returns empty string for unrecognized input.
func LangNameToISO(name string) string {
	if name == "" {
		return ""
	}
	lower := strings.ToLower(name)
	if code, ok := langNameMap[lower]; ok {
		return code
	}
	// If it's already a 2-letter ASCII code, return as-is.
	if len(lower) == 2 &&
		lower[0] >= 'a' && lower[0] <= 'z' &&
		lower[1] >= 'a' && lower[1] <= 'z' {
		return lower
	}
	return ""
}
