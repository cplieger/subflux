package confighandlers

import "testing"

// gk_subflux_u25_lines converts string rows into the [][]byte shape that
// SecretContextKey / the YAML scanners operate on.
func gk_subflux_u25_lines(ss ...string) [][]byte {
	out := make([][]byte, len(ss))
	for i, s := range ss {
		out[i] = []byte(s)
	}
	return out
}

// secrets.go:71:58 CONDITIONALS_NEGATION — the `string(val) == "''"` check in
// RedactSecrets. An empty single-quoted secret value must be left UNREDACTED;
// flipping == to != would make it get redacted. The double-quoted-empty and
// real-secret rows guard the surrounding branch behavior.
func Test_gk_subflux_u25_RedactSecretsEmptyQuotedValueNotRedacted(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty_single_quoted_stays", "password: ''", "password: ''"},
		{"empty_double_quoted_stays", `password: ""`, `password: ""`},
		{"real_secret_redacted", "password: hunter2", `password: "********"`},
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

// secrets.go:131:16 CONDITIONALS_NEGATION — the `stripped == ""` check in
// ExtractSecretValues. A non-empty value must be recorded; an empty quoted
// value must be skipped. Flipping == to != inverts both directions.
func Test_gk_subflux_u25_ExtractSecretValuesEmptyCheck(t *testing.T) {
	got := ExtractSecretValues([]byte("password: secret123"))
	if len(got) != 1 || got["password"] != "secret123" {
		t.Errorf("ExtractSecretValues(non-empty) = %v, want map[password:secret123]", got)
	}

	empty := ExtractSecretValues([]byte("password: ''"))
	if len(empty) != 0 {
		t.Errorf("ExtractSecretValues(empty quoted) = %v, want empty map", empty)
	}
}

// SecretContextKey mutants on secrets.go lines 162,164,167,170,171,173.
// Each table case pins the exact context path so a mutation at the named
// operator changes the observable output (or panics out of range).
func Test_gk_subflux_u25_SecretContextKey(t *testing.T) {
	cases := []struct {
		name    string
		lines   [][]byte
		lineIdx int
		key     string
		want    string
	}{
		// Kills:
		//  164:19 (lineIdx-1: '-'->'+' starts at lineIdx+1 -> lines[2] panic),
		//  164:26 (i>=0 -> i>0: loop body never runs -> no parent),
		//  167:19 (len(trimmed)==0 -> !=0: every non-empty line wrongly skipped),
		//  167:38 (trimmed[0]=='#' -> !='#': every non-comment line wrongly skipped),
		//  170:23 (liIndent len-len: '-'->'+' makes liIndent 14, not < 2 -> no parent),
		//  171:15 (liIndent<indent -> >=: 0>=2 false -> no parent),
		//  173:16 (colonIdx>0 -> <=0: 6<=0 false -> parent name not appended).
		// All of those mutations collapse the result to "api_key".
		{
			name:    "single_parent",
			lines:   gk_subflux_u25_lines("sonarr:", "  api_key: secret"),
			lineIdx: 1,
			key:     "api_key",
			want:    "sonarr.api_key",
		},
		// Kills:
		//  162:32 (indent len-len: '-'->'+' makes indent 32, so the same-indent
		//          "url" sibling is wrongly treated as a parent),
		//  171:15 (liIndent<indent -> <=: the same-indent "url" sibling wrongly
		//          becomes a parent).
		// Both mutations yield "sonarr.url.api_key".
		{
			name:    "sibling_same_indent_excluded",
			lines:   gk_subflux_u25_lines("sonarr:", "  url: http://x", "  api_key: secret"),
			lineIdx: 2,
			key:     "api_key",
			want:    "sonarr.api_key",
		},
		// Kills 173:16 (colonIdx>0 -> >=0): a candidate line whose colon is at
		// index 0 must NOT contribute a (empty) parent. The boundary mutation
		// would append "" and yield ".api_key".
		{
			name:    "leading_colon_not_parent",
			lines:   gk_subflux_u25_lines(":root", "  api_key: secret"),
			lineIdx: 1,
			key:     "api_key",
			want:    "api_key",
		},
		// Robustness: a real two-level nesting must produce the full path.
		{
			name:    "nested_two_parents",
			lines:   gk_subflux_u25_lines("providers:", "  opensubtitles:", "    api_key: secret"),
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
