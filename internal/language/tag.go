package language

import (
	"strings"

	"github.com/cplieger/langtag/v3"
)

// Tag is a canonical BCP 47 language tag, folded to its representative: Parse
// and every reader built on it fold zh-CN to zh-Hans, zh-TW to zh-Hant and
// zh-HK to zh-Hant-HK, the inverse of FilenameTag, and a region implying its
// script states it (ku-IQ to ku-Arab-IQ). == is canonical equality; the zero
// Tag means no language.
type Tag struct{ t langtag.Tag }

// String is the canonical text, "" for the zero Tag; Parse is its inverse.
func (t Tag) String() string { return t.t.String() }

// Parse canonicalizes raw (ISO 639-1, 639-2, 639-3 or BCP 47, any ASCII case)
// and folds it to its representative. "" is the zero Tag and true; any other
// input naming no language is the zero Tag and false. Parse reads no filename
// exception: ReadFilename does.
func Parse(raw string) (Tag, bool) {
	if raw == "" {
		return Tag{}, true
	}
	lt, ok := langtag.Parse(raw)
	if !ok {
		return Tag{}, false
	}
	if lt.Language() == chineseLanguage {
		lt, ok = foldChinese(lt)
	} else {
		lt, ok = foldScript(lt)
	}
	if !ok {
		return Tag{}, false
	}
	return Tag{lt}, true
}

const chineseLanguage = "zh"

// The script subtags the Chinese fold and the title refinements name.
const (
	scriptHans = "Hans"
	scriptHant = "Hant"
	scriptLatn = "Latn"
	scriptCyrl = "Cyrl"
)

const latinAmericanSpanish = "es-419"

// chineseForms are the region forms whose meaning is a script, keyed on the
// stated script and region; they match before any resolved script is read,
// so they never depend on langtag's script table.
var chineseForms = map[[2]string]langtag.Tag{
	{"", "CN"}: langtag.MustParse("zh-Hans"), {scriptHans, "CN"}: langtag.MustParse("zh-Hans"),
	{"", "TW"}: langtag.MustParse("zh-Hant"), {scriptHant, "TW"}: langtag.MustParse("zh-Hant"),
	{"", "HK"}: langtag.MustParse("zh-Hant-HK"),
}

// foldChinese spells every Chinese tag (zh, cmn, chi, zho) with zh, folds the
// region forms of chineseForms and then takes the script fold.
func foldChinese(t langtag.Tag) (langtag.Tag, bool) {
	if form, ok := chineseForms[[2]string{t.StatedScript(), t.Region()}]; ok {
		return form, true
	}
	if subtag(t) != chineseLanguage {
		var ok bool
		if t, ok = langtag.Parse(compose(chineseLanguage, t.StatedScript(), t.Region())); !ok {
			return langtag.Tag{}, false
		}
	}
	return foldScript(t)
}

// foldScript states the resolved script of a tag stating a region but no
// script, of a language whose script varies by region, so no held tag's
// script comes from a region lookup that a later CLDR release may change.
func foldScript(t langtag.Tag) (langtag.Tag, bool) {
	if !t.MultiScript() || t.StatedScript() != "" || t.Region() == "" {
		return t, true
	}
	return langtag.Parse(compose(subtag(t), t.Script(), t.Region()))
}

// subtag is the tag's own language subtag, before macrolanguage folding.
func subtag(t langtag.Tag) string {
	s, _, _ := strings.Cut(t.String(), "-")
	return s
}

func compose(lang, script, region string) string {
	parts := []string{lang}
	for _, p := range [...]string{script, region} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, "-")
}
