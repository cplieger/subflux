package language

import (
	"strings"
	"sync"

	"github.com/cplieger/langtag/v3/names"
)

const maxNameBytes = 64

// FromName reads a language as an English name (CLDR's, a pickable tag's
// composed name, or Sonarr's and Radarr's, in any ASCII case) or, failing
// that, as a code Parse reads. It answers false for empty input, input over
// 64 bytes after trimming, and the arr values Unknown, Any and Original.
func FromName(name string) (Tag, bool) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameBytes {
		return Tag{}, false
	}
	key := asciiLower(name)
	if _, meta := arrMetaNames[key]; meta {
		return Tag{}, false
	}
	if t, ok := nameIndex()[key]; ok {
		return t, true
	}
	return Parse(name)
}

// arrMetaNames are the lowercased Sonarr and Radarr language values that name
// no language; Parse would read "any" as Anyin.
var arrMetaNames = map[string]struct{}{"unknown": {}, "any": {}, "original": {}}

// otherEnglishNames maps lowercased English names Sonarr and Radarr use that
// CLDR en spells otherwise, each pinned by testdata/arr-languages.txt.
var otherEnglishNames = map[string]string{
	"spanish (latino)": latinAmericanSpanish,
	"flemish":          "nl-BE",
	"bengali":          "bn",
}

// nameIndex maps a lowercased English name to its tag: every langtag name,
// then every pickable tag's composed name, then otherEnglishNames.
var nameIndex = sync.OnceValue(func() map[string]Tag {
	index := languageNameIndex(names.Languages())
	for _, n := range pickableNames() {
		index[asciiLower(n.English)] = n.Tag
	}
	for name, tag := range otherEnglishNames {
		t, _ := Parse(tag)
		index[name] = t
	}
	return index
})

// languageNameIndex maps each lowercased name to Parse of its code; where
// two codes share a name the two-letter one wins, else the first listed.
func languageNameIndex(langs []names.Language) map[string]Tag {
	index := map[string]Tag{}
	twoLetter := map[string]bool{}
	for _, l := range langs {
		key := asciiLower(l.Name)
		if _, taken := index[key]; taken && (twoLetter[key] || len(l.Code) != 2) {
			continue
		}
		if t, ok := Parse(l.Code); ok {
			index[key], twoLetter[key] = t, len(l.Code) == 2
		}
	}
	return index
}
