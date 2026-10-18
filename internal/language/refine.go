package language

import (
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// RefineFromTitle adds to t what an embedded track's title states and t
// lacks: a script for a Chinese, Cantonese or Serbian tag stating none, a
// region for a Portuguese, Spanish or French tag stating none. A title naming
// two results refines nothing, and the language never changes. Bounding the
// title is the caller's.
func RefineFromTitle(t Tag, title string) Tag {
	lang := t.t.Language()
	var lower, adds string
	for _, r := range refinements {
		if !slices.Contains(r.languages, lang) || states(t, r.adds) {
			continue
		}
		if lower == "" {
			lower = strings.ToLower(title)
		}
		if !hasToken(lower, r.token, r.word) || r.adds == adds {
			continue
		}
		if adds != "" {
			return t
		}
		adds = r.adds
	}
	if adds == "" {
		return t
	}
	script, region := t.t.StatedScript(), t.t.Region()
	if isScript(adds) {
		script = adds
	} else {
		region = adds
	}
	if refined, ok := Parse(compose(subtag(t.t), script, region)); ok {
		return refined
	}
	return t
}

// refinement is one title token: it applies to a tag whose Language() is in
// languages and that states no subtag of adds' kind.
type refinement struct {
	token     string // lowercased by strings.ToLower
	adds      string // a script when four letters, else a region
	languages []string
	word      bool // no adjacent unicode.IsLetter rune
}

var (
	chinese = []string{"zh", "yue"}
	serbian = []string{"sr"}
	pt      = []string{"pt"}
	es      = []string{"es"}
	fr      = []string{"fr"}
)

var refinements = []refinement{
	{"traditional", scriptHant, chinese, false},
	{"繁", scriptHant, chinese, false},
	{"正體", scriptHant, chinese, false},
	{"正体", scriptHant, chinese, false},
	{"cht", scriptHant, chinese, true},
	{"big5", scriptHant, chinese, true},
	{"simplified", scriptHans, chinese, false},
	{"简", scriptHans, chinese, false},
	{"簡", scriptHans, chinese, false},
	{"chs", scriptHans, chinese, true},
	{"latin", scriptLatn, serbian, true},
	{"latinica", scriptLatn, serbian, true},
	{"латиница", scriptLatn, serbian, true},
	{"cyrillic", scriptCyrl, serbian, true},
	{"ćirilica", scriptCyrl, serbian, true},
	{"cirilica", scriptCyrl, serbian, true},
	{"ћирилица", scriptCyrl, serbian, true},
	{"brazil", "BR", pt, false},
	{"brasil", "BR", pt, false},
	{"pt-br", "BR", pt, true},
	{"portugal", "PT", pt, true},
	{"european", "PT", pt, true},
	{"latino", "419", es, false},
	{"latin america", "419", es, false},
	{"castellano", "ES", es, true},
	{"castilian", "ES", es, true},
	{"españa", "ES", es, true},
	{"spain", "ES", es, true},
	{"canad", "CA", fr, false},
	{"québec", "CA", fr, false},
	{"quebec", "CA", fr, false},
	{"québécois", "CA", fr, false},
	{"vfq", "CA", fr, true},
}

func isScript(subtag string) bool { return len(subtag) == 4 }

// states reports whether t already states a subtag of adds' kind.
func states(t Tag, adds string) bool {
	if isScript(adds) {
		return t.t.StatedScript() != ""
	}
	return t.t.Region() != ""
}

// hasToken reports whether s holds token, and with word set, an occurrence
// with no letter rune on either side.
func hasToken(s, token string, word bool) bool {
	for from := 0; from < len(s); {
		i := strings.Index(s[from:], token)
		if i < 0 {
			return false
		}
		start := from + i
		end := start + len(token)
		if !word || !letterAt(s[:start], true) && !letterAt(s[end:], false) {
			return true
		}
		from = start + 1
	}
	return false
}

// letterAt reports whether the rune at s's end (last) or start is a letter.
func letterAt(s string, last bool) bool {
	var r rune
	if last {
		r, _ = utf8.DecodeLastRuneInString(s)
	} else {
		r, _ = utf8.DecodeRuneInString(s)
	}
	return unicode.IsLetter(r)
}
