package obs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBuildInfoCarriesStampedVersion(t *testing.T) {
	prev := buildVersion
	buildVersion = "v9.8.7"
	t.Cleanup(func() { buildVersion = prev })
	rec := httptest.NewRecorder()
	New().Handler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	if want := `subflux_build_info{version="v9.8.7"} 1`; !strings.Contains(string(body), want) {
		t.Errorf("the exposition lacks %s:\n%s", want, body)
	}
}
