package language

import (
	"slices"
	"strings"
	"unicode"
)

// ReadFlags is what free texts (a release or file name, a provider's comment,
// a track title) say of a subtitle's flags. A flag is set when one text names
// it and none denies it, so the order of texts never matters. A word is a run
// of letters and digits matched whole, so "Foreigner", "Reinforced" and
// "Hi10p" name nothing. The Title-case word "Hi" is the greeting ("Hi-Score",
// "Oh.Hi.Mark") and names nothing, though a denial with it ("Non-Hi") still
// denies, while "HI" and "hi" name HI.
func ReadFlags(texts ...string) Flags {
	var hi, forced mention
	for _, t := range texts {
		h, f := readFlagText(t)
		hi, forced = max(hi, h), max(forced, f)
	}
	return Flags{HI: hi == named, Forced: forced == named}
}

// mention is what one text says of one flag, zero for nothing. Its order is
// load-bearing: max combines readings, so a denial outranks a naming.
type mention uint8

const (
	named mention = iota + 1
	denied
)

type flagKind uint8

const (
	flagHI flagKind = iota
	flagForced
)

const wordForced = "forced"

type flagPhrase struct {
	words []string
	kind  flagKind
	says  mention
}

// flagPhrases are matched at each word. Foreign parts hardcoded into the
// video leave the file a full track (SubSource's Inception answer, subtitle
// 1062555), so "hardcoded" denies Forced wherever the text says it.
var flagPhrases = []flagPhrase{
	{[]string{"hi"}, flagHI, named},
	{[]string{"sdh"}, flagHI, named},
	{[]string{"cc"}, flagHI, named},
	{[]string{"hearing", "impaired"}, flagHI, named},
	{[]string{"hard", "of", "hearing"}, flagHI, named},
	{[]string{"closed", "caption"}, flagHI, named},
	{[]string{"closed", "captions"}, flagHI, named},
	{[]string{wordForced}, flagForced, named},
	{[]string{"foreign"}, flagForced, named},
	{[]string{"hardcoded"}, flagForced, denied},
	{[]string{"hard", "coded"}, flagForced, denied},
}

// A naming phrase is denied by a negator word before it ("non-SDH", "Not
// Forced"), a removal word after it ("HI removed"), or a negator prefix
// closed onto a one-word phrase ("NonSDH", "unforced").
var (
	flagNegators       = []string{"non", "not"}
	flagRemovals       = []string{"removed", "remove"}
	flagClosedNegators = []string{"non", "not", "un"}
)

var closedDenials = func() map[string]flagKind {
	m := map[string]flagKind{}
	for _, p := range flagPhrases {
		if len(p.words) == 1 && p.says == named {
			for _, n := range flagClosedNegators {
				m[n+p.words[0]] = p.kind
			}
		}
	}
	return m
}()

func readFlagText(text string) (hi, forced mention) {
	words, greeting := flagWords(text)
	var says [2]mention
	for i, w := range words {
		if kind, ok := closedDenials[w]; ok {
			says[kind] = denied
			continue
		}
		for _, p := range flagPhrases {
			says[p.kind] = max(says[p.kind], p.at(words, i, greeting[i]))
		}
	}
	return says[flagHI], says[flagForced]
}

func (p flagPhrase) at(words []string, i int, greeting bool) mention {
	end := i + len(p.words)
	if end > len(words) || !slices.Equal(words[i:end], p.words) {
		return 0
	}
	if p.says != named {
		return p.says
	}
	if i > 0 && slices.Contains(flagNegators, words[i-1]) ||
		end < len(words) && slices.Contains(flagRemovals, words[end]) {
		return denied
	}
	// The greeting is exempt from naming only, so "Non-Hi" and "Hi removed"
	// still deny as "NonHi" and "non-hi" do.
	if greeting {
		return 0
	}
	return named
}

func flagWords(text string) (words []string, greeting []bool) {
	words = strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	greeting = make([]bool, len(words))
	for i, w := range words {
		greeting[i] = w == "Hi"
		words[i] = strings.ToLower(w)
	}
	return words, greeting
}
