package metrics

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/api"
	"pgregory.net/rapid"
)

// --- New ---

func TestNew_creates_metrics(t *testing.T) {
	t.Parallel()
	m := New()
	if m == nil {
		t.Fatal("New() returned nil")
	}
}

// --- RecordSearch ---

func TestRecordSearch_increments_total(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("opensubtitles", 100*time.Millisecond, nil)
	m.RecordSearch("opensubtitles", 200*time.Millisecond, nil)
	m.RecordSearch("yify", 50*time.Millisecond, nil)

	if got := m.TotalSearches(); got != 3 {
		t.Errorf("TotalSearches() = %d, want 3", got)
	}
}

func TestRecordSearch_increments_errors_on_failure(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("os", 10*time.Millisecond, errors.New("timeout"))
	m.RecordSearch("os", 10*time.Millisecond, nil)

	body := renderMetrics(t, m)
	if !strings.Contains(body, `subflux_search_errors_total{provider="os"} 1`) {
		t.Error("expected 1 error for os")
	}
	if !strings.Contains(body, `subflux_searches_total{provider="os"} 2`) {
		t.Error("expected 2 searches for os")
	}
}

func TestRecordSearch_records_duration(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("os", 1*time.Second, nil)
	m.RecordSearch("os", 2*time.Second, nil)

	body := renderMetrics(t, m)
	if !strings.Contains(body, `subflux_search_duration_seconds_count{provider="os"} 2`) {
		t.Error("expected count 2")
	}
	if !strings.Contains(body, `subflux_search_duration_seconds_sum{provider="os"} 3`) {
		t.Error("expected sum 3")
	}
}

func TestRecordSearch_success_does_not_create_error_entry(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("os", 10*time.Millisecond, nil)
	m.RecordSearch("os", 20*time.Millisecond, nil)

	body := renderMetrics(t, m)
	if !strings.Contains(body, `subflux_searches_total{provider="os"} 2`) {
		t.Error("expected 2 searches for os")
	}
	// No error entry should be present for os
	if strings.Contains(body, `subflux_search_errors_total{provider="os"}`) {
		t.Error("expected no error entry for os when no errors recorded")
	}
}

// --- RecordDownload ---

func TestRecordDownload_success_does_not_create_error_entry(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordDownload("os", nil)
	m.RecordDownload("os", nil)

	body := renderMetrics(t, m)
	if !strings.Contains(body, `subflux_downloads_total{provider="os"} 2`) {
		t.Error("expected 2 downloads for os")
	}
	if strings.Contains(body, `subflux_download_errors_total{provider="os"}`) {
		t.Error("expected no dlErrors entry for os")
	}
}

func TestRecordDownload_increments_total_and_errors(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordDownload("os", nil)
	m.RecordDownload("os", errors.New("fail"))
	m.RecordDownload("yify", nil)

	body := renderMetrics(t, m)
	if !strings.Contains(body, `subflux_downloads_total{provider="os"} 1`) {
		t.Error("expected 1 download for os")
	}
	if !strings.Contains(body, `subflux_download_errors_total{provider="os"} 1`) {
		t.Error("expected 1 dlError for os")
	}
	if !strings.Contains(body, `subflux_downloads_total{provider="yify"} 1`) {
		t.Error("expected 1 download for yify")
	}
}

// --- RecordImport ---

func TestRecordImport_increments_by_source(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordImport("sonarr")
	m.RecordImport("sonarr")
	m.RecordImport("radarr")

	body := renderMetrics(t, m)
	if !strings.Contains(body, `subflux_imports_detected_total{source="sonarr"} 2`) {
		t.Error("expected 2 imports for sonarr")
	}
	if !strings.Contains(body, `subflux_imports_detected_total{source="radarr"} 1`) {
		t.Error("expected 1 import for radarr")
	}
}

// --- RecordScan ---

func TestRecordScan_stores_values(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordScan(100, 5, 3*time.Second)

	body := renderMetrics(t, m)
	if !strings.Contains(body, "subflux_scans_total 1") {
		t.Error("expected scans_total 1")
	}
	if !strings.Contains(body, "subflux_scan_items_total 100") {
		t.Error("expected scan_items_total 100")
	}
	if !strings.Contains(body, "subflux_scan_found_total 5") {
		t.Error("expected scan_found_total 5")
	}
	if !strings.Contains(body, "subflux_scan_duration_seconds 3") {
		t.Error("expected scan_duration_seconds 3")
	}
}

