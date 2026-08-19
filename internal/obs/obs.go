// Package obs owns Subflux's observability surface: the Prometheus registry
// (exposition prefix "subflux_") and every counter, gauge and histogram it
// serves — searches, downloads, imports, scans, the HTTP transport, and the
// bbolt store. The collectors are unexported, so a caller records through a
// named method and cannot register, rename or delete a series.
//
// Named obs, not metrics: the github.com/cplieger/metrics library it wraps
// owns that name, and while this package was called metrics the collision
// forced every reference to the library through an extmetrics alias. The
// rename removed the collision, so the library is imported plainly.
package obs

import (
	"net/http"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/cplieger/metrics/v4"
	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/webhttp/v2"
)

// Metrics holds all application metrics.
type Metrics struct {
	scansTotal   *metrics.Counter
	errors       *metrics.LabeledCounter
	downloads    *metrics.LabeledCounter
	dlErrors     *metrics.LabeledCounter
	durations    *metrics.LabeledHistogram
	imports      *metrics.LabeledCounter
	searches     *metrics.LabeledCounter
	scanItems    *metrics.Counter
	scanFound    *metrics.Counter
	scanDur      *metrics.Gauge
	adaptSkips   *metrics.Counter
	embDetErrs   *metrics.Counter
	httpRequests *metrics.LabeledCounter
	httpDuration *metrics.Histogram
	httpPanics   *metrics.Counter
	registry     *metrics.Registry

	// Store observability (Requirement 17).
	storeFileBytes     *metrics.Gauge
	storeFreelistBytes *metrics.Gauge
	reconcileDuration  *metrics.Gauge
	reconcileDeleted   *metrics.Counter
	reconcileReset     *metrics.Counter
	backupLastSuccess  *metrics.Gauge
	backupDuration     *metrics.Gauge

	// Mode observability.
	configured *metrics.Gauge

	// Poll-cursor durability (S13): >0 while a cursor's durable persist is
	// failing (in-memory position ahead of disk; restart would replay).
	pollCursorsDirty *metrics.Gauge

	totalSearch atomic.Int64
}

// New creates a new Metrics instance.
func New() *Metrics {
	labels := []string{"provider"}

	m := &Metrics{
		searches:     metrics.NewLabeledCounter("searches_total", "Total subtitle searches by provider", labels),
		errors:       metrics.NewLabeledCounter("search_errors_total", "Total search errors by provider", labels),
		downloads:    metrics.NewLabeledCounter("downloads_total", "Total subtitle downloads by provider", labels),
		dlErrors:     metrics.NewLabeledCounter("download_errors_total", "Total download errors by provider", labels),
		durations:    metrics.NewLabeledHistogram("search_duration_seconds", "Search duration", labels, metrics.WithBuckets(metrics.APIBuckets())),
		imports:      metrics.NewLabeledCounter("imports_detected_total", "Total imports detected by source", []string{"source"}),
		scansTotal:   metrics.NewCounter("scans_total", "Total full scans completed"),
		scanItems:    metrics.NewCounter("scan_items_total", "Total items scanned"),
		scanFound:    metrics.NewCounter("scan_found_total", "Total subtitles found during scans"),
		scanDur:      metrics.NewGauge("scan_duration_seconds", "Last scan duration in seconds"),
		adaptSkips:   metrics.NewCounter("adaptive_skips_total", "Total items skipped by adaptive search"),
		embDetErrs:   metrics.NewCounter("embedded_detector_errors_total", "Total embedded track detector failures (context cancellations excluded)"),
		httpRequests: metrics.NewLabeledCounter("http_requests_total", "Total HTTP requests", []string{"method", "path", "status"}),
		httpDuration: metrics.NewHistogram("http_request_duration_seconds", "HTTP request latency"),
		httpPanics:   metrics.NewCounter("http_panics_total", "Total HTTP handler panics recovered by the Recoverer middleware"),

		// Store observability.
		storeFileBytes:     metrics.NewGauge("store_file_bytes", "Current bbolt database file size in bytes"),
		storeFreelistBytes: metrics.NewGauge("store_freelist_bytes", "Reclaimable freelist bytes in the bbolt database"),
		reconcileDuration:  metrics.NewGauge("reconcile_duration_seconds", "Duration of last reconcile pass in seconds"),
		reconcileDeleted:   metrics.NewCounter("reconcile_deleted_total", "Total subtitle paths deleted by reconciliation"),
		reconcileReset:     metrics.NewCounter("reconcile_reset_total", "Total triples reset by reconciliation"),
		backupLastSuccess:  metrics.NewGauge("backup_last_success_timestamp", "Unix timestamp of last successful backup"),
		backupDuration:     metrics.NewGauge("backup_duration_seconds", "Duration of last successful backup in seconds"),

		// Mode observability.
		configured: metrics.NewGauge("configured", "1 when a valid configuration is active, 0 in unconfigured mode"),

		pollCursorsDirty: metrics.NewGauge("poll_cursors_dirty", "Number of poll cursors whose durable persist is failing (in-memory ahead of disk)"),
	}

	m.registry = metrics.NewRegistry("subflux")
	// MustRegister is the right door here: New has no error result and every
	// failure it can report (bad metric name, bad label set, family collision,
	// invalid registry prefix) is a programming error fixed at the literal
	// above, not runtime state a caller could handle.
	m.registry.MustRegister(
		m.searches,
		m.errors,
		m.downloads,
		m.dlErrors,
		m.durations,
		m.imports,
		m.scansTotal,
		m.scanItems,
		m.scanFound,
		m.scanDur,
		m.adaptSkips,
		m.embDetErrs,
		m.httpRequests,
		m.httpDuration,
		m.httpPanics,
		m.storeFileBytes,
		m.storeFreelistBytes,
		m.reconcileDuration,
		m.reconcileDeleted,
		m.reconcileReset,
		m.backupLastSuccess,
		m.backupDuration,
		m.configured,
		m.pollCursorsDirty,
	)

	return m
}

// SetPollCursorsDirty records how many poll cursors currently have a failing
// durable persist (S13 dirty-cursor observability; 0 when healthy).
func (m *Metrics) SetPollCursorsDirty(n int) {
	m.pollCursorsDirty.Set(float64(n))
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

// RecordHTTP records one HTTP request. It takes webhttp's named-field record
// rather than four positional values so the two adjacent strings cannot be
// transposed on the way to a metric label.
func (m *Metrics) RecordHTTP(rm webhttp.RequestMetric) {
	metrics.RecordHTTP(m.httpRequests, m.httpDuration, rm.Latency, rm.Method, rm.Path, strconv.Itoa(rm.Status))
}

// RecordPanic records one HTTP handler panic recovered by the webhttp.Recoverer
// middleware. Wired as Recoverer's WithPanicHook so a recovered 500 also
// increments a monitorable counter.
func (m *Metrics) RecordPanic() {
	m.httpPanics.Inc()
}

// AdaptiveSkip records an item skipped by adaptive search.
func (m *Metrics) AdaptiveSkip() {
	m.adaptSkips.Inc()
}

// RecordEmbeddedDetectorError counts a failed embedded track probe. The
// search engine excludes context cancellations before calling this.
func (m *Metrics) RecordEmbeddedDetectorError() {
	m.embDetErrs.Inc()
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
