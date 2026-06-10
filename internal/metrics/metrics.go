// Package metrics provides Prometheus-compatible metrics for Subflux.
// Exposes counters and summaries for searches, downloads, and provider health.
package metrics

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	extmetrics "github.com/cplieger/metrics/v2"
	"github.com/cplieger/subflux/internal/api"
)

// Metrics holds all application metrics.
type Metrics struct {
	scansTotal   *extmetrics.Counter
	errors       *extmetrics.LabeledCounter
	downloads    *extmetrics.LabeledCounter
	dlErrors     *extmetrics.LabeledCounter
	durations    *extmetrics.LabeledHistogram
	imports      *extmetrics.LabeledCounter
	searches     *extmetrics.LabeledCounter
	scanItems    *extmetrics.Counter
	scanFound    *extmetrics.Counter
	scanDur      *extmetrics.Gauge
	adaptSkips   *extmetrics.Counter
	httpRequests *extmetrics.LabeledCounter
	httpDuration *extmetrics.Histogram
	registry     *extmetrics.Registry
	totalSearch  atomic.Int64
}

// New creates a new Metrics instance.
func New() *Metrics {
	labels := []string{"provider"}

	m := &Metrics{
		searches:     extmetrics.NewLabeledCounter("searches_total", "Total subtitle searches by provider", labels),
		errors:       extmetrics.NewLabeledCounter("search_errors_total", "Total search errors by provider", labels),
		downloads:    extmetrics.NewLabeledCounter("downloads_total", "Total subtitle downloads by provider", labels),
		dlErrors:     extmetrics.NewLabeledCounter("download_errors_total", "Total download errors by provider", labels),
		durations:    extmetrics.NewLabeledHistogram("search_duration_seconds", "Search duration", labels, extmetrics.WithBuckets(extmetrics.APIBuckets)),
		imports:      extmetrics.NewLabeledCounter("imports_detected_total", "Total imports detected by source", []string{"source"}),
		scansTotal:   extmetrics.NewCounter("scans_total", "Total full scans completed"),
		scanItems:    extmetrics.NewCounter("scan_items_total", "Total items scanned"),
		scanFound:    extmetrics.NewCounter("scan_found_total", "Total subtitles found during scans"),
		scanDur:      extmetrics.NewGauge("scan_duration_seconds", "Last scan duration in seconds"),
		adaptSkips:   extmetrics.NewCounter("adaptive_skips_total", "Total items skipped by adaptive search"),
		httpRequests: extmetrics.NewLabeledCounter("http_requests_total", "Total HTTP requests", []string{"method", "path", "status"}),
		httpDuration: extmetrics.NewHistogram("http_request_duration_seconds", "HTTP request latency"),
	}

	m.registry = extmetrics.NewRegistry("subflux")
	m.registry.RegisterLabeledCounter(m.searches)
	m.registry.RegisterLabeledCounter(m.errors)
	m.registry.RegisterLabeledCounter(m.downloads)
	m.registry.RegisterLabeledCounter(m.dlErrors)
	m.registry.RegisterLabeledHistogram(m.durations)
	m.registry.RegisterLabeledCounter(m.imports)
	m.registry.RegisterCounter(m.scansTotal)
	m.registry.RegisterCounter(m.scanItems)
	m.registry.RegisterCounter(m.scanFound)
	m.registry.RegisterGauge(m.scanDur)
	m.registry.RegisterCounter(m.adaptSkips)
	m.registry.RegisterLabeledCounter(m.httpRequests)
	m.registry.RegisterHistogram(m.httpDuration)

	return m
}

// RecordSearch records a search attempt for a provider.
func (m *Metrics) RecordSearch(provider api.ProviderID, dur time.Duration, err error) {
	p := string(provider)
	m.searches.Inc(p)
	m.totalSearch.Add(1)
	if err != nil {
		m.errors.Inc(p)
	}
	m.durations.Observe(dur.Seconds(), p)
}

// RecordDownload records a download attempt, routing to the success or error counter.
func (m *Metrics) RecordDownload(provider api.ProviderID, err error) {
	p := string(provider)
	if err == nil {
		m.downloads.Inc(p)
	} else {
		m.dlErrors.Inc(p)
	}
}

// RecordImport records an import detected.
func (m *Metrics) RecordImport(source api.PollKey) {
	m.imports.Inc(string(source))
}

// RecordScan records scan completion.
func (m *Metrics) RecordScan(items, found int, dur time.Duration) {
	m.scansTotal.Inc()
	m.scanItems.Add(int64(items))
	m.scanFound.Add(int64(found))
	m.scanDur.Set(dur.Seconds())
}

// RecordHTTP records one HTTP request (method, path, status, duration).
func (m *Metrics) RecordHTTP(method, path string, status int, d time.Duration) {
	extmetrics.RecordHTTP(m.httpRequests, m.httpDuration, d, method, path, strconv.Itoa(status))
}

// AdaptiveSkip records an item skipped by adaptive search.
func (m *Metrics) AdaptiveSkip() {
	m.adaptSkips.Inc()
}

// TotalSearches returns the cumulative search count across all providers.
func (m *Metrics) TotalSearches() int64 {
	return m.totalSearch.Load()
}

// Handler returns an HTTP handler that serves Prometheus metrics.
func (m *Metrics) Handler() http.HandlerFunc {
	return m.registry.Handler()
}