func TestRecordScan_accumulates(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordScan(50, 2, 1*time.Second)
	m.RecordScan(30, 3, 2*time.Second)

	body := renderMetrics(t, m)
	if !strings.Contains(body, "subflux_scans_total 2") {
		t.Error("expected scans_total 2")
	}
	if !strings.Contains(body, "subflux_scan_items_total 80") {
		t.Error("expected scan_items_total 80")
	}
	if !strings.Contains(body, "subflux_scan_found_total 5") {
		t.Error("expected scan_found_total 5")
	}
	// ScanDuration stores the last value (gauge).
	if !strings.Contains(body, "subflux_scan_duration_seconds 2") {
		t.Error("expected scan_duration_seconds 2")
	}
}

// --- AdaptiveSkip ---

func TestAdaptiveSkip_increments(t *testing.T) {
	t.Parallel()
	m := New()

	m.AdaptiveSkip()
	m.AdaptiveSkip()
	m.AdaptiveSkip()

	body := renderMetrics(t, m)
	if !strings.Contains(body, "subflux_adaptive_skips_total 3") {
		t.Error("expected adaptive_skips_total 3")
	}
}

// --- RecordEmbeddedDetectorError ---

func TestRecordEmbeddedDetectorError_increments(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordEmbeddedDetectorError()
	m.RecordEmbeddedDetectorError()

	body := renderMetrics(t, m)
	if !strings.Contains(body, "subflux_embedded_detector_errors_total 2") {
		t.Error("expected embedded_detector_errors_total 2")
	}
}

// --- SetConfigured ---

func TestSetConfigured_toggles_gauge(t *testing.T) {
	t.Parallel()
	m := New()

	// Default (never set) is the unconfigured zero-state.
	if body := renderMetrics(t, m); !strings.Contains(body, "subflux_configured 0") {
		t.Errorf("expected subflux_configured 0 by default\nbody:\n%s", body)
	}

	m.SetConfigured(true)
	if body := renderMetrics(t, m); !strings.Contains(body, "subflux_configured 1") {
		t.Error("expected subflux_configured 1 after SetConfigured(true)")
	}

	m.SetConfigured(false)
	if body := renderMetrics(t, m); !strings.Contains(body, "subflux_configured 0") {
		t.Error("expected subflux_configured 0 after SetConfigured(false)")
	}
}

// --- TotalSearches ---

func TestTotalSearches_sums_across_providers(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("os", 10*time.Millisecond, nil)
	m.RecordSearch("os", 10*time.Millisecond, nil)
	m.RecordSearch("yify", 10*time.Millisecond, nil)

	if got := m.TotalSearches(); got != 3 {
		t.Errorf("TotalSearches() = %d, want 3", got)
	}
}

func TestTotalSearches_zero_when_empty(t *testing.T) {
	t.Parallel()
	m := New()

	if got := m.TotalSearches(); got != 0 {
		t.Errorf("TotalSearches() = %d, want 0", got)
	}
}

// --- Handler ---

func TestHandler_returns_prometheus_text_format(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("opensubtitles", 500*time.Millisecond, nil)
	m.RecordSearch("opensubtitles", 1*time.Second, errors.New("fail"))
	m.RecordDownload("opensubtitles", nil)
	m.RecordImport("sonarr")
	m.RecordScan(42, 7, 2*time.Second)
	m.AdaptiveSkip()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Handler() status = %d, want %d", rec.Code, http.StatusOK)
	}

	ct := rec.Header().Get("Content-Type")
	if ct != "text/plain; version=0.0.4; charset=utf-8" && ct != "text/plain; version=0.0.4" {
		t.Errorf("Content-Type = %q, want prometheus text format", ct)
	}

	body := rec.Body.String()

	checks := []struct {
		name    string
		pattern string
	}{
		{name: "searches counter", pattern: `subflux_searches_total{provider="opensubtitles"} 2`},
		{name: "search errors", pattern: `subflux_search_errors_total{provider="opensubtitles"} 1`},
		{name: "downloads counter", pattern: `subflux_downloads_total{provider="opensubtitles"} 1`},
		{name: "imports counter", pattern: `subflux_imports_detected_total{source="sonarr"} 1`},
		{name: "scans counter", pattern: "subflux_scans_total 1"},
		{name: "scan items", pattern: "subflux_scan_items_total 42"},
		{name: "scan found", pattern: "subflux_scan_found_total 7"},
		{name: "adaptive skips", pattern: "subflux_adaptive_skips_total 1"},
		{name: "search duration count", pattern: `subflux_search_duration_seconds_count{provider="opensubtitles"} 2`},
		{name: "search duration bucket +Inf", pattern: `subflux_search_duration_seconds_bucket{provider="opensubtitles",le="+Inf"} 2`},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.pattern) {
			t.Errorf("Handler() body missing %s: want substring %q\nbody:\n%s", c.name, c.pattern, body)
		}
	}
}

