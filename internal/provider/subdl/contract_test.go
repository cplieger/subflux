package subdl_test

import (
	"net/http"
	"testing"

	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/provider/providertest"
	"github.com/cplieger/subflux/internal/provider/subdl"
)

func TestContract(t *testing.T) {
	search := &recorder{file: "testdata/search-movie-tt1375666.json"}
	providertest.Contract(t, providertest.Harness{
		Entry:  subdl.NewEntry(),
		Values: values,
		Query:  provider.Query{Kind: media.Movie, External: media.ExternalIDs{IMDb: 1375666}, Langs: tags(t, "en", "fr")},
		Recorded: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Host == dlHost {
				_, _ = w.Write([]byte("PK\x03\x04"))
				return
			}
			search.ServeHTTP(w, r)
		}),
		Refused: &recorder{file: "testdata/search-403.json", status: http.StatusForbidden},
	})
}
