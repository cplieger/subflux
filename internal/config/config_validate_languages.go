package config

import (
	"errors"
	"fmt"

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

// validateTarget checks a single subtitle target for validity.
func validateTarget(t *yamlSubtitleTarget, ctx string) error {
	if t.Code == "" {
		return fmt.Errorf("subtitle code cannot be empty (%s)", ctx)
	}
	if t.MinScore != nil {
		if err := validateScoreRange(*t.MinScore, fmt.Sprintf("subtitle min_score (%s, code=%s)", ctx, t.Code)); err != nil {
			return err
		}
	}
	return nil
}

// validateLanguages checks that at least one rule or default exists,
// that all audio and subtitle codes are non-empty, and that no two
// rules share the same audio language (only the first would match).
func validateLanguages(lang *LanguageRules) error {
	if len(lang.Default) == 0 {
		return ErrNoDefaultLang
	}
	seenAudio := make(map[string]struct{}, len(lang.Rules))
	for _, rule := range lang.Rules {
		if rule.Audio == "" {
			return errors.New("audio language code cannot be empty in rule")
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
	}
	for i := range lang.Default {
		if err := validateTarget(&lang.Default[i], "default"); err != nil {
			return err
		}
	}
	return nil
}
