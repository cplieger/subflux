package opensubtitles_test

import (
	"net/http"
	"testing"

	"github.com/cplieger/subflux/internal/provider/opensubtitles"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

func TestContract(t *testing.T) {
	providertest.Contract(t, providertest.Harness{
		Entry:    opensubtitles.NewEntry(),
		Values:   values,
		Query:    movie(t, "en", "fr"),
		Recorded: &recorded{},
		Refused: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusForbidden)
			serveFile(w, "testdata/login-403.json")
		}),
	})
}
