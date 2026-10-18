package obs_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/metrics/v4"
	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/webhttp/v3"
)

func scrape(t *testing.T, m *obs.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler()(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	b, err := io.ReadAll(rec.Result().Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHTTPDurationBuckets(t *testing.T) {
	m := obs.New()
	m.RecordHTTP(webhttp.RequestMetric{Method: "GET", Path: "/api/health", Status: 200, Latency: 3 * time.Second})
	body := scrape(t, m)
	if !strings.Contains(body, `subflux_http_request_duration_seconds_bucket{le="5"} 1`) {
		t.Errorf("a 3 s request is not in the 5 s bucket:\n%s", body)
	}
	if !strings.Contains(body, `subflux_http_request_duration_seconds_bucket{le="2.5"} 0`) {
		t.Errorf("the 2.5 s bucket is missing or holds the 3 s request:\n%s", body)
	}
	if !strings.Contains(body, `subflux_http_requests_total{method="GET",path="/api/health",status="200"} 1`) {
		t.Errorf("the request series is missing:\n%s", body)
	}
}

func TestRegisterSiblingCollector(t *testing.T) {
	m := obs.New()
	c := metrics.NewCounter("sse_publish_refused_total", "Refused publishes.")
	if err := m.Register(c); err != nil {
		t.Fatalf("Register = %v", err)
	}
	c.Inc()
	if body := scrape(t, m); !strings.Contains(body, "subflux_sse_publish_refused_total 1") {
		t.Errorf("the sibling counter is not exposed:\n%s", body)
	}
	if err := m.Register(metrics.NewCounter("sse_publish_refused_total", "Again.")); err == nil {
		t.Error("a duplicate family registered")
	}
	if err := m.Register(metrics.NewCounter("panics_total", "Clash.")); err == nil {
		t.Error("a family clashing with a platform series registered")
	}
}

func TestConfigModeGauge(t *testing.T) {
	m := obs.New()
	for _, mode := range []string{"unconfigured", "active", "failed"} {
		m.SetConfigMode(mode)
		body := scrape(t, m)
		for _, other := range []string{"unconfigured", "failed", "active"} {
			want := "0"
			if other == mode {
				want = "1"
			}
			if line := `subflux_config_mode{mode="` + other + `"} ` + want; !strings.Contains(body, line) {
				t.Errorf("after SetConfigMode(%q) the exposition lacks %s", mode, line)
			}
		}
	}
	rec := capture.Default(t)
	m.SetConfigMode("bogus")
	if body := scrape(t, m); strings.Contains(body, "bogus") || !strings.Contains(body, `subflux_config_mode{mode="failed"} 1`) {
		t.Errorf("an unknown mode changed the gauge:\n%s", body)
	}
	if rec.CountLevel(slog.LevelError, "obs:") != 1 {
		t.Errorf("logged %q, want one ERROR", rec.Messages())
	}
}

func TestPlatformSeries(t *testing.T) {
	m := obs.New()
	m.RecordPanic()
	body := scrape(t, m)
	for _, want := range []string{"subflux_panics_total 1", `subflux_build_info{version="`} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition lacks %q:\n%s", want, body)
		}
	}
}