func TestHandler_empty_metrics_returns_scalar_metrics(t *testing.T) {
	t.Parallel()
	m := New()

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()

	// Scalar counters and gauges should always be present even with zero values.
	zeroScalars := []string{
		"subflux_scans_total 0",
		"subflux_scan_items_total 0",
		"subflux_scan_found_total 0",
		"subflux_adaptive_skips_total 0",
	}
	for _, s := range zeroScalars {
		if !strings.Contains(body, s) {
			t.Errorf("Handler() missing metric %q for empty metrics\nbody:\n%s", s, body)
		}
	}
}

// --- Concurrent safety ---

func TestMetrics_concurrent_safety(t *testing.T) {
	t.Parallel()
	cases := []struct {
		action     func(m *Metrics, i int)
		assert     func(t *testing.T, m *Metrics, goroutines int)
		name       string
		goroutines int
	}{
		{
			name:       "RecordSearch_same_provider",
			goroutines: 50,
			action:     func(m *Metrics, _ int) { m.RecordSearch("os", 10*time.Millisecond, nil) },
			assert: func(t *testing.T, m *Metrics, n int) {
				if got := m.TotalSearches(); got != int64(n) {
					t.Errorf("TotalSearches() = %d, want %d", got, n)
				}
			},
		},
		{
			name:       "RecordSearch_distinct_providers",
			goroutines: 100,
			action: func(m *Metrics, i int) {
				m.RecordSearch(api.ProviderID(fmt.Sprintf("provider-%d", i)), 10*time.Millisecond, nil)
			},
			assert: func(t *testing.T, m *Metrics, n int) {
				if got := m.TotalSearches(); got != int64(n) {
					t.Errorf("TotalSearches() = %d, want %d", got, n)
				}
			},
		},
		{
			name:       "RecordDownload_same_provider",
			goroutines: 50,
			action:     func(m *Metrics, _ int) { m.RecordDownload("os", errors.New("fail")) },
			assert: func(t *testing.T, m *Metrics, n int) {
				body := renderMetrics(t, m)
				expected := fmt.Sprintf(`subflux_download_errors_total{provider="os"} %d`, n)
				if !strings.Contains(body, expected) {
					t.Errorf("expected %s in output", expected)
				}
			},
		},
		{
			name:       "RecordImport_distinct_sources",
			goroutines: 50,
			action:     func(m *Metrics, i int) { m.RecordImport(api.PollKey(fmt.Sprintf("source-%d", i))) },
			assert: func(t *testing.T, m *Metrics, n int) {
				body := renderMetrics(t, m)
				count := strings.Count(body, "subflux_imports_detected_total{source=")
				if count != n {
					t.Errorf("got %d import sources, want %d", count, n)
				}
			},
		},
		{
			name:       "RecordDownload_distinct_providers",
			goroutines: 100,
			action: func(m *Metrics, i int) {
				provider := api.ProviderID(fmt.Sprintf("provider-%d", i))
				if i%2 == 0 {
					m.RecordDownload(provider, nil)
				} else {
					m.RecordDownload(provider, errors.New("fail"))
				}
			},
			assert: func(t *testing.T, m *Metrics, _ int) {
				// Just verify no panic and handler works
				body := renderMetrics(t, m)
				if body == "" {
					t.Error("empty output")
				}
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := New()
			var wg sync.WaitGroup
			wg.Add(tc.goroutines)
			start := make(chan struct{})

			for i := range tc.goroutines {
				go func() {
					<-start
					tc.action(m, i)
					wg.Done()
				}()
			}

			close(start)
			wg.Wait()
			tc.assert(t, m, tc.goroutines)
		})
	}
}

// --- Handler: download errors and sorted output ---

func TestHandler_includes_download_errors(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordDownload("os", errors.New("fail"))
	m.RecordDownload("os", nil)

	body := renderMetrics(t, m)
	if !strings.Contains(body, `subflux_downloads_total{provider="os"} 1`) {
		t.Error("Handler() missing downloads_total for os")
	}
	if !strings.Contains(body, `subflux_download_errors_total{provider="os"} 1`) {
		t.Error("Handler() missing download_errors_total for os")
	}
	if !strings.Contains(body, "# HELP subflux_download_errors_total") {
		t.Error("Handler() missing HELP for download_errors_total")
	}
	if !strings.Contains(body, "# TYPE subflux_download_errors_total counter") {
		t.Error("Handler() missing TYPE for download_errors_total")
	}
}

