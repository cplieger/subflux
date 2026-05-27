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

	"subflux/internal/api"

	"pgregory.net/rapid"
)

// *Metrics satisfies consumer-side interfaces (search.SearchMetrics,
// scanning.ScanMetrics, server.ServerMetrics, etc.) via structural typing.

// --- New ---

func TestNew_creates_metrics(t *testing.T) {
	t.Parallel()
	m := New()
	if m == nil {
		t.Fatal("New() returned nil")
	}
	if m.providers.Load() == nil {
		t.Error("providers map not initialized")
	}
	if m.importsDetected.Load() == nil {
		t.Error("importsDetected map not initialized")
	}
}

// --- RecordSearch ---

func TestRecordSearch_increments_total(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("opensubtitles", 100*time.Millisecond, nil)
	m.RecordSearch("opensubtitles", 200*time.Millisecond, nil)
	m.RecordSearch("yify", 50*time.Millisecond, nil)

	got := (*m.providers.Load())["opensubtitles"].searches.Load()
	if got != 2 {
		t.Errorf("providers[opensubtitles].searches = %d, want 2", got)
	}
	got = (*m.providers.Load())["yify"].searches.Load()
	if got != 1 {
		t.Errorf("providers[yify].searches = %d, want 1", got)
	}
}

func TestRecordSearch_increments_errors_on_failure(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("os", 10*time.Millisecond, errors.New("timeout"))
	m.RecordSearch("os", 10*time.Millisecond, nil)

	if got := (*m.providers.Load())["os"].errors.Load(); got != 1 {
		t.Errorf("providers[os].errors = %d, want 1", got)
	}
	if got := (*m.providers.Load())["os"].searches.Load(); got != 2 {
		t.Errorf("providers[os].searches = %d, want 2", got)
	}
}

func TestRecordSearch_records_duration(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("os", 1*time.Second, nil)
	m.RecordSearch("os", 2*time.Second, nil)

	h := (*m.providers.Load())["os"]
	sum, count, _ := h.durations.Snapshot()
	if count != 2 {
		t.Errorf("SearchDurations[os].Count() = %d, want 2", count)
	}
	if sum != 3.0 {
		t.Errorf("SearchDurations[os].Sum() = %f, want 3.0", sum)
	}
}

func TestRecordSearch_success_does_not_create_error_entry(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordSearch("os", 10*time.Millisecond, nil)
	m.RecordSearch("os", 20*time.Millisecond, nil)

	if got := (*m.providers.Load())["os"].searches.Load(); got != 2 {
		t.Errorf("searches[os] = %d, want 2", got)
	}
	if got := (*m.providers.Load())["os"].errors.Load(); got != 0 {
		t.Errorf("errors[os] = %d, want 0 (no errors recorded)", got)
	}
}

// --- RecordDownload ---

func TestRecordDownload_success_does_not_create_error_entry(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordDownload("os", nil)
	m.RecordDownload("os", nil)

	if got := (*m.providers.Load())["os"].downloads.Load(); got != 2 {
		t.Errorf("downloads[os] = %d, want 2", got)
	}
	if got := (*m.providers.Load())["os"].dlErrors.Load(); got != 0 {
		t.Errorf("dlErrors[os] = %d, want 0 (no errors recorded)", got)
	}
}

func TestRecordDownload_increments_total_and_errors(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordDownload("os", nil)
	m.RecordDownload("os", errors.New("fail"))
	m.RecordDownload("yify", nil)

	if got := (*m.providers.Load())["os"].downloads.Load(); got != 1 {
		t.Errorf("downloads[os] = %d, want 1", got)
	}
	if got := (*m.providers.Load())["os"].dlErrors.Load(); got != 1 {
		t.Errorf("dlErrors[os] = %d, want 1", got)
	}
	if got := (*m.providers.Load())["yify"].downloads.Load(); got != 1 {
		t.Errorf("downloads[yify] = %d, want 1", got)
	}
}

