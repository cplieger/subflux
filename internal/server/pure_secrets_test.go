package server

import (
	"strings"
	"testing"
)

// --- redactSecrets ---

func TestRedactSecrets(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			"api_key redacted",
			"  api_key: my-secret-key",
			"  api_key: \"********\"",
		},
		{
			"password redacted",
			"  password: hunter2",
			"  password: \"********\"",
		},
		{
			"token redacted",
			"  token: abc123",
			"  token: \"********\"",
		},
		{
			"secret redacted",
			"  secret: top-secret-value",
			"  secret: \"********\"",
		},
		{
			"passkey redacted",
			"  passkey: secret123",
			"  passkey: \"********\"",
		},
		{
			"non-secret preserved",
			"  url: http://example.com",
			"  url: http://example.com",
		},
		{
			"case insensitive match",
			"  API_KEY: my-key",
			"  API_KEY: \"********\"",
		},
		{
			"multiline mixed",
			"  url: http://example.com\n  token: secret\n  port: 8080",
			"  url: http://example.com\n  token: \"********\"\n  port: 8080",
		},
		{"empty input", "", ""},
		{
			"partial key match not redacted",
			"  api_key_name: visible",
			"  api_key_name: visible",
		},
		{
			"empty double-quoted value not redacted",
			"  api_key: \"\"",
			"  api_key: \"\"",
		},
		{
			"empty single-quoted value not redacted",
			"  api_key: ''",
			"  api_key: ''",
		},
		{
			"blank value not redacted",
			"  api_key: ",
			"  api_key: ",
		},
		{
			"inline comment stripped before redaction",
			"  token: my-secret # this is a comment",
			"  token: \"********\"",
		},
		{
			"client_key redacted",
			"  client_key: ck-12345",
			"  client_key: \"********\"",
		},
		{
			"anidb_client_key redacted",
			"  anidb_client_key: anidb-key-123",
			"  anidb_client_key: \"********\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := string(redactSecrets([]byte(tt.input)))
			if got != tt.want {
				t.Errorf("redactSecrets(%q) = %q, want %q",
					tt.input, got, tt.want)
			}
		})
	}
}

// --- secretContextKey ---

func TestSecretContextKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		yaml    string
		key     string
		want    string
		lineIdx int
	}{
		{name: "top level key", yaml: "api_key: secret123", lineIdx: 0, key: "api_key", want: "api_key"},
		{name: "nested under one parent", yaml: "sonarr:\n  api_key: secret123", lineIdx: 1, key: "api_key", want: "sonarr.api_key"},
		{
			name: "deeply nested", yaml: "providers:\n  opensubtitles:\n    settings:\n      password: hunter2", lineIdx: 3, key: "password",
			want: "providers.opensubtitles.settings.password",
		},
		{
			name: "skips blank lines", yaml: "providers:\n\n  os:\n    api_key: abc", lineIdx: 3, key: "api_key",
			want: "providers.os.api_key",
		},
		{
			name: "skips comment lines", yaml: "providers:\n  # comment\n  os:\n    api_key: abc", lineIdx: 3, key: "api_key",
			want: "providers.os.api_key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lines := splitYAMLLines(tt.yaml)
			got := secretContextKey(lines, tt.lineIdx, tt.key)
			if got != tt.want {
				t.Errorf("secretContextKey(..., %d, %q) = %q, want %q",
					tt.lineIdx, tt.key, got, tt.want)
			}
		})
	}
}

// splitYAMLLines is a test helper that splits a string into [][]byte lines.
func splitYAMLLines(s string) [][]byte {
	parts := strings.Split(s, "\n")
	lines := make([][]byte, len(parts))
	for i, p := range parts {
		lines[i] = []byte(p)
	}
	return lines
}

// --- extractSecretValues ---

func TestExtractSecretValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		want map[string]string
		name string
		yaml string
	}{
		{name: "empty input", yaml: "", want: map[string]string{}},
		{name: "no secrets", yaml: "url: http://example.com\nport: 8080", want: map[string]string{}},
		{name: "simple api_key", yaml: "sonarr:\n  api_key: abc123", want: map[string]string{
			"sonarr.api_key": "abc123",
		}},
		{name: "multiple secrets", yaml: "sonarr:\n  api_key: key1\nradarr:\n  api_key: key2", want: map[string]string{
			"sonarr.api_key": "key1",
			"radarr.api_key": "key2",
		}},
		{name: "strips inline comment", yaml: "sonarr:\n  api_key: abc123 # my key", want: map[string]string{
			"sonarr.api_key": "abc123",
		}},
		{name: "skips empty values", yaml: "sonarr:\n  api_key: ", want: map[string]string{}},
		{name: "skips quoted empty", yaml: "sonarr:\n  api_key: \"\"", want: map[string]string{}},
		{name: "password key", yaml: "providers:\n  os:\n    password: hunter2", want: map[string]string{
			"providers.os.password": "hunter2",
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := extractSecretValues([]byte(tt.yaml))
			if len(got) != len(tt.want) {
				t.Fatalf("extractSecretValues() returned %d entries, want %d\ngot: %v",
					len(got), len(tt.want), got)
			}
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok {
					t.Errorf("extractSecretValues() missing key %q", k)
				} else if gotV != wantV {
					t.Errorf("extractSecretValues()[%q] = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}
