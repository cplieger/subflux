package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/config/defaults"
)

// Sentinel errors for the most common config validation failures.
// These enable errors.Is dispatch instead of string matching.
var (
	// ErrNoProvider indicates no subtitle provider is enabled.
	ErrNoProvider = errors.New("at least one provider must be enabled")

	// ErrNoArr indicates neither Sonarr nor Radarr is configured.
	ErrNoArr = errors.New("at least one of sonarr or radarr must be configured")

	// ErrNoDefaultLang indicates the languages.default section is empty.
	ErrNoDefaultLang = errors.New("languages.default must contain at least one subtitle target; every item must have a fallback set of subtitles to look for")

	// ErrDuplicateAudioRule indicates a duplicate audio language rule was found.
	ErrDuplicateAudioRule = errors.New("duplicate audio language rule")

	// ErrSearchConfig indicates an invalid search configuration.
	ErrSearchConfig = errors.New("invalid search configuration")

	// ErrAdaptiveConfig indicates an invalid adaptive configuration.
	ErrAdaptiveConfig = errors.New("invalid adaptive configuration")

	// ErrLoggingConfig indicates an invalid logging configuration.
	ErrLoggingConfig = errors.New("invalid logging configuration")

	// ErrPostProcessConfig indicates an invalid post-processing configuration.
	ErrPostProcessConfig = errors.New("invalid post-processing configuration")

	// ErrMissingAPIKey indicates a required API key is not configured.
	ErrMissingAPIKey = errors.New("API key required")
)

// FieldDependencyError is a typed error for config field-requires-field
// constraint violations. Callers can use errors.As to programmatically
// identify which field combinations are invalid.
type FieldDependencyError struct {
	Field     string // the field that has the constraint
	DependsOn string // the field it depends on
	Reason    string // human-readable explanation
}

func (e *FieldDependencyError) Error() string {
	return fmt.Sprintf("%s requires %s: %s", e.Field, e.DependsOn, e.Reason)
}

// Validate checks that the Config has the minimum required configuration.
func (c *Config) Validate() error { return validate(context.Background(), c) }

