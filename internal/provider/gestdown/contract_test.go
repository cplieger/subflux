package gestdown_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/provider/gestdown"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

func TestContract(t *testing.T) {
	f := &fake{}
	providertest.Contract(t, providertest.Harness{
		Entry: gestdown.NewEntry(),
		Query: episode(t, "en"),
		Recorded: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/subtitles/download/") {
				_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n"))
				return
			}
			f.ServeHTTP(w, r)
		}),
	})
}
