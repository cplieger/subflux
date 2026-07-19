package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/cliparse"
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

// statusParams parses args against the status spec (which carries the
// shared formatFlag) for rawJSONFormat tests.
func statusParams(t *testing.T, args ...string) cliparse.Params {
	t.Helper()
	spec := cliSpecs[cmdStatus]
	p, err := cliparse.ParseAndValidate(args, &spec)
	if err != nil {
		t.Fatalf("ParseAndValidate(%v) error = %v, want nil", args, err)
	}
	return p
}

func TestRawJSONFormat_recognized(t *testing.T) {
	if !rawJSONFormat(statusParams(t, "--format", "json")) {
		t.Errorf("rawJSONFormat(--format json) = false, want true")
	}
	if !rawJSONFormat(statusParams(t, "--format=JSON")) {
		t.Errorf("rawJSONFormat(--format=JSON) = false, want true (case-insensitive)")
	}
}

func TestRawJSONFormat_default_returns_false(t *testing.T) {
	if rawJSONFormat(statusParams(t)) {
		t.Errorf("rawJSONFormat() = true with no --format, want false (default pretty)")
	}
}

func TestRawJSONFormat_pretty_value_returns_false(t *testing.T) {
	if rawJSONFormat(statusParams(t, "--format", "pretty")) {
		t.Errorf("rawJSONFormat(--format pretty) = true, want false")
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

// --- P15 dispatch parity ---

// Every declared subcommand must carry a Run function: dispatch has no
// fallback table anymore (cliDispatch is gone), so a spec without Run
// would nil-panic at invocation.
func TestCLISpecs_every_entry_has_run(t *testing.T) {
	for name, spec := range cliSpecs {
		if spec.Run == nil {
			t.Errorf("cliSpecs[%q].Run is nil; every subcommand must be dispatchable", name)
		}
		if spec.Name != name {
			t.Errorf("cliSpecs[%q].Name = %q; map key and Name must agree", name, spec.Name)
		}
	}
}

// The sync-worker spec is hidden: dispatchable but absent from root help.
func TestOrderedSpecs_excludes_hidden(t *testing.T) {
	for _, s := range orderedSpecs() {
		if s.Hidden {
			t.Errorf("orderedSpecs() includes hidden spec %q", s.Name)
		}
		if s.Name == cmdSyncWorker {
			t.Errorf("orderedSpecs() includes %q; the P13 vehicle must stay out of help", cmdSyncWorker)
		}
	}
	if _, ok := cliSpecs[cmdSyncWorker]; !ok {
		t.Error("cliSpecs is missing the hidden sync-worker entry (the P13 vehicle)")
	}
}

// Parity: one invocation parses exactly once, and the runner receives the
// same Params a direct ParseAndValidate of the argv produces — no runner
// ever reparses os.Args (cliparse.ParseArgs no longer exists to reparse
// with).
func TestDispatchCLI_single_parse_parity(t *testing.T) {
	spyCalls := 0
	var got cliparse.Params
	spec := cliparse.Spec{
		Name: "spy",
		Flags: []cliparse.Flag{
			{Name: "lang", Default: "fr"},
			{Name: "pick", Type: "int", Default: "1"},
			{Name: "download", Type: "bool"},
		},
		Run: func(p cliparse.Params) int {
			spyCalls++
			got = p
			return 42
		},
	}
	cliSpecs["spy"] = spec
	t.Cleanup(func() { delete(cliSpecs, "spy") })

	args := []string{"--pick", "3", "--download"}
	code, handled := dispatchCLI("spy", args)
	if !handled || code != 42 {
		t.Fatalf("dispatchCLI(spy) = (%d, %v), want (42, true)", code, handled)
	}
	if spyCalls != 1 {
		t.Fatalf("runner invoked %d times, want exactly 1", spyCalls)
	}

	want, err := cliparse.ParseAndValidate(args, &spec)
	if err != nil {
		t.Fatalf("reference parse failed: %v", err)
	}
	if got.String("lang") != want.String("lang") ||
		got.Int("pick") != want.Int("pick") ||
		got.Bool("download") != want.Bool("download") {
		t.Errorf("runner params = (lang=%q pick=%d download=%t), want (lang=%q pick=%d download=%t)",
			got.String("lang"), got.Int("pick"), got.Bool("download"),
			want.String("lang"), want.Int("pick"), want.Bool("download"))
	}
}

// Positional tokens are rejected as usage errors by the single-pass parser.
func TestDispatchCLI_rejects_positional(t *testing.T) {
	code, handled := dispatchCLI(cmdStatus, []string{"stray"})
	if !handled || code != 2 {
		t.Errorf("dispatchCLI(status stray) = (%d, %v), want (2, true)", code, handled)
	}
}

// --- S19: real bootstrap logging ---

// captureStderrJSON swaps os.Stderr for a pipe, installs the bootstrap slog
// default (setupLogging writes to the CURRENT os.Stderr), runs fn, and
// returns the captured lines. Serial by nature: process-global stderr and
// slog default (no t.Parallel).
func captureStderrJSON(t *testing.T, fn func()) []string {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stderr = w
	t.Cleanup(func() {
		os.Stderr = old
		setupLogging("info", "json")
	})

	// Install the bootstrap default AFTER the swap so the handler binds to
	// the pipe — exactly what runServer's top-of-function setup does.
	setupLogging("info", "json")
	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe writer: %v", err)
	}
	os.Stderr = old
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	var lines []string
	for l := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestBootstrapLogging_first_line_and_early_failure_are_json pins S19: the
// very first line ("subflux starting") and a pre-config early-failure line
// (config-dir creation failure) are BOTH valid JSON slog records in the
// default format — not hand-rolled Fprintf fakes.
func TestBootstrapLogging_first_line_and_early_failure_are_json(t *testing.T) {
	// An unwritable parent directory makes MkdirAll fail with EACCES while
	// os.Stat still reports the config path as not-existing: the genuine
	// early-failure path. (Would not fail as root; tests run unprivileged.)
	tmp := t.TempDir()
	roDir := filepath.Join(tmp, "ro")
	if err := os.Mkdir(roDir, 0o500); err != nil {
		t.Fatalf("mkdir ro: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(roDir, 0o700) })
	badPath := filepath.Join(roDir, "sub", "config.yaml")
	if os.Geteuid() == 0 {
		t.Skip("running as root: directory permissions cannot force the failure path")
	}

	var ensureErr error
	lines := captureStderrJSON(t, func() {
		slog.Info("subflux starting")
		ensureErr = ensureConfigFile(badPath, []byte("default: true\n"))
	})

	if ensureErr == nil {
		t.Fatal("ensureConfigFile(unwritable path) error = nil, want error")
	}
	if len(lines) < 2 {
		t.Fatalf("captured %d log lines, want at least 2 (start + failure): %v", len(lines), lines)
	}

	type record struct {
		Time  string `json:"time"`
		Level string `json:"level"`
		Msg   string `json:"msg"`
	}
	var first, failure record
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first line is not valid JSON slog: %v\nline: %s", err, lines[0])
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &failure); err != nil {
		t.Fatalf("failure line is not valid JSON slog: %v\nline: %s", err, lines[len(lines)-1])
	}

	if first.Msg != "subflux starting" || first.Level != "INFO" || first.Time == "" {
		t.Errorf("first record = %+v, want msg=%q level=INFO with a timestamp", first, "subflux starting")
	}
	if failure.Msg != "failed to create config dir" || failure.Level != "ERROR" {
		t.Errorf("failure record = %+v, want msg=%q level=ERROR", failure, "failed to create config dir")
	}
}

// TestEnsureConfigFile_writes_default_and_logs_json covers the
// created-config notice path: the default lands on disk 0600 and the notice
// is a JSON slog line.
func TestEnsureConfigFile_writes_default_and_logs_json(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfgdir", "config.yaml")
	def := []byte("sonarr:\n  enabled: true\n")

	lines := captureStderrJSON(t, func() {
		if err := ensureConfigFile(path, def); err != nil {
			t.Errorf("ensureConfigFile() error = %v, want nil", err)
		}
	})

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("default config not written: %v", err)
	}
	if string(got) != string(def) {
		t.Errorf("written config = %q, want %q", got, def)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("config mode = %v, want 0600", info.Mode().Perm())
	}

	if len(lines) != 1 {
		t.Fatalf("captured %d lines, want exactly the created-config notice: %v", len(lines), lines)
	}
	var rec struct {
		Msg  string `json:"msg"`
		Path string `json:"path"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("notice line is not valid JSON slog: %v\nline: %s", err, lines[0])
	}
	if !strings.Contains(rec.Msg, "created default config") || rec.Path != path {
		t.Errorf("notice record = %+v, want created-default-config msg carrying path=%q", rec, path)
	}
}

// TestEnsureConfigFile_existing_file_untouched: a present config file is
// never rewritten (and nothing is logged).
func TestEnsureConfigFile_existing_file_untouched(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte("mine: true\n"), 0o600); err != nil {
		t.Fatalf("seed config: %v", err)
	}

	lines := captureStderrJSON(t, func() {
		if err := ensureConfigFile(path, []byte("default: true\n")); err != nil {
			t.Errorf("ensureConfigFile() error = %v, want nil", err)
		}
	})

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(got) != "mine: true\n" {
		t.Errorf("existing config was rewritten to %q", got)
	}
	if len(lines) != 0 {
		t.Errorf("existing-file path logged %v, want silence", lines)
	}
}
