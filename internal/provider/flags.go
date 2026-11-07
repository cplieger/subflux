package provider

import "strings"

// rule is one entry of a variant table: a match with polarity false denies
// the variant whatever else the text says.
type rule struct {
	pattern  string
	polarity bool
}

// forcedRules lists its negatives first: a comment saying the foreign parts
// are hardcoded describes a full track (SubSource's recorded Inception
// answer, subtitle 1062555).
var forcedRules = []rule{
	{"hardcoded", false},
	{"hard-coded", false},
	{"hard coded", false},
	{"forced", true},
	{"foreign", true},
}

// hearingImpairedRules lists its negatives first: each answers a real name
// ("Non-SDH", "HI removed") that a positive below would otherwise flag, and
// the first match decides.
var hearingImpairedRules = []rule{
	{"hi remove", false},
	{"non hi", false},
	{"nonhi", false},
	{"non-hi", false},
	{"non-sdh", false},
	{"non sdh", false},
	{"nonsdh", false},
	{"sdh remove", false},
	{"[hi]", true},
	{"(hi)", true},
	{"hearing impaired", true},
	{"_hi_", true},
	{" hi ", true},
	{".hi.", true},
	{"_cc_", true},
	{" cc ", true},
	{".cc.", true},
	{"sdh", true},
	{"closed caption", true},
}

// match reads each text by its first matching rule: true when one text reads
// positive and none reads negative, so the order of texts never matters.
func match(rules []rule, texts []string) bool {
	positive := false
	for _, t := range texts {
		t = strings.ToLower(t)
		for _, r := range rules {
			if strings.Contains(t, r.pattern) {
				if !r.polarity {
					return false
				}
				positive = true
				break
			}
		}
	}
	return positive
}

// HearingImpaired reports whether the provider flags a file hearing-impaired
// or any of texts (its release name, file name or comment) says so while none
// denies it.
func HearingImpaired(flagged bool, texts ...string) bool {
	return flagged || match(hearingImpairedRules, texts)
}

// Forced reports whether the provider flags a file forced or any of texts
// names it a forced or foreign-parts track.
func Forced(flagged bool, texts ...string) bool {
	return flagged || match(forcedRules, texts)
}
