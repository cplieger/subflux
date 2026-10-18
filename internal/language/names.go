package language

import (
	"cmp"
	"slices"
	"sync"

	"github.com/cplieger/langtag/v3"
	"github.com/cplieger/langtag/v3/names"
)

// Name is one tag a person can pick as a target, with its English name and
// the other pickable tags it grades Exact, Substitute or ManualOnly.
type Name struct {
	Tag     Tag
	English string
	Close   []CloseName
}

// CloseName is one pickable tag graded against a Name's tag as the target.
type CloseName struct {
	English   string
	Tag       Tag
	Closeness Closeness
	Class     Class
}

// Names lists the pickable targets sorted by English name, each Close list
// sorted by class, closeness and name. The answer is the caller's to change.
func Names() []Name {
	built := pickableNames()
	out := make([]Name, len(built))
	for i, n := range built {
		out[i] = n
		out[i].Close = slices.Clone(n.Close)
	}
	return out
}

// EnglishName is the English name of any tag ("Portuguese (Brazil)"), its
// canonical text when langtag names none, "" for the zero Tag.
func EnglishName(t Tag) string {
	if name, ok := names.English(t.t); ok {
		return name
	}
	return t.String()
}

// unpickable are two-letter codes that name no selectable target: bare zh
// claims no script, and bh is a collective code.
var unpickable = map[string]bool{"zh": true, "bh": true}

// varieties are the region and script tags offered beside the languages,
// the ones providers label or readers need apart; extras are the pickable
// languages with no two-letter code.
var (
	varieties = []string{"pt-BR", "pt-PT", latinAmericanSpanish, "es-ES", "fr-CA", "fr-FR", "zh-Hans", "zh-Hant", "zh-Hant-HK", "sr-Latn", "sr-Cyrl"}
	extras    = []string{"fil", "yue", "cnr"}
)

// pickableTags is every two-letter language subtag langtag names whose Parse
// text is itself, but unpickable, then extras and varieties.
func pickableTags() []Tag {
	var tags []Tag
	for _, l := range names.Languages() {
		if len(l.Code) != 2 || unpickable[l.Code] {
			continue
		}
		if t, ok := Parse(l.Code); ok && t.String() == l.Code {
			tags = append(tags, t)
		}
	}
	for _, s := range slices.Concat(extras, varieties) {
		tags = append(tags, Tag{langtag.MustParse(s)})
	}
	return tags
}

var pickableNames = sync.OnceValue(func() []Name {
	tags := pickableTags()
	out := make([]Name, len(tags))
	for i, target := range tags {
		out[i] = Name{Tag: target, English: EnglishName(target)}
		for _, have := range tags {
			if have == target {
				continue
			}
			g := Policy{}.Grade(target, have)
			if g.Class == ClassUnrelated {
				continue
			}
			out[i].Close = append(out[i].Close, CloseName{Tag: have, English: EnglishName(have), Closeness: g.Closeness, Class: g.Class})
		}
		slices.SortFunc(out[i].Close, func(a, b CloseName) int {
			return cmp.Or(cmp.Compare(a.Class, b.Class), cmp.Compare(a.Closeness, b.Closeness), cmp.Compare(a.English, b.English))
		})
	}
	slices.SortFunc(out, func(a, b Name) int {
		return cmp.Or(cmp.Compare(a.English, b.English), cmp.Compare(a.Tag.String(), b.Tag.String()))
	})
	return out
})
