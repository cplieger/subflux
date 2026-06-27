package authhandlers

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
