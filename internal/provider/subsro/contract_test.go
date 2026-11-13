package subsro_test

import (
	"net/http"
	"testing"

	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subsro"
)

func TestContract(t *testing.T) {
	providertest.Contract(t, providertest.Harness{
		Entry:    subsro.NewEntry(),
		Values:   values,
		Query:    inception("ro", "en"),
		Recorded: recorded(),
		Refused:  &site{routes: map[string]route{"/v1.0/search/imdbid/tt1375666": {file: "search-403.json", status: http.StatusForbidden}}},
	})
}
