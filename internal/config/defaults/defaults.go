// Package defaults provides shared configuration constants and helpers
// used by both config/ and config/schema/. Extracting these into a leaf
// package prevents a circular import risk between config and schema.
package defaults

import (
	"strconv"
	"time"
)

// Default config string constants.
const (
	ExcludeTag = "no-subflux"
	LogLevel   = "info"
	LogFormat  = "json"
)

// Embedded subtitle policy defaults (embedded_subtitles config section).
// Declared once here; consumed by both the config pre-defaulted decode and
// the schema section builder so the two can never drift.
const (
	EmbeddedIgnorePGS    = true
	EmbeddedIgnoreVobSub = true
	EmbeddedIgnoreASS    = false
)

// Default duration and numeric values used by both config loading and schema.
const (
	DefaultPollInterval      = 30 * time.Second
	DefaultScanInterval      = 24 * time.Hour
	DefaultScanDelay         = 5 * time.Second
	DefaultProviderTimeout   = time.Hour
	DefaultUpgradeWindowDays = 7
	DefaultMaxSSEClients     = 32
	DefaultAdaptiveInitDelay = 7 * 24 * time.Hour
	DefaultAdaptiveMaxDelay  = 3 * 730 * time.Hour
	DefaultBackoffMultiplier = 2
	DefaultBackupFrequency   = 24 * time.Hour
	DefaultBackupRetention   = 7
)

// Minimum validation thresholds — the floor below which config is rejected.
const (
	MinPollInterval    = 10 * time.Second
	MinScanDelay       = 5 * time.Second
	MinProviderTimeout = time.Hour
	MinScanInterval    = time.Hour
	MinBackupFrequency = time.Hour
)

// Session timeout defaults.
const (
	DefaultSessionIdleTimeout     = 24 * time.Hour
	DefaultSessionAbsoluteTimeout = 7 * 24 * time.Hour
)

// Score range bounds for min_score validation.
const (
	MinScoreValue = 0
	MaxScoreValue = 100
)

// FormatDuration converts a time.Duration to a human-friendly config string.
// Prefers the largest clean unit (M > D > h > m > s). Sub-second precision
// is truncated to whole seconds; callers should only pass durations that
// are meaningful at second granularity or above.
func FormatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	hours := int(d.Hours())
	months := int(d / (730 * time.Hour))
	if months > 0 && d == time.Duration(months)*730*time.Hour {
		return strconv.Itoa(months) + "M"
	}
	if hours >= 24 && hours%24 == 0 {
		days := hours / 24
		return strconv.Itoa(days) + "D"
	}
	if hours > 0 && d == time.Duration(hours)*time.Hour {
		return strconv.Itoa(hours) + "h"
	}
	mins := int(d.Minutes())
	if mins > 0 && d == time.Duration(mins)*time.Minute {
		return strconv.Itoa(mins) + "m"
	}
	secs := int(d.Seconds())
	return strconv.Itoa(secs) + "s"
}
