package config

import (
	"fmt"
	"math"

	"github.com/cplieger/subflux/internal/config/defaults"
	"github.com/cplieger/subflux/internal/subflux"
)

// validateSearch checks search settings for consistency.
// Accumulates all errors rather than returning on the first failure.
func validateSearch(s *yamlSearchConfig) error {
	var ve validationErrors
	ve.add(validateScoreRange(s.MinScore, "search.min_score"))
	ve.add(validateDurationConstraints([]durationConstraint{
		{"search.provider_timeout", s.ProviderTimeout.D, defaults.MinProviderTimeout, true},
		{"search.scan_delay", s.ScanDelay.D, defaults.MinScanDelay, false},
		{"search.scan_interval", s.ScanInterval.D, defaults.MinScanInterval, false},
	}))
	if s.DownloadMaxAttempts <= 0 {
		s.DownloadMaxAttempts = subflux.DefaultDownloadMaxAttempts
	}
	if s.UpgradeEnabled && s.UpgradeWindowDays <= 0 {
		ve.add(&fieldDependencyError{
			Field:     "search.upgrade_window_days",
			DependsOn: "search.upgrade_enabled",
			Reason:    fmt.Sprintf("search.upgrade_window_days must be positive when upgrades are enabled, got %d", s.UpgradeWindowDays),
		})
	}
	if err := ve.err(); err != nil {
		return fmt.Errorf("%w: %w", errSearchConfig, err)
	}
	return nil
}

// validateAdaptive checks adaptive search backoff settings for consistency.
// Accumulates all errors rather than returning on the first failure.
func validateAdaptive(a *yamlAdaptiveConfig) error {
	if !a.Enabled {
		return nil
	}
	var ve validationErrors
	// NaN compares false against every bound (NaN < 1 is false), so it would
	// slip through a plain range check and later defeat the max-delay clamp in
	// the backoff computation (NaN > max is also false). Require a finite
	// value explicitly. YAML accepts .nan/.inf literals, so this is reachable
	// from a config file.
	if math.IsNaN(a.BackoffMultiplier) || math.IsInf(a.BackoffMultiplier, 0) || a.BackoffMultiplier < 1 {
		ve.add(configFieldErr(fmt.Sprintf("adaptive.backoff_multiplier must be a finite number >= 1, got %g", a.BackoffMultiplier)))
	}
	if a.InitialDelay.D <= 0 {
		ve.add(&fieldDependencyError{
			Field:     "adaptive.initial_delay",
			DependsOn: "adaptive.enabled",
			Reason:    "adaptive.initial_delay must be positive when adaptive is enabled",
		})
	}
	if a.MaxDelay.D < a.InitialDelay.D {
		ve.add(configFieldErr(fmt.Sprintf("adaptive.max_delay (%s) must be >= initial_delay (%s)", a.MaxDelay.D, a.InitialDelay.D)))
	}
	if a.MaxAttempts < 0 {
		ve.add(configFieldErr(fmt.Sprintf("adaptive.max_attempts must be non-negative, got %d", a.MaxAttempts)))
	}
	if err := ve.err(); err != nil {
		return fmt.Errorf("%w: %w", errAdaptiveConfig, err)
	}
	return nil
}
