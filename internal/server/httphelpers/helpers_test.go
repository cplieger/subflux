package httphelpers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/server/httphelpers"
)

type decodeTarget struct {
	Name string `json:"name"`
	N    int    `json:"n"`
}

func TestDecodeJSONBody_valid(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{"name":"sub","n":7}`))
	rec := httptest.NewRecorder()

	var dst decodeTarget
	ok := httphelpers.DecodeJSONBody(rec, req, &dst, 0)

	if !ok {
		t.Fatal("DecodeJSONBody(valid) = false, want true")
	}
	if dst.Name != "sub" || dst.N != 7 {
		t.Errorf("decoded = %+v, want {Name:sub N:7}", dst)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (no error response on success)", rec.Code)
	}
}

func TestDecodeJSONBody_invalid_returns_400(t *testing.T) {
	t.Parallel()
	req := httptest.NewRequest(http.MethodPost, "/x",
		strings.NewReader(`{not json`))
	rec := httptest.NewRecorder()

	var dst decodeTarget
	ok := httphelpers.DecodeJSONBody(rec, req, &dst, 0)

	if ok {
		t.Fatal("DecodeJSONBody(invalid) = true, want false")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

// A JSON value larger than the byte cap is truncated by the LimitReader into
// invalid JSON, so the decode is rejected. This pins the size-cap boundary:
// remove the cap and the oversized value decodes successfully.
func TestDecodeJSONBody_oversized_returns_400(t *testing.T) {
	t.Parallel()
	big := `"` + strings.Repeat("x", 100) + `"`
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(big))
	rec := httptest.NewRecorder()

	var dst string
	ok := httphelpers.DecodeJSONBody(rec, req, &dst, 5)

	if ok {
		t.Fatal("DecodeJSONBody(oversized, cap=5) = true, want false")
	}
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestRequireMethod(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		want     string
		got      string
		pass     bool
		wantCode int
	}{
		{"match", http.MethodPost, http.MethodPost, true, http.StatusOK},
		{"mismatch", http.MethodPost, http.MethodGet, false, http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(tc.got, "/x", http.NoBody)
			rec := httptest.NewRecorder()

			ok := httphelpers.RequireMethod(rec, req, tc.want)

			if ok != tc.pass {
				t.Errorf("RequireMethod(got %s, want %s) = %v, want %v",
					tc.got, tc.want, ok, tc.pass)
			}
			if rec.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", rec.Code, tc.wantCode)
			}
		})
	}
}

// RequireGET and RequirePOST are sugar over RequireMethod; verify each wires
// the correct method by rejecting the opposite verb with 405.
func TestRequireGET_and_RequirePOST_reject_opposite_method(t *testing.T) {
	t.Parallel()

	getRec := httptest.NewRecorder()
	if httphelpers.RequireGET(getRec, httptest.NewRequest(http.MethodPost, "/x", http.NoBody)) {
		t.Error("RequireGET(POST) = true, want false")
	}
	if getRec.Code != http.StatusMethodNotAllowed {
		t.Errorf("RequireGET(POST) status = %d, want 405", getRec.Code)
	}

	postRec := httptest.NewRecorder()
	if httphelpers.RequirePOST(postRec, httptest.NewRequest(http.MethodGet, "/x", http.NoBody)) {
		t.Error("RequirePOST(GET) = true, want false")
	}
	if postRec.Code != http.StatusMethodNotAllowed {
		t.Errorf("RequirePOST(GET) status = %d, want 405", postRec.Code)
	}
}
