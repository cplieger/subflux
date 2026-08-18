package api

import (
	"strings"

	"github.com/cplieger/langtag/v2"
)

// LangBrazilianPortuguese is subflux's internal code for Brazilian Portuguese.
//
// It is not an ISO 639 code. Subflux invented it so that a Brazilian target
// stays distinguishable from a European Portuguese one inside a two-letter code
// space, and it is load-bearing well beyond memory: it appears in user
// configuration, in the subtitle filename on disk (movie.pb.srt), and in the
// bbolt state key. langtag rejects it for the same reason it rejects any tag the
// IANA registry does not list, so it is translated at the boundary instead of
// being handed to Parse.
const LangBrazilianPortuguese = "pb"

// LangBrazilianPortugueseTag is the BCP 47 tag that LangBrazilianPortuguese
// stands for. Any canonicalization landing on this tag yields the internal code.
const LangBrazilianPortugueseTag = "pt-BR"

// langAliases are the identifiers subflux accepts whose canonical BCP 47 form it
// cannot use. Each one must be recognized before langtag.Parse, never as a
// fallback after it, because Parse either rejects them or answers with something
// the internal space cannot hold.
//
// Two reasons put a code here, and nothing else does:
//
//   - The IANA registry does not list it. "pb" and "pob" are subflux's own
//     spelling of Brazilian Portuguese, so Parse rejects both.
//   - The registry lists it, but its canonical form has no two-letter spelling.
//     Probing all 187 ISO 639-1 assignments (including the three deprecated
//     ones) finds exactly two: "tl" canonicalizes to "fil" and "bh" to "bho".
//     Both are real assignments the two-letter space can hold, so they stay as
//     written rather than becoming "". "tl" is live — langNameMap resolves arr's
//     "Tagalog" to it — and "bh" is the same class, closed here so the rule has
//     no exceptions rather than because a caller needs it.
//
// Provider-private spellings deliberately stay out. They are wire formats owned
// by one provider's dialect table, not identifiers subflux accepts: hdbits means
// Brazilian by "br" and Slovenian by "si", while the registry reads those as
// Breton and Sinhala. Admitting one here would apply one provider's private
// meaning to every source.
var langAliases = map[string]string{
	LangBrazilianPortuguese: LangBrazilianPortuguese,
	"pob":                   LangBrazilianPortuguese,
	"tl":                    "tl",
	"bh":                    "bh",
}

// CanonicalLangCode maps one raw language identifier — from a provider payload,
// an ffprobe stream tag, an arr API response, or a config file — onto subflux's
// internal code space, and returns "" when the input names no language subflux
// can represent.
//
// The internal space is ISO 639-1 two-letter codes plus
// LangBrazilianPortuguese. That is not a naming preference: the code becomes a
// segment of the subtitle filename and part of the bbolt state key, so a code
// outside the space cannot survive a round trip through a scan.
//
// Two rules follow from the space rather than from any standard, which is why
// they live here and not in langtag:
//
//   - pt-BR yields "pb", not "pt". Region is consulted before it is discarded,
//     because European and Brazilian Portuguese are separate subtitle targets.
//   - A language with no ISO 639-1 assignment yields "". langtag's Tag.Language
//     reports a three-letter subtag for those (Cantonese is "yue", Filipino
//     "fil"), and admitting one would place a three-letter code in a
//     two-letter namespace, where it matches no configured target and no
//     display name — silently, forever.
func CanonicalLangCode(raw string) string {
	if raw == "" {
		return ""
	}
	if code, ok := langAliases[strings.ToLower(strings.TrimSpace(raw))]; ok {
		return code
	}
	t, ok := langtag.Parse(raw)
	if !ok {
		return ""
	}
	if t.String() == LangBrazilianPortugueseTag {
		return LangBrazilianPortuguese
	}
	// Tag.Language folds macrolanguages and deprecated codes (nob and nor both
	// report "no", iw reports "he"), which is exactly the collapse the internal
	// space wants. Its width is the part langtag does not promise.
	base := t.Language()
	if len(base) != 2 {
		return ""
	}
	return base
}

// ValidLangCode reports whether raw is a language code subflux can act on: it
// names a real language, and it is already written the way the internal space
// spells it.
//
// The second half is what makes this useful for validating configuration. A
// code that merely canonicalizes to something valid is not enough, because
// nothing canonicalizes a configured target at match time — subtitle targets
// are compared against provider results by exact string. So "eng" names a
// language and still matches nothing, and telling a user their config is fine
// would be a lie. Callers report CanonicalLangCode(raw) as the spelling to use.
func ValidLangCode(raw string) bool {
	code := CanonicalLangCode(raw)
	return code != "" && code == raw
}
