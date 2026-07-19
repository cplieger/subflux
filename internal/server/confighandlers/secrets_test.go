package confighandlers

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// byteLines converts string rows into the [][]byte shape the YAML scanners
// (SecretContextKey, ExtractSecretValues) operate on.
func byteLines(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

func TestStripYAMLComment(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"comment_after_quoted_value", `"abc" # x`, `"abc"`},
		{"comment_immediately_after_closing_quote", `"abc" #x`, `"abc"`},
		{"comment_after_single_quoted_value", `'a' # c`, `'a'`},
		{"unquoted_value_with_comment", "value # comment", "value"},
		{"unquoted_value_without_comment", "value", "value"},
		{"empty_input", "", ""},
		{"unterminated_quote_left_intact", `"abc`, `"abc`},
		// A '#' not preceded by a space is part of the value, not a comment,
		// so it must not be truncated (secret values may contain '#').
		{"hash_without_space_not_truncated", "pass#word", "pass#word"},
		{"hash_inside_quotes_not_truncated", `"pass#word"`, `"pass#word"`},
		// Migrated from the root server package's delegate-era tests.
		{"double_quoted_no_comment", `"my-value"`, `"my-value"`},
		{"double_quoted_escaped_quote", `"my-\"value"`, `"my-\"value"`},
		{"double_quoted_escaped_quote_and_comment", `"my-\"value" # comment`, `"my-\"value"`},
		{"single_quoted_no_comment", "'my-value'", "'my-value'"},
		{"single_quoted_backslash_not_escape", "'my-\\'value'", "'my-\\'value'"},
		{"unterminated_single_quote", "'no-close", "'no-close"},
		{"double_quoted_contains_space_hash", `"abc #123"`, `"abc #123"`},
		{"double_quoted_space_hash_with_trailing_comment", `"abc #123" # real comment`, `"abc #123"`},
		{"unquoted_multiple_space_hash", "val # first # second", "val"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(StripYAMLComment([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("StripYAMLComment(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestRedactSecrets(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"real_secret_redacted", "password: hunter2", `password: "********"`},
		{"empty_single_quoted_stays", "password: ''", "password: ''"},
		{"empty_double_quoted_stays", `password: ""`, `password: ""`},
		// The whole value is replaced, so a trailing comment is dropped along
		// with the secret (it never reaches the redacted output).
		{"secret_with_comment_fully_replaced", "password: hunter2 # note", `password: "********"`},
		// A '#' inside the value (no leading space) is not a comment, so the
		// value is still treated as a real secret and redacted, not truncated.
		{"secret_containing_hash_redacted", "api_key: secret#val", `api_key: "********"`},
		{"non_secret_key_untouched", "name: value", "name: value"},
		{
			name: "nested_secret_redacted_parent_untouched",
			in:   "sonarr:\n  api_key: realkey",
			want: "sonarr:\n  api_key: \"********\"",
		},
		// Migrated from the root server package's delegate-era tests: the
		// full secret key list, case-insensitivity, and non-redaction paths.
		{"api_key_redacted", "  api_key: my-secret-key", "  api_key: \"********\""},
		{"token_redacted", "  token: abc123", "  token: \"********\""},
		{"secret_redacted", "  secret: top-secret-value", "  secret: \"********\""},
		{"passkey_redacted", "  passkey: secret123", "  passkey: \"********\""},
		{"client_key_redacted", "  client_key: ck-12345", "  client_key: \"********\""},
		{"anidb_client_key_redacted", "  anidb_client_key: anidb-key-123", "  anidb_client_key: \"********\""},
		{"case_insensitive_match", "  API_KEY: my-key", "  API_KEY: \"********\""},
		{"non_secret_url_preserved", "  url: http://example.com", "  url: http://example.com"},
		{
			name: "multiline_mixed",
			in:   "  url: http://example.com\n  token: secret\n  port: 8080",
			want: "  url: http://example.com\n  token: \"********\"\n  port: 8080",
		},
		{"empty_input", "", ""},
		{"partial_key_match_not_redacted", "  api_key_name: visible", "  api_key_name: visible"},
		{"blank_value_not_redacted", "  api_key: ", "  api_key: "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := string(RedactSecrets([]byte(tc.in)))
			if got != tc.want {
				t.Errorf("RedactSecrets(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestExtractSecretValues(t *testing.T) {
	t.Run("records_non_empty_value", func(t *testing.T) {
		got := ExtractSecretValues([]byte("password: secret123"))
		if len(got) != 1 || got["password"] != "secret123" {
			t.Errorf("ExtractSecretValues(non-empty) = %v, want map[password:secret123]", got)
		}
	})

	t.Run("skips_empty_quoted_value", func(t *testing.T) {
		got := ExtractSecretValues([]byte("password: ''"))
		if len(got) != 0 {
			t.Errorf("ExtractSecretValues(empty quoted) = %v, want empty map", got)
		}
	})

	t.Run("qualifies_key_with_parent_context", func(t *testing.T) {
		got := ExtractSecretValues([]byte("sonarr:\n  api_key: xyz"))
		if len(got) != 1 || got["sonarr.api_key"] != "xyz" {
			t.Errorf("ExtractSecretValues(nested) = %v, want map[sonarr.api_key:xyz]", got)
		}
	})

	// Migrated from the root server package's delegate-era tests.
	t.Run("empty_input", func(t *testing.T) {
		if got := ExtractSecretValues([]byte("")); len(got) != 0 {
			t.Errorf("ExtractSecretValues(empty) = %v, want empty map", got)
		}
	})

	t.Run("no_secrets", func(t *testing.T) {
		got := ExtractSecretValues([]byte("url: http://example.com\nport: 8080"))
		if len(got) != 0 {
			t.Errorf("ExtractSecretValues(no secrets) = %v, want empty map", got)
		}
	})

	t.Run("multiple_secrets", func(t *testing.T) {
		got := ExtractSecretValues([]byte("sonarr:\n  api_key: key1\nradarr:\n  api_key: key2"))
		if len(got) != 2 || got["sonarr.api_key"] != "key1" || got["radarr.api_key"] != "key2" {
			t.Errorf("ExtractSecretValues(multiple) = %v, want sonarr.api_key:key1 + radarr.api_key:key2", got)
		}
	})

	t.Run("strips_inline_comment", func(t *testing.T) {
		got := ExtractSecretValues([]byte("sonarr:\n  api_key: abc123 # my key"))
		if len(got) != 1 || got["sonarr.api_key"] != "abc123" {
			t.Errorf("ExtractSecretValues(comment) = %v, want map[sonarr.api_key:abc123]", got)
		}
	})

	t.Run("skips_bare_empty_value", func(t *testing.T) {
		if got := ExtractSecretValues([]byte("sonarr:\n  api_key: ")); len(got) != 0 {
			t.Errorf("ExtractSecretValues(bare empty) = %v, want empty map", got)
		}
	})

	t.Run("password_key_deeply_nested", func(t *testing.T) {
		got := ExtractSecretValues([]byte("providers:\n  os:\n    password: hunter2"))
		if len(got) != 1 || got["providers.os.password"] != "hunter2" {
			t.Errorf("ExtractSecretValues(password) = %v, want map[providers.os.password:hunter2]", got)
		}
	})
}

func TestSecretContextKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		want    string
		lines   [][]byte
		lineIdx int
	}{
		{
			name:    "single_parent",
			lines:   byteLines("sonarr:", "  api_key: secret"),
			lineIdx: 1,
			key:     "api_key",
			want:    "sonarr.api_key",
		},
		{
			// A sibling key at the same indent is not a parent.
			name:    "sibling_same_indent_excluded",
			lines:   byteLines("sonarr:", "  url: http://x", "  api_key: secret"),
			lineIdx: 2,
			key:     "api_key",
			want:    "sonarr.api_key",
		},
		{
			// A candidate line whose colon is at index 0 contributes no parent.
			name:    "leading_colon_not_parent",
			lines:   byteLines(":root", "  api_key: secret"),
			lineIdx: 1,
			key:     "api_key",
			want:    "api_key",
		},
		{
			name:    "nested_two_parents",
			lines:   byteLines("providers:", "  opensubtitles:", "    api_key: secret"),
			lineIdx: 2,
			key:     "api_key",
			want:    "providers.opensubtitles.api_key",
		},
		// Migrated from the root server package's delegate-era tests.
		{
			name:    "top_level_key",
			lines:   byteLines("api_key: secret123"),
			lineIdx: 0,
			key:     "api_key",
			want:    "api_key",
		},
		{
			name:    "deeply_nested_three_parents",
			lines:   byteLines("providers:", "  opensubtitles:", "    settings:", "      password: hunter2"),
			lineIdx: 3,
			key:     "password",
			want:    "providers.opensubtitles.settings.password",
		},
		{
			name:    "skips_blank_lines",
			lines:   byteLines("providers:", "", "  os:", "    api_key: abc"),
			lineIdx: 3,
			key:     "api_key",
			want:    "providers.os.api_key",
		},
		{
			name:    "skips_comment_lines",
			lines:   byteLines("providers:", "  # comment", "  os:", "    api_key: abc"),
			lineIdx: 3,
			key:     "api_key",
			want:    "providers.os.api_key",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SecretContextKey(tc.lines, tc.lineIdx, tc.key)
			if got != tc.want {
				t.Errorf("SecretContextKey(key=%q, lineIdx=%d) = %q, want %q",
					tc.key, tc.lineIdx, got, tc.want)
			}
		})
	}
}

// --- IsRedactedPlaceholder ---
//
// Migrated from the root server package's delegate-era tests.

func TestIsRedactedPlaceholder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"empty", "", false},
		{"all asterisks", "********", true},
		{"single asterisk", "*", true},
		{"REDACTED tag", "[REDACTED]", true},
		{"real value", "my-secret-key", false},
		{"asterisks with text", "***abc", false},
		{"text with asterisks", "abc***", false},
		{"redacted lowercase", "[redacted]", false},
		{"partial redacted", "REDACTED", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := IsRedactedPlaceholder([]byte(tt.input))
			if got != tt.want {
				t.Errorf("IsRedactedPlaceholder(%q) = %v, want %v",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- FindClosingQuote ---
//
// Migrated from the root server package's delegate-era tests.

func TestFindClosingQuote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		quote byte
		want  int
	}{
		{"empty after opening double quote", `"`, '"', -1},
		{"simple double quoted", `"abc"`, '"', 4},
		{"simple single quoted", `'abc'`, '\'', 4},
		{"escaped double quote inside", `"ab\"cd"`, '"', 7},
		{"backslash not escape in single quote", `'ab\'`, '\'', 4},
		{"double quote with backslash at end", `"ab\"`, '"', -1},
		{"two escaped backslashes then quote", `"a\\\\"`, '"', 6},
		{"empty quoted string", `""`, '"', 1},
		{"single char quoted", `"x"`, '"', 2},
		{"no closing quote", `"abcdef`, '"', -1},
		{"escaped backslash then quote", `"a\\"`, '"', 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := FindClosingQuote([]byte(tt.input), tt.quote)
			if got != tt.want {
				t.Errorf("FindClosingQuote(%q, %q) = %d, want %d",
					tt.input, string(tt.quote), got, tt.want)
			}
		})
	}
}

// --- MergeSecrets ---
//
// Migrated from the root server package's delegate-era tests.

func TestMergeSecrets(t *testing.T) {
	t.Run("merges empty secret from existing config", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-secret-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := MergeSecrets([]byte("sonarr:\n  api_key: \"\"\n"), existingPath)
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !strings.Contains(string(got), "real-secret-key") {
			t.Errorf("MergeSecrets() = %q, want to contain %q", string(got), "real-secret-key")
		}
	})

	t.Run("preserves non-empty secret in new config", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: old-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := MergeSecrets([]byte("sonarr:\n  api_key: new-key\n"), existingPath)
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !strings.Contains(string(got), "new-key") {
			t.Errorf("MergeSecrets() = %q, want to contain %q", string(got), "new-key")
		}
		if strings.Contains(string(got), "old-key") {
			t.Errorf("MergeSecrets() = %q, should not contain old key", string(got))
		}
	})

	t.Run("returns newData when no existing file", func(t *testing.T) {
		newData := []byte("sonarr:\n  api_key: \"\"\n")
		got, err := MergeSecrets(newData, "/nonexistent/config.yaml")
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !bytes.Equal(got, newData) {
			t.Errorf("MergeSecrets(no existing) = %q, want %q", string(got), string(newData))
		}
	})

	t.Run("fails closed on unreadable baseline with keep-semantics secret", func(t *testing.T) {
		// A directory as the config path forces a read error that is not
		// fs.ErrNotExist (open+stat succeed, read fails EISDIR). The payload
		// carries an empty secret (keep semantics), so silently proceeding
		// would persist the empty value literally — the merge must refuse.
		if _, err := MergeSecrets([]byte("sonarr:\n  api_key: \"\"\n"), t.TempDir()); !errors.Is(err, errBaselineUnavailable) {
			t.Errorf("MergeSecrets(unreadable baseline, keep secret) error = %v, want errBaselineUnavailable", err)
		}
	})

	t.Run("fails closed on redacted placeholder with unreadable baseline", func(t *testing.T) {
		if _, err := MergeSecrets([]byte("sonarr:\n  api_key: \"********\"\n"), t.TempDir()); !errors.Is(err, errBaselineUnavailable) {
			t.Errorf("MergeSecrets(unreadable baseline, placeholder) error = %v, want errBaselineUnavailable", err)
		}
	})

	t.Run("proceeds on unreadable baseline when all secrets are explicit", func(t *testing.T) {
		// No keep-semantics secret: the baseline is never needed, so a
		// complete payload can overwrite (and repair) an unreadable file.
		newData := []byte("sonarr:\n  api_key: explicit-key\n")
		got, err := MergeSecrets(newData, t.TempDir())
		if err != nil {
			t.Fatalf("MergeSecrets(explicit secrets) error = %v", err)
		}
		if !bytes.Equal(got, newData) {
			t.Errorf("MergeSecrets(explicit secrets) = %q, want %q", string(got), string(newData))
		}
	})

	t.Run("returns newData when existing has no secrets", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  url: http://sonarr:8989\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		newData := []byte("sonarr:\n  api_key: \"\"\n")
		got, err := MergeSecrets(newData, existingPath)
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !bytes.Equal(got, newData) {
			t.Errorf("MergeSecrets(no secrets in existing) = %q, want %q", string(got), string(newData))
		}
	})

	t.Run("merges multiple secrets from different sections", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: sonarr-key\nradarr:\n  api_key: radarr-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := MergeSecrets([]byte("sonarr:\n  api_key: \"\"\nradarr:\n  api_key: \"\"\n"), existingPath)
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !strings.Contains(string(got), "sonarr-key") {
			t.Errorf("MergeSecrets() missing sonarr key in %q", string(got))
		}
		if !strings.Contains(string(got), "radarr-key") {
			t.Errorf("MergeSecrets() missing radarr key in %q", string(got))
		}
	})

	t.Run("bare key colon without value is not matched", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		newData := []byte("sonarr:\n  api_key: \n")
		got, err := MergeSecrets(newData, existingPath)
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !bytes.Equal(got, newData) {
			t.Errorf("MergeSecrets(bare key) = %q, want unchanged %q", string(got), string(newData))
		}
	})

	t.Run("merges single-quoted empty value", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := MergeSecrets([]byte("sonarr:\n  api_key: ''\n"), existingPath)
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !strings.Contains(string(got), "real-key") {
			t.Errorf("MergeSecrets(single-quoted empty) = %q, want to contain %q", string(got), "real-key")
		}
	})

	t.Run("merges password key", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("providers:\n  os:\n    password: hunter2\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := MergeSecrets([]byte("providers:\n  os:\n    password: \"\"\n"), existingPath)
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !strings.Contains(string(got), "hunter2") {
			t.Errorf("MergeSecrets(password) = %q, want to contain %q", string(got), "hunter2")
		}
	})

	t.Run("preserves indentation", func(t *testing.T) {
		dir := t.TempDir()
		existingPath := filepath.Join(dir, "config.yaml")
		if err := os.WriteFile(existingPath, []byte("providers:\n  os:\n    settings:\n      api_key: deep-key\n"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		got, err := MergeSecrets([]byte("providers:\n  os:\n    settings:\n      api_key: \"\"\n"), existingPath)
		if err != nil {
			t.Fatalf("MergeSecrets() error = %v", err)
		}
		if !strings.Contains(string(got), "      api_key: deep-key") {
			t.Errorf("MergeSecrets() indentation wrong in %q", string(got))
		}
	})
}

func TestMergeSecrets_restores_redacted_placeholder(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-secret-key\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := MergeSecrets([]byte("sonarr:\n  api_key: \"********\"\n"), existingPath)
	if err != nil {
		t.Fatalf("MergeSecrets() error = %v", err)
	}
	if !strings.Contains(string(got), "real-secret-key") {
		t.Errorf("MergeSecrets(redacted placeholder) = %q, want to contain %q",
			string(got), "real-secret-key")
	}
	if strings.Contains(string(got), "********") {
		t.Errorf("MergeSecrets(redacted placeholder) = %q, should not contain asterisks",
			string(got))
	}
}

func TestMergeSecrets_restores_REDACTED_tag(t *testing.T) {
	dir := t.TempDir()
	existingPath := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(existingPath, []byte("sonarr:\n  api_key: real-secret-key\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := MergeSecrets([]byte("sonarr:\n  api_key: \"[REDACTED]\"\n"), existingPath)
	if err != nil {
		t.Fatalf("MergeSecrets() error = %v", err)
	}
	if !strings.Contains(string(got), "real-secret-key") {
		t.Errorf("MergeSecrets([REDACTED]) = %q, want to contain %q",
			string(got), "real-secret-key")
	}
}

// --- atomicWriteConfig ---
//
// Migrated from the root server package's delegate-era tests, retargeted at
// this package's atomicWriteConfig (the one HandleResetConfig uses).

func TestAtomicWriteConfig_writes_content(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	data := []byte("key: value\n")

	if err := atomicWriteConfig(context.Background(), path, data); err != nil {
		t.Fatalf("atomicWriteConfig(%q) error = %v", path, err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if string(got) != string(data) {
		t.Errorf("atomicWriteConfig content = %q, want %q", got, data)
	}
}

func TestAtomicWriteConfig_sets_permissions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := atomicWriteConfig(context.Background(), path, []byte("test")); err != nil {
		t.Fatalf("atomicWriteConfig error = %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%q) error = %v", path, err)
	}
	perm := fi.Mode().Perm()
	if perm != 0o600 {
		t.Errorf("atomicWriteConfig permissions = %o, want 600", perm)
	}
}

func TestAtomicWriteConfig_overwrites_existing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	newData := []byte("new content")
	if err := atomicWriteConfig(context.Background(), path, newData); err != nil {
		t.Fatalf("atomicWriteConfig error = %v", err)
	}

	got, _ := os.ReadFile(path)
	if string(got) != string(newData) {
		t.Errorf("atomicWriteConfig overwrite content = %q, want %q", got, newData)
	}
}

func TestAtomicWriteConfig_nonexistent_dir_returns_error(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "nonexistent", "config.yaml")

	err := atomicWriteConfig(context.Background(), path, []byte("test"))
	if err == nil {
		t.Error("atomicWriteConfig(nonexistent dir) error = nil, want error")
	}
}

func TestAtomicWriteConfig_empty_data(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := atomicWriteConfig(context.Background(), path, []byte{}); err != nil {
		t.Fatalf("atomicWriteConfig(empty) error = %v", err)
	}

	got, _ := os.ReadFile(path)
	if len(got) != 0 {
		t.Errorf("atomicWriteConfig(empty) content len = %d, want 0", len(got))
	}
}
