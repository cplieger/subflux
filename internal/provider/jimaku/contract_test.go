package jimaku_test

import (
	"net/http"
	"testing"

	"github.com/cplieger/subflux/internal/provider/jimaku"
	"github.com/cplieger/subflux/internal/provider/providertest"
)

func TestContract(t *testing.T) {
	ep01 := "/entry/4464/download/1 Litre of Tears ep01 (1280x720 WMV9).jp.srt"
	providertest.Contract(t, providertest.Harness{
		Entry:  jimaku.NewEntry(),
		Values: values,
		Query:  episode(6484),
		Recorded: &site{routes: map[string]string{
			search("tv:6484", "false"): "search-tv-6484.json",
			"/api/entries/4464/files":  "files-4464.json",
			ep01:                       "dl-4464-ep01.srt",
		}},
		Refused: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized","code":7}`))
		}),
	})
}
