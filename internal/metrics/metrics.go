// Package metrics provides Prometheus-compatible metrics for Subflux.
// Exposes counters and summaries for searches, downloads, and provider health.
package metrics

import (
	"maps"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"subflux/internal/api"
)

// Compile-time assertion moved to metrics_test.go to avoid
// circular dependency concerns and keep the assertion in test scope.

// Metrics holds all application metrics.
//
// The providers and importsDetected maps are stored behind atomic pointers
// using copy-on-write semantics. The common read path (RecordSearch for an
// existing provider, Handler rendering) performs a single atomic load with
// no lock. The rare write path (first time a provider is seen at startup)
// takes mu for the copy-on-write append.
type Metrics struct {
	providers       atomic.Pointer[map[api.ProviderID]*providerMetrics]
	importsDetected atomic.Pointer[map[string]*atomic.Int64]
	scansTotal      atomic.Int64
	scanItemsTotal  atomic.Int64
	scanFoundTotal  atomic.Int64
	scanDuration    atomic.Int64
	adaptiveSkips   atomic.Int64
	mu              sync.RWMutex // write-side lock for copy-on-write map appends
}

// providerMetrics holds all per-provider counters in a single struct,
// reducing map lookups from 3 per RecordSearch to 1.
type providerMetrics struct {
	searches  atomic.Int64
	errors    atomic.Int64
	downloads atomic.Int64
	dlErrors  atomic.Int64
	durations durationHist
}

// New creates a new Metrics instance.
func New() *Metrics {
	m := &Metrics{}
	providers := make(map[api.ProviderID]*providerMetrics)
	m.providers.Store(&providers)
	imports := make(map[string]*atomic.Int64)
	m.importsDetected.Store(&imports)
	return m
}

// RecordSearch records a search attempt for a provider.
func (m *Metrics) RecordSearch(provider api.ProviderID, dur time.Duration, err error) {
	pm := m.getOrCreateProvider(provider)
	pm.searches.Add(1)
	if err != nil {
		pm.errors.Add(1)
	}
	pm.durations.Record(dur)
}

// RecordDownload records a download attempt, routing to the success or error counter.
func (m *Metrics) RecordDownload(provider api.ProviderID, err error) {
	pm := m.getOrCreateProvider(provider)
	if err == nil {
		pm.downloads.Add(1)
	} else {
		pm.dlErrors.Add(1)
	}
}

// RecordImport records an import detected.
func (m *Metrics) RecordImport(source api.PollKey) {
	m.getOrCreateImport(string(source)).Add(1)
}

// RecordScan records scan completion.
func (m *Metrics) RecordScan(items, found int, dur time.Duration) {
	m.scansTotal.Add(1)
	m.scanItemsTotal.Add(int64(items))
	m.scanFoundTotal.Add(int64(found))
	m.scanDuration.Store(dur.Milliseconds())
}

// AdaptiveSkip records an item skipped by adaptive search.
func (m *Metrics) AdaptiveSkip() {
	m.adaptiveSkips.Add(1)
}

// TotalSearches returns the cumulative search count across all providers.
func (m *Metrics) TotalSearches() int64 {
	providers := m.providers.Load()
	var total int64
	for _, pm := range *providers {
		total += pm.searches.Load()
	}
	return total
}

// getOrCreateProvider returns the providerMetrics for key. The hot path
// (provider already exists) is a single atomic pointer load + map lookup
// with no lock. The cold path (new provider at startup) uses copy-on-write
// under a write lock.
func (m *Metrics) getOrCreateProvider(key api.ProviderID) *providerMetrics {
	// Hot path: read-only atomic load.
	providers := m.providers.Load()
	if pm, ok := (*providers)[key]; ok {
		return pm
	}

	// Cold path: copy-on-write under write lock.
	m.mu.Lock()

	// Double-check after acquiring lock.
	providers = m.providers.Load()
	if pm, ok := (*providers)[key]; ok {
		m.mu.Unlock()
		return pm
	}

	pm := &providerMetrics{}
	newMap := make(map[api.ProviderID]*providerMetrics, len(*providers)+1)
	maps.Copy(newMap, *providers)
	newMap[key] = pm
	m.providers.Store(&newMap)
	m.mu.Unlock()
	return pm
}

// getOrCreateImport returns the atomic counter for the given import source.
// Same lock-free hot path / copy-on-write cold path as getOrCreateProvider.
func (m *Metrics) getOrCreateImport(key string) *atomic.Int64 {
	// Hot path: read-only atomic load.
	imports := m.importsDetected.Load()
	if v, ok := (*imports)[key]; ok {
		return v
	}

	// Cold path: copy-on-write under write lock.
	m.mu.Lock()

	// Double-check after acquiring lock.
	imports = m.importsDetected.Load()
	if v, ok := (*imports)[key]; ok {
		m.mu.Unlock()
		return v
	}

	v := &atomic.Int64{}
	newMap := make(map[string]*atomic.Int64, len(*imports)+1)
	maps.Copy(newMap, *imports)
	newMap[key] = v
	m.importsDetected.Store(&newMap)
	m.mu.Unlock()
	return v
}

// durationHist tracks search-duration sum, count, and cumulative bucket
// counts for a Prometheus histogram. Cumulative buckets per the Prometheus
// convention: the counter at bucket boundary B counts every observation
// where d <= B. The implicit +Inf bucket equals total count.
//
// Uses atomic operations to avoid lock contention on the hot recording path.
type durationHist struct {
	sumBits atomic.Uint64 // float64 bits stored atomically
	count   atomic.Int64
	// buckets[i] is the cumulative count of observations <= BucketBounds[i].
	// Index len(BucketBounds) is the +Inf bucket; equals total count by definition.
	buckets [len(BucketBounds) + 1]atomic.Int64
}

// BucketBounds are the histogram boundaries in seconds. Tuned for the
// observed range of provider search durations (cache hits ~50-200ms,
// normal queries 0.5-2s, slow API calls 2-10s, near-timeout 10-30s).
// The set follows Prometheus's documented latency-bucket guidance with
// the upper end extended for slow providers.
//
// Adding/removing a boundary is a wire-format change for Grafana
// dashboards depending on these labels; bump cautiously.
var BucketBounds = [8]float64{0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0}

func (h *durationHist) Record(d time.Duration) {
	secs := d.Seconds()
	for {
		old := h.sumBits.Load()
		oldF := math.Float64frombits(old)
		newF := oldF + secs
		if h.sumBits.CompareAndSwap(old, math.Float64bits(newF)) {
			break
		}
	}
	h.count.Add(1)
	// Cumulative bucket increment: find the first bucket whose boundary
	// is >= secs, then increment all buckets from that index onward.
	// This makes the cumulative invariant explicit and avoids redundant
	// comparisons for the common fast-observation case.
	for i, bound := range BucketBounds {
		if secs <= bound {
			for j := i; j < len(BucketBounds); j++ {
				h.buckets[j].Add(1)
			}
			break
		}
	}
	// +Inf bucket: every observation counts.
	h.buckets[len(BucketBounds)].Add(1)
}

// Snapshot returns sum, count, and per-bucket cumulative counts.
// Atomic loads are not coordinated as a transaction; the values reflect
// the histogram at slightly different instants but are individually
// consistent — sufficient for metric scraping where eventual consistency
// is the norm.
func (h *durationHist) Snapshot() (sum float64, count int64, buckets [len(BucketBounds) + 1]int64) {
	count = h.count.Load()
	sum = math.Float64frombits(h.sumBits.Load())
	for i := range buckets {
		buckets[i] = h.buckets[i].Load()
	}
	return sum, count, buckets
}
