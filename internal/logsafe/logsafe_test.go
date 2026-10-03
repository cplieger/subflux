package logsafe

import (
	"strings"
	"testing"

	"github.com/cplieger/httpx/v5"
)

func TestRedactedField_leaves_no_fragment_of_the_secret(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		secret string
		in     string
	}{
		{name: "verbatim", secret: "placeholder-key", in: "invalid key placeholder-key"},
		{name: "assembled_by_the_normalization", secret: "placeholder key", in: "invalid key placeholder\nkey"},
		{name: "rewritten_by_the_normalization", secret: "placeholder\u202ekey", in: "invalid key placeholder\u202ekey"},
		{name: "cut_by_the_cap", secret: "placeholder key", in: strings.Repeat("x", MaxFieldBytes-6) + "placeholder\nkey"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RedactedField(tt.in, httpx.Secret(tt.secret))
			if strings.Contains(got, "placeh") {
				t.Errorf("RedactedField(%q) = %q, carries a fragment of the secret", tt.in, got)
			}
			if strings.ContainsAny(got, "\n\u202e") {
				t.Errorf("RedactedField(%q) = %q, want a single line with no unsafe rune", tt.in, got)
			}
			if len(got) > MaxFieldBytes+len("...") {
				t.Errorf("RedactedField(%q) is %d bytes, want at most %d", tt.in, len(got), MaxFieldBytes+3)
			}
		})
	}
}

func TestRedactedField_marks_a_cut_value(t *testing.T) {
	t.Parallel()
	got := RedactedField(strings.Repeat("y", MaxFieldBytes+10), httpx.Secret("placeholder-key"))
	if want := strings.Repeat("y", MaxFieldBytes) + "..."; got != want {
		t.Errorf("RedactedField(long) = %q, want %q", got, want)
	}
}

func TestRedactedField_redacts_every_secret_passed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		in      string
		secrets []httpx.Secret
	}{
		{
			name:    "each_echoed",
			in:      "bad pair placeholder-user / placeholder-pass",
			secrets: []httpx.Secret{"placeholder-user", "placeholder-pass"},
		},
		{
			name:    "one_assembled_by_the_normalization",
			in:      "bad pair placeholder\nuser / placeholder-pass",
			secrets: []httpx.Secret{"placeholder user", "placeholder-pass"},
		},
		{
			name:    "a_shorter_inside_a_longer",
			in:      "bad pair placeholder-pass-user",
			secrets: []httpx.Secret{"user", "placeholder-pass-user"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := RedactedField(tt.in, tt.secrets...)
			if strings.Contains(got, "placeh") || strings.Contains(got, "user") {
				t.Errorf("RedactedField(%q, %q) = %q, carries a fragment of a secret", tt.in, tt.secrets, got)
			}
		})
	}
}