// --- RecordImport ---

func TestRecordImport_increments_by_source(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordImport("sonarr")
	m.RecordImport("sonarr")
	m.RecordImport("radarr")

	if got := (*m.importsDetected.Load())["sonarr"].Load(); got != 2 {
		t.Errorf("importsDetected[sonarr] = %d, want 2", got)
	}
	if got := (*m.importsDetected.Load())["radarr"].Load(); got != 1 {
		t.Errorf("importsDetected[radarr] = %d, want 1", got)
	}
}

// --- RecordScan ---

func TestRecordScan_stores_values(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordScan(100, 5, 3*time.Second)

	if got := m.scansTotal.Load(); got != 1 {
		t.Errorf("scansTotal = %d, want 1", got)
	}
	if got := m.scanItemsTotal.Load(); got != 100 {
		t.Errorf("scanItemsTotal = %d, want 100", got)
	}
	if got := m.scanFoundTotal.Load(); got != 5 {
		t.Errorf("scanFoundTotal = %d, want 5", got)
	}
	if got := m.scanDuration.Load(); got != 3000 {
		t.Errorf("scanDuration = %d, want 3000", got)
	}
}

func TestRecordScan_accumulates(t *testing.T) {
	t.Parallel()
	m := New()

	m.RecordScan(50, 2, 1*time.Second)
	m.RecordScan(30, 3, 2*time.Second)

	if got := m.scansTotal.Load(); got != 2 {
		t.Errorf("scansTotal = %d, want 2", got)
	}
	if got := m.scanItemsTotal.Load(); got != 80 {
		t.Errorf("scanItemsTotal = %d, want 80", got)
	}
	if got := m.scanFoundTotal.Load(); got != 5 {
		t.Errorf("scanFoundTotal = %d, want 5", got)
	}
	// ScanDuration stores the last value, not cumulative.
	if got := m.scanDuration.Load(); got != 2000 {
		t.Errorf("scanDuration = %d, want 2000 (last scan)", got)
	}
}

// --- AdaptiveSkip ---

