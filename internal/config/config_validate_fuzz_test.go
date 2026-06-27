package config

import (
	"errors"
	"testing"
	"time"
)

// FuzzValidateBackup exercises validateBackup with arbitrary inputs, ensuring
// it never panics, that disabled configs always pass, and that an enabled
// config with retention < 1 always fails.
func FuzzValidateBackup(f *testing.F) {
	f.Add(true, "/tmp/backups", int64(24*time.Hour), 7)
	f.Add(false, "", int64(0), 0)
	f.Add(true, "", int64(time.Minute), 1)
	f.Add(true, "relative/path", int64(time.Hour), 3)
	f.Add(true, "/foo/../bar", int64(time.Hour), 5)
	f.Add(true, "/valid/path", int64(30*time.Minute), 0)
	f.Add(true, "/ok", int64(time.Hour), -1)

	f.Fuzz(func(t *testing.T, enabled bool, path string, freqNs int64, retention int) {
		cfg := &yamlBackupConfig{
			Enabled:   enabled,
			Path:      path,
			Frequency: Duration{D: time.Duration(freqNs)},
			Retention: retention,
		}

		err := validateBackup(cfg)

		// Invariant 1: disabled backup always passes.
		if !enabled && err != nil {
			t.Fatalf("disabled backup should never error, got: %v", err)
		}

		// Invariant 2: if enabled with retention < 1, must error.
		if enabled && retention < 1 && err == nil {
			t.Fatal("enabled backup with retention < 1 should error")
		}
	})
}

// FuzzValidateLogging exercises validateLogging with arbitrary level/format
// strings, checking that valid combinations pass and that every failure wraps
// ErrLoggingConfig.
func FuzzValidateLogging(f *testing.F) {
	f.Add("info", "json")
	f.Add("debug", "text")
	f.Add("", "")
	f.Add("invalid", "invalid")
	f.Add("error", "yaml")
	f.Add("WARN", "JSON")

	f.Fuzz(func(t *testing.T, level, format string) {
		cfg := &LoggingConfig{Level: LogLevel(level), Format: LogFormat(format)}
		err := validateLogging(cfg)

		// Invariant 1: empty level and format always passes.
		if level == "" && format == "" && err != nil {
			t.Fatalf("empty logging config should pass, got: %v", err)
		}

		// Invariant 2: valid level+format never errors.
		if ValidLogLevel(LogLevel(level)) && ValidLogFormat(LogFormat(format)) && err != nil {
			t.Fatalf("valid logging config (%q, %q) should pass, got: %v", level, format, err)
		}

		// Invariant 3: if error, it wraps ErrLoggingConfig.
		if err != nil && !errors.Is(err, ErrLoggingConfig) {
			t.Fatalf("logging validation error should wrap ErrLoggingConfig, got: %v", err)
		}
	})
}

// FuzzValidateScoreRange exercises validateScoreRange boundaries: values in
// [0,100] pass, everything else fails with a *ValidationError carrying the
// queried field.
func FuzzValidateScoreRange(f *testing.F) {
	f.Add(0, "search.min_score")
	f.Add(100, "search.min_score")
	f.Add(-1, "field")
	f.Add(101, "field")
	f.Add(50, "test.score")

	f.Fuzz(func(t *testing.T, value int, field string) {
		err := validateScoreRange(value, field)

		// Invariant: values in [0,100] pass; outside fail.
		inRange := value >= 0 && value <= 100
		if inRange && err != nil {
			t.Fatalf("validateScoreRange(%d, %q) = %v, want nil", value, field, err)
		}
		if !inRange && err == nil {
			t.Fatalf("validateScoreRange(%d, %q) = nil, want error", value, field)
		}

		// If error, it should be a *ValidationError naming the field.
		if err != nil {
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Fatalf("error is not *ValidationError: %v", err)
			}
			if ve.Field != field {
				t.Fatalf("ValidationError.Field = %q, want %q", ve.Field, field)
			}
		}
	})
}
