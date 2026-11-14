package obs_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/go/httpserver"
	"github.com/cplieger/go/measure"
	"github.com/cplieger/go/slogx/capture"
	"github.com/cplieger/subflux/internal/buildinfo"
	"github.com/cplieger/subflux/internal/obs"
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

func TestBuildInfoCarriesVersion(t *testing.T) {
	if want := `subflux_build_info{version="` + buildinfo.Version() + `"} 1`; !strings.Contains(scrape(t, obs.New()), want) {
		t.Errorf("the exposition lacks %s", want)
	}
}

func TestHTTPDurationBuckets(t *testing.T) {
	m := obs.New()
	m.RecordHTTP(httpserver.RequestMetric{Method: "GET", Path: "/api/health", Status: 200, Latency: 3 * time.Second})
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
	c := measure.NewCounter("sse_publish_refused_total", "Refused publishes.")
	if err := m.Register(c); err != nil {
		t.Fatalf("Register = %v", err)
	}
	c.Inc()
	if body := scrape(t, m); !strings.Contains(body, "subflux_sse_publish_refused_total 1") {
		t.Errorf("the sibling counter is not exposed:\n%s", body)
	}
	if err := m.Register(measure.NewCounter("sse_publish_refused_total", "Again.")); err == nil {
		t.Error("a duplicate family registered")
	}
	if err := m.Register(measure.NewCounter("panics_total", "Clash.")); err == nil {
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

func TestBackupSeries(t *testing.T) {
	m := obs.New()
	m.SetBackupSchedule(true, 24*time.Hour)
	m.SetBackupLastSuccess(time.Unix(1700000000, 0))
	m.IncBackupFailure()
	body := scrape(t, m)
	for _, want := range []string{"subflux_backup_enabled 1\n", "subflux_backup_interval_seconds 86400\n", "subflux_backup_last_success_timestamp_seconds 1700000000\n", "subflux_backup_failures_total 1\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition lacks %q:\n%s", want, body)
		}
	}
	m.SetBackupSchedule(false, time.Hour)
	m.SetBackupLastSuccess(time.Time{})
	body = scrape(t, m)
	for _, want := range []string{"subflux_backup_enabled 0\n", "subflux_backup_interval_seconds 3600\n", "subflux_backup_last_success_timestamp_seconds 0\n"} {
		if !strings.Contains(body, want) {
			t.Errorf("exposition lacks %q:\n%s", want, body)
		}
	}
}
