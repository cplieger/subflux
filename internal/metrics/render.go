package metrics

import (
	"io"
	"log/slog"
	"net/http"
	"strings"
)

// Handler returns an HTTP handler that serves Prometheus metrics.
func (m *Metrics) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4")

		var b strings.Builder
		b.Grow(4096)

		// Snapshot the provider map atomically (single pointer load).
		providers := m.providers.Load()

		// Provider metrics (lock-free read of atomic counters).
		writeProviderMetrics(&b, *providers)

		// Imports map: snapshot under write-side lock for copy-on-write maps.
		imports := m.importsDetected.Load()
		writeCounterMap(&b, "subflux_imports_detected_total", "Total imports detected by source", "source", *imports)

		// Scan metrics (all atomic — no lock needed).
		writeSingleCounter(&b, "subflux_scans_total", "Total full scans completed", m.scansTotal.Load())
		writeSingleCounter(&b, "subflux_scan_items_total", "Total items scanned", m.scanItemsTotal.Load())
		writeSingleCounter(&b, "subflux_scan_found_total", "Total subtitles found during scans", m.scanFoundTotal.Load())
		writeGauge(&b, "subflux_scan_duration_seconds", "Last scan duration in seconds",
			float64(m.scanDuration.Load())/1000.0)

		// Adaptive.
		writeSingleCounter(&b, "subflux_adaptive_skips_total", "Total items skipped by adaptive search", m.adaptiveSkips.Load())

		if _, err := io.WriteString(w, b.String()); err != nil { // nosemgrep: no-io-writestring-to-responsewriter -- Prometheus text format, not HTML
			slog.Debug("write metrics failed", "error", err)
		}
	}
}
