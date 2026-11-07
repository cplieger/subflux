package subsource_test

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subsource"
)

func TestContract(t *testing.T) {
	rec := &recorded{}
	refusal, err := os.ReadFile("testdata/search-401.json")
	if err != nil {
		t.Fatal(err)
	}
	providertest.Contract(t, providertest.Harness{
		Entry:  subsource.NewEntry(),
		Values: values,
		Query:  movie(t, "en", "fr"),
		Recorded: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(r.URL.Path, "/download") {
				_, _ = w.Write([]byte("PK\x03\x04"))
				return
			}
			rec.ServeHTTP(w, r)
		}),
		Refused: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write(refusal)
		}),
	})
}
