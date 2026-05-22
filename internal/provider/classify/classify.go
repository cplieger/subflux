// Package classify provides subtitle classification (forced/HI) and language
// resolution utilities consumed by provider sub-packages.
package classify

import "strings"

// Rule defines a single subtitle classification entry.
// Polarity determines the match outcome: true = positive (confirms the
// classification), false = negative (denies it, overrides positive matches).
type Rule struct {
	Pattern  string
	Polarity bool
}

// ForcedRules is the declarative table for forced/foreign-parts classification.
// All entries are positive (any match confirms forced status).
var ForcedRules = []Rule{
	{"forced", true},
	{"foreign", true},
}

// HearingImpairedRules is the declarative table for HI/SDH/CC classification.
// Negative entries (Polarity=false) deny HI status and take precedence over
// positive entries. The first matching entry determines the result.
var HearingImpairedRules = []Rule{
	// Negative: deny HI despite containing HI-like keywords.
	{"hi remove", false},
	{"non hi", false},
	{"nonhi", false},
	{"non-hi", false},
	{"non-sdh", false},
	{"non sdh", false},
	{"nonsdh", false},
	{"sdh remove", false},
	// Positive: confirm HI/SDH/CC.
	{"_hi_", true},
	{" hi ", true},
	{".hi.", true},
	{"_cc_", true},
	{" cc ", true},
	{".cc.", true},
	{"sdh", true},
	{"closed caption", true},
}

// IsForced returns true if the comment string indicates a forced or
// foreign-parts subtitle track.
func IsForced(comment string) bool {
	c := strings.ToLower(comment)
	for _, rule := range ForcedRules {
		if strings.Contains(c, rule.Pattern) {
			return rule.Polarity
		}
	}
	return false
}

// IsHearingImpaired reports whether commentary or a filename indicates a
// hearing-impaired / SDH / closed-caption subtitle. A filename containing
// "_hi_" short-circuits to true regardless of commentary. Negative rules
// ("non hi", "non sdh", etc.) win over positive ones. The filename is
// optional: providers without a filename field should pass "".
func IsHearingImpaired(commentary, filename string) bool {
	if filename != "" && strings.Contains(strings.ToLower(filename), "_hi_") {
		return true
	}
	if commentary == "" {
		return false
	}
	c := strings.ToLower(commentary)
	for _, rule := range HearingImpairedRules {
		if strings.Contains(c, rule.Pattern) {
			return rule.Polarity
		}
	}
	return false
}
