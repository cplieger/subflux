package authhandlers

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cplieger/auth/v4"
)

func TestExtractPathSegment(t *testing.T) {
	t.Parallel()
	const prefix = "/api/auth/users/"
	tests := []struct {
		name   string
		path   string
		prefix string
		suffix string
		want   string
	}{
		{"id_no_suffix", "/api/auth/users/42", prefix, "", "42"},
		{"missing_prefix", "/other/42", prefix, "", ""},
		{"segment_before_suffix", "/api/auth/users/42/passkeys", prefix, "/passkeys", "42"},
		{"suffix_not_found", "/api/auth/users/42", prefix, "/passkeys", ""},
		{"empty_segment", "/api/auth/users/", prefix, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := extractPathSegment(tc.path, tc.prefix, tc.suffix); got != tc.want {
				t.Errorf("extractPathSegment(%q, %q, %q) = %q, want %q",
					tc.path, tc.prefix, tc.suffix, got, tc.want)
			}
		})
	}
}

func TestParseIDFromPath_valid(t *testing.T) {
	t.Parallel()
	const prefix = "/api/auth/users/"
	tests := []struct {
		name string
		path string
		want int64
	}{
		{"small", "/api/auth/users/42", 42},
		{"one", "/api/auth/users/1", 1},
		{"max_int64", "/api/auth/users/9223372036854775807", 9223372036854775807},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			got, ok := parseIDFromPath(rec, tc.path, prefix, "user id")
			if !ok {
				t.Fatalf("parseIDFromPath(%q) ok = false, want true (status %d)", tc.path, rec.Code)
			}
			if got != tc.want {
				t.Errorf("parseIDFromPath(%q) = %d, want %d", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseIDFromPath_rejects(t *testing.T) {
	t.Parallel()
	const prefix = "/api/auth/users/"
	tests := []struct {
		name string
		path string
	}{
		{"missing_id", "/api/auth/users/"},
		{"non_numeric", "/api/auth/users/abc"},
		{"zero", "/api/auth/users/0"},
		{"negative", "/api/auth/users/-5"},
		{"trailing_garbage", "/api/auth/users/42x"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			got, ok := parseIDFromPath(rec, tc.path, prefix, "user id")
			if ok {
				t.Fatalf("parseIDFromPath(%q) ok = true, want false", tc.path)
			}
			if got != 0 {
				t.Errorf("parseIDFromPath(%q) id = %d, want 0 on rejection", tc.path, got)
			}
			if rec.Code != http.StatusBadRequest {
				t.Errorf("parseIDFromPath(%q) status = %d, want %d", tc.path, rec.Code, http.StatusBadRequest)
			}
		})
	}
}

// TestValidateAndHashPasswordResultsStayDistinctlyTyped pins the type
// separation, not a value: every call site writes the second result into a 400
// response body and the first into auth.User.PasswordHash, so if the two ever
// share a type again a transposed assignment compiles and each successful
// validation returns the Argon2id hash to the client.
func TestValidateAndHashPasswordResultsStayDistinctlyTyped(t *testing.T) {
	t.Parallel()
	hash, userMsg, err := ValidateAndHashPassword(t.Context(), PasswordCheck{
		Password: "correct-horse-battery-staple",
		Username: "alice",
	}, nil)
	if err != nil {
		t.Fatalf("ValidateAndHashPassword() err = %v, want nil", err)
	}
	if reflect.TypeOf(hash) == reflect.TypeOf(userMsg) {
		t.Errorf("hash and userMsg both have type %s; a transposed assignment would compile and write the hash into a 400 body",
			reflect.TypeOf(hash))
	}
}

func TestValidateAndHashPassword(t *testing.T) {
	t.Parallel()
	const (
		username = "alice"
		password = "correct-horse-battery-staple"
	)
	tests := []struct {
		name     string
		check    PasswordCheck
		wantMsg  string // substring; "" means accepted
		wantHash bool
	}{
		{
			name:     "accepted",
			check:    PasswordCheck{Password: password, Username: username},
			wantHash: true,
		},
		{
			name:    "too_short",
			check:   PasswordCheck{Password: "short", Username: username},
			wantMsg: "characters",
		},
		{
			name:    "contains_username",
			check:   PasswordCheck{Password: "alice-in-wonderland-and-beyond", Username: username},
			wantMsg: "username",
		},
		{
			name:    "contains_app_name",
			check:   PasswordCheck{Password: "my-subflux-password-here", Username: username},
			wantMsg: "forbidden word",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			hash, userMsg, err := ValidateAndHashPassword(t.Context(), tc.check, nil)
			if err != nil {
				t.Fatalf("ValidateAndHashPassword() err = %v, want nil", err)
			}
			if tc.wantMsg == "" {
				if userMsg != "" {
					t.Errorf("userMsg = %q, want empty", userMsg)
				}
			} else if !strings.Contains(userMsg, tc.wantMsg) {
				t.Errorf("userMsg = %q, want it to mention %q", userMsg, tc.wantMsg)
			}
			if !tc.wantHash {
				if hash != "" {
					t.Errorf("hash = %q, want empty on rejection", hash)
				}
				return
			}
			// The accepted result must be the hash of the password, not the
			// message: this is the assertion a transposition breaks.
			ok, verifyErr := auth.VerifyPassword(tc.check.Password, string(hash))
			if verifyErr != nil {
				t.Fatalf("VerifyPassword(%q) err = %v", hash, verifyErr)
			}
			if !ok {
				t.Errorf("VerifyPassword(password, hash) = false; hash result %q does not hash the password", hash)
			}
		})
	}
}
