package subx_test

import (
	"net/http"
	"testing"

	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subx"
)

func TestContract(t *testing.T) {
	providertest.Contract(t, providertest.Harness{
		Entry:    subx.NewEntry(),
		Values:   values,
		Query:    inception("es-ES", "es-419"),
		Recorded: recorded(),
		Refused: &site{routes: map[string]route{
			"/robots.txt": {file: "robots-404.html", status: http.StatusNotFound},
			search:        {file: "search-401.json", status: http.StatusUnauthorized},
		}},
	})
}
