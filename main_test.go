package main

import (
	"context"
	"log/slog"
	"os"
	"testing"
)

func TestEnvOr_returns_env_value_when_set(t *testing.T) {
	t.Setenv("TEST_ENVOR_KEY", "from-env")

	got := envOr("TEST_ENVOR_KEY", "fallback")
	if got != "from-env" {
		t.Errorf("envOr(%q, %q) = %q, want %q",
			"TEST_ENVOR_KEY", "fallback", got, "from-env")
	}
}

func TestEnvOr_returns_fallback_when_unset(t *testing.T) {
	got := envOr("TEST_ENVOR_UNSET_KEY_12345", "fallback")
	if got != "fallback" {
		t.Errorf("envOr(%q, %q) = %q, want %q",
			"TEST_ENVOR_UNSET_KEY_12345", "fallback", got, "fallback")
	}
}

func TestEnvOr_returns_fallback_when_empty(t *testing.T) {
	t.Setenv("TEST_ENVOR_EMPTY", "")

	got := envOr("TEST_ENVOR_EMPTY", "fallback")
	if got != "fallback" {
		t.Errorf("envOr(%q, %q) = %q, want %q",
			"TEST_ENVOR_EMPTY", "fallback", got, "fallback")
	}
}

func TestSetupLogging_valid_level(t *testing.T) {
	setupLogging("debug", "text")

	handler := slog.Default().Handler()
	if handler.Enabled(context.Background(), slog.LevelDebug) != true {
		t.Error("setupLogging(\"debug\", \"text\"): expected debug level to be enabled")
	}
}

func TestSetupLogging_invalid_level_defaults_to_info(t *testing.T) {
	setupLogging("bogus", "text")

	handler := slog.Default().Handler()
	if handler.Enabled(context.Background(), slog.LevelDebug) {
		t.Error("setupLogging(\"bogus\", \"text\"): debug should not be enabled (expected info default)")
	}
	if !handler.Enabled(context.Background(), slog.LevelInfo) {
		t.Error("setupLogging(\"bogus\", \"text\"): info should be enabled")
	}
}

func TestSetupLogging_json_format(t *testing.T) {
	setupLogging("info", "json")

	handler := slog.Default().Handler()
	if _, ok := handler.(*slog.JSONHandler); !ok {
		t.Errorf("setupLogging(\"info\", \"json\"): handler type = %T, want *slog.JSONHandler", handler)
	}
}

func TestSetupLogging_text_format(t *testing.T) {
	setupLogging("info", "text")

	handler := slog.Default().Handler()
	if _, ok := handler.(*slog.TextHandler); !ok {
		t.Errorf("setupLogging(\"info\", \"text\"): handler type = %T, want *slog.TextHandler", handler)
	}
}

func TestHandleCLI_unknown_command_returns_false(t *testing.T) {
	_, handled := handleCLI("nonexistent-command")
	if handled {
		t.Errorf("handleCLI(%q) handled = true, want false", "nonexistent-command")
	}
}

func TestServerURL_default(t *testing.T) {
	t.Setenv("SUBFLUX_URL", "")

	got, ok := serverURL()
	if !ok {
		t.Fatalf("serverURL() ok = false, want true for default URL")
	}
	if got != "http://127.0.0.1:8374" {
		t.Errorf("serverURL() = %q, want %q", got, "http://127.0.0.1:8374")
	}
}

func TestServerURL_custom(t *testing.T) {
	t.Setenv("SUBFLUX_URL", "http://custom:9999")

	got, ok := serverURL()
	if !ok {
		t.Fatalf("serverURL() ok = false, want true for custom URL")
	}
	if got != "http://custom:9999" {
		t.Errorf("serverURL() = %q, want %q", got, "http://custom:9999")
	}
}

// --- handleCLI exit-code tests (POSIX exit code 2 for usage errors) ---

func TestHandleCLI_help_returns_zero(t *testing.T) {
	prev := os.Args
	t.Cleanup(func() { os.Args = prev })

	// Help on a known command should return code 0 + handled=true.
	os.Args = []string{"subflux", "search", "--help"}
	code, handled := handleCLI("search")
	if !handled {
		t.Fatalf("handleCLI(search --help) handled = false, want true")
	}
	if code != 0 {
		t.Errorf("handleCLI(search --help) code = %d, want 0", code)
	}
}

func TestHandleCLI_missing_required_flag_returns_two(t *testing.T) {
	prev := os.Args
	t.Cleanup(func() { os.Args = prev })

	// `unlock` requires --type/--id/--lang. Calling with none should
	// fail validation and return POSIX exit code 2 (usage error), not
	// 1 (runtime error).
	os.Args = []string{"subflux", "unlock"}
	code, handled := handleCLI("unlock")
	if !handled {
		t.Fatalf("handleCLI(unlock) handled = false, want true")
	}
	if code != 2 {
		t.Errorf("handleCLI(unlock) without required flags: code = %d, want 2 (usage)", code)
	}
}

func TestHandleCLI_unknown_flag_returns_two(t *testing.T) {
	prev := os.Args
	t.Cleanup(func() { os.Args = prev })

	// Unknown flag on a known command should fail validation with code 2.
	os.Args = []string{"subflux", "status", "--bogus", "value"}
	code, handled := handleCLI("status")
	if !handled {
		t.Fatalf("handleCLI(status --bogus) handled = false, want true")
	}
	if code != 2 {
		t.Errorf("handleCLI(status --bogus): code = %d, want 2 (usage)", code)
	}
}

// --- --format passthrough flag ---

func TestRawJSONFormat_recognized_for_short_form(t *testing.T) {
	prev := os.Args
	t.Cleanup(func() { os.Args = prev })

	os.Args = []string{"subflux", "status", "--format", "json"}
	if !rawJSONFormat() {
		t.Errorf("rawJSONFormat() = false for --format json, want true")
	}
}

func TestRawJSONFormat_default_returns_false(t *testing.T) {
	prev := os.Args
	t.Cleanup(func() { os.Args = prev })

	os.Args = []string{"subflux", "status"}
	if rawJSONFormat() {
		t.Errorf("rawJSONFormat() = true with no --format, want false")
	}
}

func TestRawJSONFormat_pretty_value_returns_false(t *testing.T) {
	prev := os.Args
	t.Cleanup(func() { os.Args = prev })

	os.Args = []string{"subflux", "status", "--format", "pretty"}
	if rawJSONFormat() {
		t.Errorf("rawJSONFormat() = true for --format pretty, want false")
	}
}
