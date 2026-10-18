package language

import "github.com/cplieger/langtag/v3"

// FilenameTag is the language segment subflux writes into a subtitle file
// name: "" for the zero Tag, zh-CN, zh-TW and zh-HK for zh-Hans, zh-Hant and
// zh-Hant-HK (the region forms media servers read), and the canonical text of
// every other tag, at most 12 bytes of [A-Za-z0-9-].
func FilenameTag(t Tag) string {
	if s, ok := filenameChinese[t]; ok {
		return s
	}
	return t.String()
}

var filenameChinese = map[Tag]string{
	{langtag.MustParse("zh-Hans")}:    "zh-CN",
	{langtag.MustParse("zh-Hant")}:    "zh-TW",
	{langtag.MustParse("zh-Hant-HK")}: "zh-HK",
}

// ReadFilename reads one language segment of a subtitle file name: a spelling
// other tools commonly write (pb, cht, es-la, in any ASCII case) reads as the
// tag it means there, and anything else as Parse reads it. "" is the zero Tag
// and true.
func ReadFilename(segment string) (Tag, bool) {
	if e, ok := readExceptions[asciiLower(segment)]; ok {
		return e.tag, true
	}
	return Parse(segment)
}

// exceptionRow is on-disk spellings, each refused by Parse or read by it as
// another tag, that mean tag (in English, meaning), with the evidence that
// they are in common use.
type exceptionRow struct {
	tag       string
	meaning   string
	evidence  string
	spellings []string
}

type readException struct {
	meaning string
	tag     Tag
}

const bazarrCustomLang = "https://github.com/morpheus65535/bazarr/blob/bfe4593b04caa16eb4a09c2a233777b0bd7723b6/bazarr/languages/custom_lang.py"

// cht, spl and es-la are also real tags (Cholon, Selepet, Spanish in Laos);
// ParsePolicy refuses those three as targets, so every target reads back from
// its own file name.
var exceptionRows = []exceptionRow{
	{"pt-BR", "Brazilian Portuguese", "Bazarr custom_lang.py " + bazarrCustomLang + "#L26; OpenSubtitles.org's language id pob, https://www.opensubtitles.org/en/search/sublanguageid-pob", []string{"pb", "pob"}},
	{"zh-Hant", "Traditional Chinese", "Bazarr custom_lang.py " + bazarrCustomLang + "#L154", []string{"cht", "zht", "tc", "hant", "zhhant", "big5"}},
	{"zh-Hans", "Simplified Chinese", "Bazarr custom_lang.py " + bazarrCustomLang + "#L168", []string{"chs", "zhs", "gb", "hans", "zhhans"}},
	{latinAmericanSpanish, "Latin American Spanish", "Bazarr custom_lang.py " + bazarrCustomLang + "#L230", []string{"ea", "spl", "es-la", "spa-la", "lat"}},
}

// readExceptions is keyed by the ASCII-lowercased spelling.
var readExceptions = func() map[string]readException {
	table := map[string]readException{}
	for _, r := range exceptionRows {
		e := readException{tag: Tag{langtag.MustParse(r.tag)}, meaning: r.meaning}
		for _, k := range r.spellings {
			table[k] = e
		}
	}
	return table
}()

// asciiLower folds only A-Z, as RFC 5646 section 2.1 folds a tag's case.
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