func TestAdaptiveSkip_increments(t *testing.T) {
	t.Parallel()
	m := New()

	m.AdaptiveSkip()
	m.AdaptiveSkip()
	m.AdaptiveSkip()

	if got := m.adaptiveSkips.Load(); got != 3 {
		t.Errorf("adaptiveSkips = %d, want 3", got)
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
	if ct != "text/plain; version=0.0.4" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain; version=0.0.4")
	}

	body := rec.Body.String()

	// Verify key metrics are present.
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
		{name: "scan duration", pattern: "subflux_scan_duration_seconds 2.000"},
		{name: "adaptive skips", pattern: "subflux_adaptive_skips_total 1"},
		{name: "search duration count", pattern: `subflux_search_duration_seconds_count{provider="opensubtitles"} 2`},
		{name: "search duration sum", pattern: `subflux_search_duration_seconds_sum{provider="opensubtitles"} 1.500`},
		{name: "search duration bucket 0.5", pattern: `subflux_search_duration_seconds_bucket{provider="opensubtitles",le="0.5"}`},
		{name: "search duration bucket 1", pattern: `subflux_search_duration_seconds_bucket{provider="opensubtitles",le="1"}`},
		{name: "search duration bucket +Inf", pattern: `subflux_search_duration_seconds_bucket{provider="opensubtitles",le="+Inf"} 2`},
		{name: "TYPE counter", pattern: "# TYPE subflux_searches_total counter"},
		{name: "TYPE single counter", pattern: "# TYPE subflux_scans_total counter"},
		{name: "TYPE gauge", pattern: "# TYPE subflux_scan_duration_seconds gauge"},
		{name: "TYPE histogram", pattern: "# TYPE subflux_search_duration_seconds histogram"},
		{name: "HELP searches", pattern: "# HELP subflux_searches_total Total subtitle searches by provider"},
		{name: "HELP scans", pattern: "# HELP subflux_scans_total Total full scans completed"},
		{name: "HELP duration", pattern: "# HELP subflux_search_duration_seconds Search duration"},
	}
	for _, c := range checks {
		if !strings.Contains(body, c.pattern) {
			t.Errorf("Handler() body missing %s: want substring %q", c.name, c.pattern)
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
		"subflux_scan_duration_seconds 0.000",
		"subflux_adaptive_skips_total 0",
	}
	for _, s := range zeroScalars {
		if !strings.Contains(body, s) {
			t.Errorf("Handler() missing metric %q for empty metrics", s)
		}
	}

	// Counter maps and summaries should be absent when no providers recorded.
	absentFamilies := []string{
		"subflux_searches_total",
		"subflux_search_errors_total",
		"subflux_downloads_total",
		"subflux_download_errors_total",
		"subflux_imports_detected_total",
		"subflux_search_duration_seconds",
	}
	for _, family := range absentFamilies {
		if strings.Contains(body, family) {
			t.Errorf("Handler() should not emit %q when no data recorded", family)
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
				if got := (*m.providers.Load())["os"].searches.Load(); got != int64(n) {
					t.Errorf("searchesTotal[os] = %d, want %d", got, n)
				}
				_, count, _ := (*m.providers.Load())["os"].durations.Snapshot()
				if count != int64(n) {
					t.Errorf("searchDurations[os].Count = %d, want %d", count, n)
				}
			},
		},
		{
			name:       "RecordDownload_same_provider",
			goroutines: 50,
			action:     func(m *Metrics, _ int) { m.RecordDownload("os", errors.New("fail")) },
			assert: func(t *testing.T, m *Metrics, n int) {
				if got := (*m.providers.Load())["os"].downloads.Load(); got != 0 {
					t.Errorf("downloads[os] = %d, want 0 (errors only)", got)
				}
				if got := (*m.providers.Load())["os"].dlErrors.Load(); got != int64(n) {
					t.Errorf("dlErrors[os] = %d, want %d", got, n)
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
				if provCount := len(*m.providers.Load()); provCount != n {
					t.Errorf("providers has %d entries, want %d", provCount, n)
				}
			},
		},
		{
			name:       "RecordImport_distinct_sources",
			goroutines: 50,
			action:     func(m *Metrics, i int) { m.RecordImport(api.PollKey(fmt.Sprintf("source-%d", i))) },
			assert: func(t *testing.T, m *Metrics, n int) {
				if importCount := len(*m.importsDetected.Load()); importCount != n {
					t.Errorf("importsDetected has %d sources, want %d", importCount, n)
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
			assert: func(t *testing.T, m *Metrics, n int) {
				if provCount := len(*m.providers.Load()); provCount != n {
					t.Errorf("providers has %d entries, want %d", provCount, n)
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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()

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

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", http.NoBody)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)

	body := rec.Body.String()

	// Providers should appear in alphabetical order.
	idxB := strings.Index(body, `provider="betaseries"`)
	idxO := strings.Index(body, `provider="opensubtitles"`)
	idxY := strings.Index(body, `provider="yify"`)

	if idxB < 0 || idxO < 0 || idxY < 0 {
		t.Fatalf("Handler() missing providers in output")
	}
	if idxB > idxO || idxO > idxY {
		t.Errorf("Handler() providers not sorted: betaseries@%d, opensubtitles@%d, yify@%d",
			idxB, idxO, idxY)
	}
}

// --- Handler write error ---

// errWriter is an http.ResponseWriter that fails on Write.
// Used to exercise the write-error branch in Handler.
type errWriter struct {
	header http.Header
}

func (e *errWriter) Header() http.Header       { return e.header }
func (e *errWriter) Write([]byte) (int, error) { return 0, http.ErrAbortHandler }
func (e *errWriter) WriteHeader(int)           {}

func TestHandler_write_error_does_not_panic(t *testing.T) {
	t.Parallel()
	// Verifies the handler degrades gracefully when the response writer fails.
	m := New()

	m.RecordSearch("os", 100*time.Millisecond, nil)
	m.RecordScan(10, 2, time.Second)

	w := &errWriter{header: make(http.Header)}
	req := httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/metrics", http.NoBody)
	m.Handler().ServeHTTP(w, req)

	// Reaching here without panic is the assertion.
}

// --- Concurrent creation of distinct providers ---

// --- Property-based tests ---

func TestMetrics_counter_monotonicity_property(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(t *rapid.T) {
		m := New()

		providers := []string{"alpha", "beta", "gamma"}
		n := rapid.IntRange(1, 50).Draw(t, "ops")

		var totalSearches int64
		searchCounts := map[string]int64{}
		errorCounts := map[string]int64{}
		dlSuccess := map[string]int64{}
		dlError := map[string]int64{}

		for range n {
			prov := providers[rapid.IntRange(0, len(providers)-1).Draw(t, "prov")]
			op := rapid.IntRange(0, 2).Draw(t, "op")
			hasErr := rapid.Float64Range(0, 1).Draw(t, "err") < 0.3

			switch op {
			case 0: // RecordSearch
				var err error
				if hasErr {
					err = errors.New("fail")
					errorCounts[prov]++
				}
				m.RecordSearch(api.ProviderID(prov), 10*time.Millisecond, err)
				searchCounts[prov]++
				totalSearches++
			case 1: // RecordDownload
				if hasErr {
					m.RecordDownload(api.ProviderID(prov), errors.New("fail"))
					dlError[prov]++
				} else {
					m.RecordDownload(api.ProviderID(prov), nil)
					dlSuccess[prov]++
				}
			case 2: // RecordScan
				m.RecordScan(10, 2, time.Second)
			}
		}

		// Invariant 1: searches[P] == expected count
		for prov, expected := range searchCounts {
			got := (*m.providers.Load())[api.ProviderID(prov)].searches.Load()
			if got != expected {
				t.Fatalf("searches[%s] = %d, want %d", prov, got, expected)
			}
		}
		// Invariant 2: errors[P] <= searches[P]
		for prov := range searchCounts {
			pm := (*m.providers.Load())[api.ProviderID(prov)]
			if pm.errors.Load() > pm.searches.Load() {
				t.Fatalf("errors[%s]=%d > searches[%s]=%d", prov, pm.errors.Load(), prov, pm.searches.Load())
			}
		}
		// Invariant 3: downloads[P] == expected success count
		for prov := range dlSuccess {
			got := (*m.providers.Load())[api.ProviderID(prov)].downloads.Load()
			if got != dlSuccess[prov] {
				t.Fatalf("downloads[%s] = %d, want %d", prov, got, dlSuccess[prov])
			}
		}
		for prov := range dlError {
			got := (*m.providers.Load())[api.ProviderID(prov)].dlErrors.Load()
			if got != dlError[prov] {
				t.Fatalf("dlErrors[%s] = %d, want %d", prov, got, dlError[prov])
			}
		}
		// Invariant 4: TotalSearches() == sum of per-provider counts
		if got := m.TotalSearches(); got != totalSearches {
			t.Fatalf("TotalSearches() = %d, want %d", got, totalSearches)
		}
		// Invariant 5: scansTotal is non-negative (monotonic from zero)
		if got := m.scansTotal.Load(); got < 0 {
			t.Fatalf("scansTotal = %d, want >= 0", got)
		}
	})
}

// --- Concurrent stress: getOrCreate double-checked locking ---

func TestMetrics_getOrCreate_concurrent_new_providers(t *testing.T) {
	t.Parallel()
	m := New()

	const uniqueProviders = 20
	const sharedCount = 20
	var wg sync.WaitGroup
	wg.Add(uniqueProviders + sharedCount)
	gate := make(chan struct{})

	// 20 goroutines each creating a unique provider
	for i := range uniqueProviders {
		go func() {
			<-gate
			m.RecordSearch(api.ProviderID(fmt.Sprintf("provider_%d", i)), 10*time.Millisecond, nil)
			wg.Done()
		}()
	}
	// 20 goroutines all hitting the same "shared" provider
	for range sharedCount {
		go func() {
			<-gate
			m.RecordSearch("shared", 10*time.Millisecond, nil)
			wg.Done()
		}()
	}

	close(gate)
	wg.Wait()

	// (a) All 21 providers exist
	provCount := len(*m.providers.Load())
	if provCount != uniqueProviders+1 {
		t.Errorf("providers has %d entries, want %d", provCount, uniqueProviders+1)
	}

	// (b) shared == 20
	if got := (*m.providers.Load())["shared"].searches.Load(); got != sharedCount {
		t.Errorf("searches[shared] = %d, want %d", got, sharedCount)
	}

	// (c) Each unique provider has count == 1
	for i := range uniqueProviders {
		key := fmt.Sprintf("provider_%d", i)
		if got := (*m.providers.Load())[api.ProviderID(key)].searches.Load(); got != 1 {
			t.Errorf("searches[%s] = %d, want 1", key, got)
		}
	}
}

// TestRecordSearch_bucket_distribution validates that durationHist
// produces the cumulative bucket counts that Prometheus expects:
// the counter at boundary B counts every observation where d <= B,
// and the +Inf bucket equals total count.
func TestRecordSearch_bucket_distribution(t *testing.T) {
	t.Parallel()
	m := New()
	// Observations covering several bucket transitions.
	durations := []time.Duration{
		50 * time.Millisecond,  // <= 0.1
		200 * time.Millisecond, // <= 0.25
		700 * time.Millisecond, // <= 1.0
		3 * time.Second,        // <= 5.0
		15 * time.Second,       // <= 30.0
		60 * time.Second,       // > 30; lands only in +Inf bucket
	}
	for _, d := range durations {
		m.RecordSearch("os", d, nil)
	}

	_, count, buckets := (*m.providers.Load())["os"].durations.Snapshot()
	if count != int64(len(durations)) {
		t.Fatalf("count = %d, want %d", count, len(durations))
	}

	// bucketBounds order: 0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0
	want := [9]int64{
		1, // <= 0.1   (50ms)
		2, // <= 0.25  (50ms, 200ms)
		2, // <= 0.5   (50ms, 200ms)
		3, // <= 1.0   (50ms, 200ms, 700ms)
		3, // <= 2.5   (same as <=1.0)
		4, // <= 5.0   (+ 3s)
		4, // <= 10.0  (same)
		5, // <= 30.0  (+ 15s)
		6, // +Inf
	}
	for i, w := range want {
		if buckets[i] != w {
			t.Errorf("buckets[%d] = %d, want %d", i, buckets[i], w)
		}
	}
}

// TestRecordSearch_buckets_concurrent_safe exercises the cumulative
// bucket counters under concurrent recording. Verifies the +Inf bucket
// always equals total count (the strongest invariant) and that the
// cumulative property holds (each bucket >= the previous one).
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
				// Spread durations across buckets deterministically.
				ms := (seed*perGoroutine + i) % 50_000
				m.RecordSearch("os", time.Duration(ms)*time.Millisecond, nil)
			}
		}(g)
	}
	wg.Wait()

	_, count, buckets := (*m.providers.Load())["os"].durations.Snapshot()
	want := int64(goroutines * perGoroutine)
	if count != want {
		t.Errorf("count = %d, want %d", count, want)
	}
	if buckets[len(buckets)-1] != want {
		t.Errorf("+Inf bucket = %d, want %d (must equal total count)", buckets[len(buckets)-1], want)
	}
	for i := 1; i < len(buckets); i++ {
		if buckets[i] < buckets[i-1] {
			t.Errorf("buckets[%d] = %d < buckets[%d] = %d (cumulative property violated)",
				i, buckets[i], i-1, buckets[i-1])
		}
	}
}