// ValidationError is a structured validation error that identifies the
// offending config field. Callers can use errors.As to extract the field name
// for targeted UI display or programmatic handling.
type ValidationError struct {
	Field   string // e.g. "search.min_score", "sonarr.api_key"
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// configFieldErr constructs a ValidationError for the given field.
func configFieldErr(field, msg string) error {
	return &ValidationError{Field: field, Message: msg}
}

// hasEnabledProvider reports whether at least one provider is enabled.
func hasEnabledProvider(providers map[api.ProviderID]yamlProviderCfg) bool {
	for _, p := range providers {
		if p.Enabled {
			return true
		}
	}
	return false
}

// validate checks that cfg has the minimum required configuration:
// at least one arr endpoint, at least one language rule or default,
// non-empty codes in all rules, and at least one enabled provider.
// Accumulates all validation errors and returns them joined.

// ValidationErrors accumulates multiple validation errors from config
// checking. Sub-validators append directly via Add, eliminating the
// repeated if-err-append boilerplate.
type ValidationErrors struct {
	errs []error
}

// Add appends a non-nil error to the accumulator.
func (ve *ValidationErrors) Add(err error) {
	if err != nil {
		ve.errs = append(ve.errs, err)
	}
}

// Err returns the accumulated errors joined, or nil if none.
func (ve *ValidationErrors) Err() error {
	return errors.Join(ve.errs...)
}

func validate(ctx context.Context, cfg *Config) error {
	var ve ValidationErrors
	ve.Add(validateArrs(cfg))
	ve.Add(validateLanguages(&cfg.Languages))
	if !hasEnabledProvider(cfg.Providers) {
		ve.Add(ErrNoProvider)
	}
	ve.Add(validateDurationConstraints([]durationConstraint{
		{"poll_interval", cfg.PollIntervalCfg.D, defaults.MinPollInterval, false},
	}))
	ve.Add(validateSearch(&cfg.SearchCfg))
	ve.Add(validateAdaptive(&cfg.AdaptiveCfg))
	if cfg.PostProcessing.AudioSyncFallback && !cfg.PostProcessing.SyncSubtitles {
		ve.Add(fmt.Errorf("%w: %w", ErrPostProcessConfig, &FieldDependencyError{
			Field:     "post_processing.audio_sync_fallback",
			DependsOn: "post_processing.sync_subtitles",
			Reason:    "audio_sync_fallback requires sync_subtitles to be enabled",
		}))
	}
	ve.Add(validateLogging(&cfg.Logging))
	ve.Add(validateBackup(&cfg.Backup))
	if _, err := parseTrustedProxies(cfg.TrustedProxies); err != nil {
		ve.Add(err)
	}
	if cfg.Auth.DisableAuth {
		slog.Warn("auth.disable_auth is enabled: ALL authentication is bypassed")
	}
	if cfg.Auth.BasicEnabled != nil && !*cfg.Auth.BasicEnabled && !cfg.Auth.OIDCEnabled {
		ve.Add(errors.New("auth.basic_enabled: password login cannot be disabled unless oidc_enabled is true (otherwise no one could log in); a CLI override can re-enable it"))
	}
	if len(cfg.MediaRootDirs) == 0 {
		slog.Warn("media_roots not configured, all paths will be allowed")
	} else {
		for _, root := range cfg.MediaRootDirs {
			if err := ctx.Err(); err != nil {
				ve.Add(err)
				break
			}
			if _, err := os.Stat(root); err != nil {
				slog.Warn("media root not accessible at config load time",
					"root", root, "error", err)
			}
		}
	}
	return ve.Err()
}

// durationConstraint defines a minimum-duration validation rule.
type durationConstraint struct {
	field   string
	value   time.Duration
	min     time.Duration
	nonZero bool // when true, skip check if value is zero (optional field)
}

// validateDurationConstraints checks a slice of duration constraints,
// returning the first violation found.
func validateDurationConstraints(constraints []durationConstraint) error {
	for _, c := range constraints {
		if c.nonZero && c.value == 0 {
			continue
		}
		if c.value < c.min {
			return configFieldErr(c.field,
				fmt.Sprintf("%s must be at least %s, got %s", c.field, c.min, c.value))
		}
	}
	return nil
}

// validateBackup checks the scheduled-backup settings when enabled.
func validateBackup(c *yamlBackupConfig) error {
	if !c.Enabled {
		return nil
	}
	var ve ValidationErrors
	if c.Retention < 1 {
		ve.Add(configFieldErr("backup.retention", "backup.retention must be at least 1 when backups are enabled"))
	}
	ve.Add(validateDurationConstraints([]durationConstraint{
		{"backup.frequency", c.Frequency.D, defaults.MinBackupFrequency, false},
	}))
	if c.Path != "" {
		if !filepath.IsAbs(c.Path) {
			ve.Add(configFieldErr("backup.path", "backup.path must be an absolute directory"))
		} else if strings.Contains(c.Path, "..") {
			ve.Add(configFieldErr("backup.path", "backup.path must not contain '..'"))
		}
	}
	return ve.Err()
}

// validateLogging checks that log level and format are recognized values.
func validateLogging(l *LoggingConfig) error {
	var ve ValidationErrors
	if l.Level != "" && !ValidLogLevel(l.Level) {
		ve.Add(configFieldErr("logging.level",
			fmt.Sprintf("logging.level must be one of error/warn/info/debug, got %q", l.Level)))
	}
	if l.Format != "" && !ValidLogFormat(l.Format) {
		ve.Add(configFieldErr("logging.format",
			fmt.Sprintf("logging.format must be one of json/text, got %q", l.Format)))
	}
	if err := ve.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrLoggingConfig, err)
	}
	return nil
}

// warnArrURLs logs a warning when only one of url/public_url is set.
// The fallback covers it, but the user should know the web UI links
// or API calls may point to the wrong address.
func warnArrURLs(name string, y yamlArrConfig) {
	if y.URL == "" && y.PublicURL == "" {
		return // not configured at all
	}
	if y.URL != "" && y.PublicURL == "" {
		slog.Warn("public_url not set, falling back to url (web UI links may not work from a browser)",
			"arr", name, "url", y.URL)
	}
	if y.PublicURL != "" && y.URL == "" {
		slog.Warn("url not set, falling back to public_url (may not work from inside Docker)",
			"arr", name, "public_url", y.PublicURL)
	}
}

// validateArrs checks that at least one arr endpoint is configured and
// that configured endpoints have API keys. Returns an error on failure.
func validateArrs(cfg *Config) error {
	sonarr := cfg.SonarrConfig()
	radarr := cfg.RadarrConfig()
	if sonarr.URL == "" && radarr.URL == "" {
		return ErrNoArr
	}
	warnArrURLs("sonarr", cfg.Sonarr)
	warnArrURLs("radarr", cfg.Radarr)
	var missing []string
	if sonarr.URL != "" && cfg.Sonarr.APIKey == "" {
		missing = append(missing, "sonarr")
	}
	if radarr.URL != "" && cfg.Radarr.APIKey == "" {
		missing = append(missing, "radarr")
	}
	if len(missing) > 0 {
		return fmt.Errorf("%w: %s", ErrMissingAPIKey, strings.Join(missing, ", "))
	}
	return nil
}
