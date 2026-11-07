package betaseries_test

import (
	"net/http"
	"testing"

	"github.com/cplieger/subflux/internal/provider/betaseries"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

func TestContract(t *testing.T) {
	var req http.Request
	search := serveFile(t, http.StatusOK, "testdata/episodes-81189-s01e01.json", &req)
	providertest.Contract(t, providertest.Harness{
		Entry:  betaseries.NewEntry(),
		Values: values,
		Query:  episode(t, "en"),
		Recorded: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Host {
			case apiHost:
				search(w, r)
			case siteHost:
				http.Redirect(w, r, "https://"+subHost+"/opensubtitles/breaking.bad.s01.e01.pilot.zip", http.StatusFound)
			default:
				_, _ = w.Write([]byte("PK\x03\x04"))
			}
		}),
		Refused: serveFile(t, http.StatusBadRequest, "testdata/error-1001.json", &req),
	})
}
