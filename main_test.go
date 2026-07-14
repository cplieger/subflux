package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

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

// TestHandleCLI_contract pins handleCLI's recognized-command contract:
// a registered subcommand prints help (code 0) on --help/-h, and
// rejects unknown flags, missing required flags, and unparseable typed
// values with POSIX usage code 2; an unrecognized command is reported
// as not handled. Every case short-circuits in the help/validation
// preamble (validateCLIArgs) before any HTTP dispatch, so these run
// with no server. Successful dispatch of a validated command is an HTTP
// path covered by the functional suite, not here.
func TestHandleCLI_contract(t *testing.T) {
	tests := []struct {
		name        string
		argv        []string // full os.Args: program, subcommand, flags...
		cmd         string
		wantCode    int
		wantHandled bool
	}{
		{
			name:        "long help flag prints help and returns zero",
			argv:        []string{"subflux", cmdSearch, "--help"},
			cmd:         cmdSearch,
			wantCode:    0,
			wantHandled: true,
		},
		{
			name:        "short help flag prints help and returns zero",
			argv:        []string{"subflux", cmdSearch, "-h"},
			cmd:         cmdSearch,
			wantCode:    0,
			wantHandled: true,
		},
		{
			name:        "missing required flag is a usage error",
			argv:        []string{"subflux", cmdUnlock},
			cmd:         cmdUnlock,
			wantCode:    2,
			wantHandled: true,
		},
		{
			name:        "unknown flag is a usage error",
			argv:        []string{"subflux", cmdStatus, "--bogus", "value"},
			cmd:         cmdStatus,
			wantCode:    2,
			wantHandled: true,
		},
		{
			name:        "unparseable typed flag is a usage error",
			argv:        []string{"subflux", cmdSearch, "--pick", "notanint"},
			cmd:         cmdSearch,
			wantCode:    2,
			wantHandled: true,
		},
		{
			name:        "unrecognized command is not handled",
			argv:        []string{"subflux", "nonexistent-command"},
			cmd:         "nonexistent-command",
			wantCode:    0,
			wantHandled: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := os.Args
			t.Cleanup(func() { os.Args = prev })
			os.Args = tt.argv

			code, handled := handleCLI(tt.cmd)
			if handled != tt.wantHandled {
				t.Errorf("handleCLI(%q) handled = %v, want %v", tt.cmd, handled, tt.wantHandled)
			}
			if code != tt.wantCode {
				t.Errorf("handleCLI(%q) code = %d, want %d", tt.cmd, code, tt.wantCode)
			}
		})
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

// --- enablePasswordLoginYAML (CLI lockout recovery) ---

// authConfigView is the subset of config touched by
// enablePasswordLoginYAML, used so assertions check resulting config
// semantics after a round-trip rather than exact YAML byte formatting.
type authConfigView struct {
	Server struct {
		Port int `yaml:"port"`
	} `yaml:"server"`
	Auth struct {
		BasicEnabled bool `yaml:"basic_enabled"`
		OIDCEnabled  bool `yaml:"oidc_enabled"`
	} `yaml:"auth"`
}

func parseConfigView(t *testing.T, data []byte) authConfigView {
	t.Helper()
	var v authConfigView
	if err := yaml.Unmarshal(data, &v); err != nil {
		t.Fatalf("re-parse output YAML: %v\noutput:\n%s", err, data)
	}
	return v
}

func TestEnablePasswordLoginYAML_sets_basic_enabled_true(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{name: "auth section absent", input: "server:\n  port: 8374\n"},
		{name: "auth present without basic_enabled", input: "auth:\n  oidc_enabled: true\n"},
		{name: "basic_enabled already false", input: "auth:\n  basic_enabled: false\n"},
		{name: "basic_enabled already true", input: "auth:\n  basic_enabled: true\n"},
		{name: "auth is null", input: "auth:\nserver:\n  port: 8374\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := enablePasswordLoginYAML([]byte(tt.input))
			if err != nil {
				t.Fatalf("enablePasswordLoginYAML(%q) error = %v, want nil", tt.input, err)
			}
			if got := parseConfigView(t, out); !got.Auth.BasicEnabled {
				t.Errorf("enablePasswordLoginYAML(%q): auth.basic_enabled = false, want true\noutput:\n%s",
					tt.input, out)
			}
		})
	}
}

func TestEnablePasswordLoginYAML_preserves_other_keys(t *testing.T) {
	// Enabling password login must not clobber sibling keys (oidc_enabled)
	// or sibling sections (server.port): the rewrite mutates only the one
	// auth.basic_enabled field.
	input := "server:\n  port: 8374\nauth:\n  oidc_enabled: true\n"

	out, err := enablePasswordLoginYAML([]byte(input))
	if err != nil {
		t.Fatalf("enablePasswordLoginYAML error = %v, want nil", err)
	}

	got := parseConfigView(t, out)
	if !got.Auth.BasicEnabled {
		t.Errorf("auth.basic_enabled = false, want true")
	}
	if !got.Auth.OIDCEnabled {
		t.Errorf("auth.oidc_enabled = false, want true (sibling key must survive)")
	}
	if got.Server.Port != 8374 {
		t.Errorf("server.port = %d, want 8374 (sibling section must survive)", got.Server.Port)
	}
}

func TestEnablePasswordLoginYAML_errors(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantErrSub string
	}{
		{name: "empty config", input: "", wantErrSub: "config is empty"},
		{name: "comment-only config", input: "# just a comment\n", wantErrSub: "config is empty"},
		{name: "malformed yaml", input: "auth:\n\tbasic_enabled: true\n", wantErrSub: "parse config"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := enablePasswordLoginYAML([]byte(tt.input))
			if err == nil {
				t.Fatalf("enablePasswordLoginYAML(%q) error = nil, want error containing %q\noutput:\n%s",
					tt.input, tt.wantErrSub, out)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Errorf("enablePasswordLoginYAML(%q) error = %q, want substring %q",
					tt.input, err.Error(), tt.wantErrSub)
			}
		})
	}
}
