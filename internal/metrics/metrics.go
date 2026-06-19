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

	// Store observability (Requirement 17).
	storeFileBytes     *extmetrics.Gauge
	storeFreelistBytes *extmetrics.Gauge
	reconcileDuration  *extmetrics.Gauge
	reconcileDeleted   *extmetrics.Counter
	reconcileReset     *extmetrics.Counter
	backupLastSuccess  *extmetrics.Gauge
	backupDuration     *extmetrics.Gauge

	// Mode observability.
	configured *extmetrics.Gauge

	totalSearch atomic.Int64
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

		// Store observability.
		storeFileBytes:     extmetrics.NewGauge("store_file_bytes", "Current bbolt database file size in bytes"),
		storeFreelistBytes: extmetrics.NewGauge("store_freelist_bytes", "Reclaimable freelist bytes in the bbolt database"),
		reconcileDuration:  extmetrics.NewGauge("reconcile_duration_seconds", "Duration of last reconcile pass in seconds"),
		reconcileDeleted:   extmetrics.NewCounter("reconcile_deleted_total", "Total subtitle paths deleted by reconciliation"),
		reconcileReset:     extmetrics.NewCounter("reconcile_reset_total", "Total triples reset by reconciliation"),
		backupLastSuccess:  extmetrics.NewGauge("backup_last_success_timestamp", "Unix timestamp of last successful backup"),
		backupDuration:     extmetrics.NewGauge("backup_duration_seconds", "Duration of last successful backup in seconds"),

		// Mode observability.
		configured: extmetrics.NewGauge("configured", "1 when a valid configuration is active, 0 in unconfigured mode"),
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
	m.registry.RegisterGauge(m.storeFileBytes)
	m.registry.RegisterGauge(m.storeFreelistBytes)
	m.registry.RegisterGauge(m.reconcileDuration)
	m.registry.RegisterCounter(m.reconcileDeleted)
	m.registry.RegisterCounter(m.reconcileReset)
	m.registry.RegisterGauge(m.backupLastSuccess)
	m.registry.RegisterGauge(m.backupDuration)
	m.registry.RegisterGauge(m.configured)

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

// --- Store observability (Requirement 17) ---

// RecordStoreFileSize records the current bbolt database file size.
func (m *Metrics) RecordStoreFileSize(bytes int64) {
	m.storeFileBytes.Set(float64(bytes))
}

// RecordStoreFreelistBytes records the reclaimable freelist bytes.
func (m *Metrics) RecordStoreFreelistBytes(bytes int64) {
	m.storeFreelistBytes.Set(float64(bytes))
}

// RecordReconcile records metrics from a reconcile pass.
func (m *Metrics) RecordReconcile(deleted int, reset int64, dur time.Duration) {
	m.reconcileDuration.Set(dur.Seconds())
	m.reconcileDeleted.Add(int64(deleted))
	m.reconcileReset.Add(reset)
}

// RecordBackupSuccess records a successful backup's timestamp and duration.
func (m *Metrics) RecordBackupSuccess(dur time.Duration) {
	m.backupLastSuccess.Set(float64(time.Now().Unix()))
	m.backupDuration.Set(dur.Seconds())
}

// SetConfigured records whether a valid configuration is active. It sets the
// subflux_configured gauge to 1 when configured (background automation running)
// and 0 in unconfigured mode (scheduler/poller/backup goroutines skipped),
// enabling a level-triggered "stuck unconfigured" alert.
func (m *Metrics) SetConfigured(ok bool) {
	v := 0.0
	if ok {
		v = 1.0
	}
	m.configured.Set(v)
}
