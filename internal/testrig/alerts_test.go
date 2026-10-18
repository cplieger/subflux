package testrig

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"testing"

	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/webhttp/v4"
	"go.yaml.in/yaml/v3"
)

// promqlWords are the PromQL functions and keywords the shipped rules may
// use; any other identifier in an expr must be a series.
var promqlWords = []string{
	"sum", "max", "min", "avg", "count", "increase", "rate", "irate", "absent", "time",
	"and", "or", "unless", "by", "without", "bool", "offset", "histogram_quantile",
}

var (
	promqlStrip = regexp.MustCompile(`"[^"]*"|\{[^}]*\}|\[[^\]]*\]|\b(by|without)\s*\([^)]*\)`)
	promqlIdent = regexp.MustCompile(`[a-zA-Z_:][a-zA-Z0-9_:]*`)
	exposedType = regexp.MustCompile(`(?m)^# TYPE (\S+) (\S+)$`)
)

// exportedSeries is every series name obs exposes once each family has a
// sample, histogram suffixes included, plus Prometheus' own up.
func exportedSeries(t *testing.T) []string {
	t.Helper()
	m := obs.New()
	m.RecordHTTP(webhttp.RequestMetric{Method: http.MethodGet, Path: "/", Status: http.StatusOK})
	rec := httptest.NewRecorder()
	m.Handler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", http.NoBody))
	body, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{"up"}
	for _, match := range exposedType.FindAllStringSubmatch(string(body), -1) {
		names = append(names, match[1])
		if match[2] == "histogram" {
			names = append(names, match[1]+"_bucket", match[1]+"_sum", match[1]+"_count")
		}
	}
	return names
}

func TestAlertRulesNameExportedSeries(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(moduleRoot, "alerts", "promql.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var file struct {
		Groups []struct {
			Rules []struct{ Alert, Expr string }
		}
	}
	if err := yaml.Unmarshal(raw, &file); err != nil {
		t.Fatal(err)
	}
	series := exportedSeries(t)
	rules := 0
	for _, g := range file.Groups {
		for _, r := range g.Rules {
			rules++
			for _, id := range promqlIdent.FindAllString(promqlStrip.ReplaceAllString(r.Expr, " "), -1) {
				if !slices.Contains(promqlWords, id) && !slices.Contains(series, id) {
					t.Errorf("%s reads %s, which subflux does not export", r.Alert, id)
				}
			}
		}
	}
	if rules == 0 {
		t.Fatal("alerts/promql.yaml holds no rule")
	}
}
