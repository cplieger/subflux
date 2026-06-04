package httphelpers

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequireMethod_match(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	if !RequireMethod(w, r, http.MethodPost) {
		t.Fatal("RequireMethod should return true for matching method")
	}
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
}

func TestRequireMethod_mismatch(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	if RequireMethod(w, r, http.MethodPost) {
		t.Fatal("RequireMethod should return false for mismatched method")
	}
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", w.Code)
	}
}

func TestRequireGET(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	w := httptest.NewRecorder()
	if !RequireGET(w, r) {
		t.Fatal("RequireGET should return true for GET")
	}
}

func TestRequirePOST(t *testing.T) {
	t.Parallel()
	r := httptest.NewRequest(http.MethodPost, "/", nil)
	w := httptest.NewRecorder()
	if !RequirePOST(w, r) {
		t.Fatal("RequirePOST should return true for POST")
	}
}

func TestDecodeJSONBody_valid(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`{"name":"test"}`)
	r := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	var dst struct{ Name string }
	if !DecodeJSONBody(w, r, &dst, 0) {
		t.Fatal("DecodeJSONBody should return true for valid JSON")
	}
	if dst.Name != "test" {
		t.Fatalf("Name = %q, want %q", dst.Name, "test")
	}
}

func TestDecodeJSONBody_invalid(t *testing.T) {
	t.Parallel()
	body := strings.NewReader(`not json`)
	r := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	var dst struct{ Name string }
	if DecodeJSONBody(w, r, &dst, 0) {
		t.Fatal("DecodeJSONBody should return false for invalid JSON")
	}
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

func TestDecodeJSONBody_truncated(t *testing.T) {
	t.Parallel()
	// Body larger than maxBytes limit should fail decode (truncated JSON).
	body := strings.NewReader(`{"name":"` + strings.Repeat("x", 100) + `"}`)
	r := httptest.NewRequest(http.MethodPost, "/", body)
	w := httptest.NewRecorder()
	var dst struct{ Name string }
	if DecodeJSONBody(w, r, &dst, 10) {
		t.Fatal("DecodeJSONBody should return false when body exceeds maxBytes")
	}
}
