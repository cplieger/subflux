package assrt_test

import (
	"net/http"
	"testing"

	"github.com/cplieger/subflux/internal/provider/providertest"
)

func TestContract(t *testing.T) {
	providertest.Contract(t, providertest.Harness{
		Entry:    entry(),
		Values:   values,
		Query:    inception("zh-Hans", "en"),
		Recorded: recorded(),
		Refused: &site{routes: map[string]route{
			"/v1/user/quota": {file: "status-20001.json", status: http.StatusBadRequest},
			"/v1/sub/search": {file: "status-20001.json", status: http.StatusBadRequest},
		}},
	})
}