func TestHandler_sorts_providers_alphabetically(t *testing.T) {
	t.Parallel()
	m := New()

	// Record in non-alphabetical order.
	m.RecordSearch("yify", 10*time.Millisecond, nil)
	m.RecordSearch("betaseries", 10*time.Millisecond, nil)
	m.RecordSearch("opensubtitles", 10*time.Millisecond, nil)

	body := renderMetrics(t, m)

	// Providers should appear in alphabetical order within the same metric family.
	idxB := strings.Index(body, `provider="betaseries"`)
	idxO := strings.Index(body, `provider="opensubtitles"`)
	idxY := strings.Index(body, `provider="yify"`)

	if idxB < 0 || idxO < 0 || idxY < 0 {
		t.Fatalf("Handler() missing providers in output\nbody:\n%s", body)
	}
	if idxB > idxO || idxO > idxY {
		t.Errorf("Handler() providers not sorted: betaseries@%d, opensubtitles@%d, yify@%d",
			idxB, idxO, idxY)
	}
}

// --- Handler write error ---

type errWriter struct {
	header http.Header
}

func (e *errWriter) Header() http.Header       { return e.header }
func (e *errWriter) Write([]byte) (int, error) { return 0, http.ErrAbortHandler }
func (e *errWriter) WriteHeader(int)           {}

func TestHandler_write_error_does_not_panic(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("os", 100*time.Millisecond, nil)
	m.RecordScan(10, 2, time.Second)

	w := &errWriter{header: make(http.Header)}
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/metrics", http.NoBody)
	m.Handler().ServeHTTP(w, req)
}

// --- Property-based tests ---

func TestMetrics_counter_monotonicity_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		m := New()

		providers := []string{"alpha", "beta", "gamma"}
		n := rapid.IntRange(1, 50).Draw(t, "ops")

		var totalSearches int64

		for range n {
			prov := providers[rapid.IntRange(0, len(providers)-1).Draw(t, "prov")]
			op := rapid.IntRange(0, 2).Draw(t, "op")
			hasErr := rapid.Float64Range(0, 1).Draw(t, "err") < 0.3

			switch op {
			case 0:
				var err error
				if hasErr {
					err = errors.New("fail")
				}
				m.RecordSearch(api.ProviderID(prov), 10*time.Millisecond, err)
				totalSearches++
			case 1:
				if hasErr {
					m.RecordDownload(api.ProviderID(prov), errors.New("fail"))
				} else {
					m.RecordDownload(api.ProviderID(prov), nil)
				}
			case 2:
				m.RecordScan(10, 2, time.Second)
			}
		}

		if got := m.TotalSearches(); got != totalSearches {
			t.Fatalf("TotalSearches() = %d, want %d", got, totalSearches)
		}
	})
}

// --- Concurrent stress ---

func TestMetrics_getOrCreate_concurrent_new_providers(t *testing.T) {
	t.Parallel()
	m := New()

	const uniqueProviders = 20
	const sharedCount = 20
	var wg sync.WaitGroup
	wg.Add(uniqueProviders + sharedCount)
	gate := make(chan struct{})

	for i := range uniqueProviders {
		go func() {
			<-gate
			m.RecordSearch(api.ProviderID(fmt.Sprintf("provider_%d", i)), 10*time.Millisecond, nil)
			wg.Done()
		}()
	}
	for range sharedCount {
		go func() {
			<-gate
			m.RecordSearch("shared", 10*time.Millisecond, nil)
			wg.Done()
		}()
	}

	close(gate)
	wg.Wait()

	expected := int64(uniqueProviders + sharedCount)
	if got := m.TotalSearches(); got != expected {
		t.Errorf("TotalSearches() = %d, want %d", got, expected)
	}
}

func TestRecordSearch_buckets_concurrent_safe(t *testing.T) {
	t.Parallel()
	m := New()
	const goroutines = 8
	const perGoroutine = 200
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(seed int) {
			defer wg.Done()
			for i := range perGoroutine {
				ms := (seed*perGoroutine + i) % 50_000
				m.RecordSearch("os", time.Duration(ms)*time.Millisecond, nil)
			}
		}(g)
	}
	wg.Wait()

	want := int64(goroutines * perGoroutine)
	if got := m.TotalSearches(); got != want {
		t.Errorf("TotalSearches() = %d, want %d", got, want)
	}

	body := renderMetrics(t, m)
	expected := fmt.Sprintf(`subflux_search_duration_seconds_count{provider="os"} %d`, want)
	if !strings.Contains(body, expected) {
		t.Errorf("expected %s in output", expected)
	}
}

// helper

func renderMetrics(t *testing.T, m *Metrics) string {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	return rec.Body.String()
}
