package subdl

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/runesafe/v2"
	"github.com/cplieger/subflux/internal/subflux"
)

func TestCheckAPIStatus_classifies_a_refused_key_as_auth(t *testing.T) {
	t.Parallel()
	for _, msg := range []string{"Not Authorized", "Invalid API key", "invalid key", "API key not found"} {
		_, err := (&Provider{}).checkAPIStatus(&apiResponse{Status: false, Error: runesafe.Untrusted(msg)}, "Movie (2024)")
		if _, ok := errors.AsType[*subflux.AuthError](err); !ok {
			t.Errorf("checkAPIStatus(%q) = %v, want *subflux.AuthError", msg, err)
		}
	}
}

func TestCheckAPIStatus_keeps_other_failures_out_of_auth(t *testing.T) {
	t.Parallel()
	for _, msg := range []string{"Daily quota exceeded", "something broke"} {
		_, err := (&Provider{}).checkAPIStatus(&apiResponse{Status: false, Error: runesafe.Untrusted(msg)}, "Movie (2024)")
		if err == nil {
			t.Fatalf("checkAPIStatus(%q) = nil, want an error", msg)
		}
		if _, ok := errors.AsType[*subflux.AuthError](err); ok {
			t.Errorf("checkAPIStatus(%q) = AuthError, want a plain error", msg)
		}
		if strings.Contains(err.Error(), "not found") {
			t.Errorf("checkAPIStatus(%q) = %q, want no not-found claim", msg, err.Error())
		}
	}
}

func TestSearch_reports_a_refused_key_answered_with_http_200(t *testing.T) {
	t.Parallel()
	p := &Provider{
		apiKey: "placeholder-api-key",
		client: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"status":false,"error":"Not Authorized"}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}
	subs, err := p.Search(t.Context(), &subflux.SearchRequest{
		MediaType: subflux.MediaTypeMovie, ImdbID: "tt0111161", Languages: []string{"en"},
	})
	if _, ok := errors.AsType[*subflux.AuthError](err); !ok {
		t.Errorf("Search() = (%v, %v), want *subflux.AuthError", subs, err)
	}
}
