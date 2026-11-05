package release

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// cutWords maps each word of an EditionRegex capture to its cut token; a
// word absent here is presentation or a connector (Cut, Edition, Version).
var cutWords = map[string]Cut{
	"theatrical":    CutTheatrical,
	"extended":      CutExtended,
	"directors":     CutDirectors,
	"ultimate":      CutUltimate,
	"final":         CutFinal,
	"assembly":      CutAssembly,
	"recut":         CutRecut,
	"despecialized": CutDespecialized,
	"special":       CutSpecial,
	"unrated":       CutUnrated,
	"uncut":         CutUncut,
	"uncensored":    CutUncensored,
	"fanedit":       CutFanEdit,
}

// edition is Radarr's ParseEdition: ReportEditionRegex puts ^.+? before
// EditionRegex, so the match starts after the first rune.
func (t *parseTables) edition(s string) string {
	if s == "" {
		return ""
	}
	_, w := utf8.DecodeRuneInString(s)
	m := t.editions.Find(s, w)
	if m == nil {
		return ""
	}
	e, _ := group(t.editions, s, m, "edition")
	return strings.ReplaceAll(e, ".", " ")
}

// cutOf reads an edition's words, joining the two the regex lets a
// separator split: Director.?s and Fan.?Edit.
func cutOf(edition string) Cut {
	words := strings.FieldsFunc(strings.ToLower(edition), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	var c Cut
	for i := 0; i < len(words); i++ {
		w := words[i]
		if i+1 < len(words) && (w == "director" && words[i+1] == "s" || w == "fan" && words[i+1] == "edit") {
			w += words[i+1]
			i++
		}
		c |= cutWords[w]
	}
	return c
}
