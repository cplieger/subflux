package confighandlers

import "testing"

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
