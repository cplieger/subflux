package config

import (
	"errors"
	"fmt"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config/defaults"
)

// validateScoreRange checks that a score value is within [defaults.MinScoreValue, defaults.MaxScoreValue].
func validateScoreRange(value int, field string) error {
	if value < defaults.MinScoreValue || value > defaults.MaxScoreValue {
		return configFieldErr(field,
			fmt.Sprintf("%s must be between %d and %d, got %d",
				field, defaults.MinScoreValue, defaults.MaxScoreValue, value))
	}
	return nil
}

// validateLangCode checks that a user-authored language code is one subflux can
// act on. An unusable code is rejected at load time rather than accepted and
// then silently matched against nothing for the lifetime of the install, which
// is what a typo used to buy.
//
// The code must be spelled the way the internal space spells it, not merely be
// canonicalizable to it: nothing canonicalizes a configured code at match time,
// so "eng" names a real language and still matches no provider result. When a
// canonical spelling exists the error names it, so any rejected config is one
// edit from working.
func validateLangCode(code, field, ctx string) error {
	if api.ValidLangCode(code) {
		return nil
	}
	if canon := api.CanonicalLangCode(code); canon != "" {
		return configFieldErr(field, fmt.Sprintf(
			"%s %q (%s) is not the code subflux uses for that language; use %q",
			field, code, ctx, canon))
	}
	return configFieldErr(field, fmt.Sprintf(
		"%s %q (%s) is not a known language code", field, code, ctx))
}

// validateTarget checks a single subtitle target for validity.
func validateTarget(t *yamlSubtitleTarget, ctx string) error {
	if t.Code == "" {
		return fmt.Errorf("subtitle code cannot be empty (%s)", ctx)
	}
	if err := validateLangCode(t.Code, "subtitle code", ctx); err != nil {
		return err
	}
	if t.MinScore != nil {
		if err := validateScoreRange(*t.MinScore, fmt.Sprintf("subtitle min_score (%s, code=%s)", ctx, t.Code)); err != nil {
			return err
		}
	}
	return nil
}

// validateAudioRule checks one audio rule: its code names a language subflux can
// act on, it has not already been claimed by an earlier rule (only the first
// would ever match), and every subtitle target under it is valid. seenAudio is
// updated with the rule's code.
func validateAudioRule(rule *AudioRule, seenAudio map[string]struct{}) error {
	if rule.Audio == "" {
		return errors.New("audio language code cannot be empty in rule")
	}
	// An audio code is matched by exact string against a resolved original
	// language, so a typo here fails exactly as silently as one in a target.
	if err := validateLangCode(rule.Audio, "audio language code", "rule"); err != nil {
		return err
	}
	if _, dup := seenAudio[rule.Audio]; dup {
		return fmt.Errorf("%w: %s", ErrDuplicateAudioRule, rule.Audio)
	}
	seenAudio[rule.Audio] = struct{}{}
	for i := range rule.Subtitles {
		if err := validateTarget(&rule.Subtitles[i], fmt.Sprintf("rule audio=%s", rule.Audio)); err != nil {
			return err
		}
	}
	return nil
}

// validateLanguages checks that at least one rule or default exists,
// that all audio and subtitle codes are non-empty and name a language
// subflux can act on, and that no two rules share the same audio language
// (only the first would match).
func validateLanguages(lang *LanguageRules) error {
	if len(lang.Default) == 0 {
		return ErrNoDefaultLang
	}
	seenAudio := make(map[string]struct{}, len(lang.Rules))
	for i := range lang.Rules {
		if err := validateAudioRule(&lang.Rules[i], seenAudio); err != nil {
			return err
		}
	}
	for i := range lang.Default {
		if err := validateTarget(&lang.Default[i], "default"); err != nil {
			return err
		}
	}
	return nil
}
