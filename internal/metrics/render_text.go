package metrics

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"

	"subflux/internal/api"
)

// metricDescriptor defines a per-provider counter metric rendered in Prometheus format.
type metricDescriptor struct {
	Field func(*providerMetrics) int64
	Name  string
	Help  string
}

// providerMetricDescriptors is the declarative table of per-provider counter metrics.
var providerMetricDescriptors = []metricDescriptor{
	{Name: "subflux_searches_total", Help: "Total subtitle searches by provider", Field: func(p *providerMetrics) int64 { return p.searches.Load() }},
	{Name: "subflux_search_errors_total", Help: "Total search errors by provider", Field: func(p *providerMetrics) int64 { return p.errors.Load() }},
	{Name: "subflux_downloads_total", Help: "Total subtitle downloads by provider", Field: func(p *providerMetrics) int64 { return p.downloads.Load() }},
	{Name: "subflux_download_errors_total", Help: "Total download errors by provider", Field: func(p *providerMetrics) int64 { return p.dlErrors.Load() }},
}

// writeProviderMetrics emits all per-provider counter and summary metrics.
func writeProviderMetrics(b *strings.Builder, providers map[api.ProviderID]*providerMetrics) {
	if len(providers) == 0 {
		return
	}
	keys := slices.Sorted(maps.Keys(providers))

	for _, desc := range providerMetricDescriptors {
		fmt.Fprintf(b, "# HELP %s %s\n", desc.Name, desc.Help)
		fmt.Fprintf(b, "# TYPE %s counter\n", desc.Name)
		for _, k := range keys {
			fmt.Fprintf(b, "%s{provider=%q} %d\n", desc.Name, string(k), desc.Field(providers[k]))
		}
	}

	fmt.Fprintf(b, "# HELP subflux_search_duration_seconds Search duration\n")
	fmt.Fprintf(b, "# TYPE subflux_search_duration_seconds histogram\n")
	for _, k := range keys {
		sum, count, buckets := providers[k].durations.Snapshot()
		for i, bound := range BucketBounds {
			fmt.Fprintf(b, "subflux_search_duration_seconds_bucket{provider=%q,le=%q} %d\n",
				string(k), formatBucketBound(bound), buckets[i])
		}
		fmt.Fprintf(b, "subflux_search_duration_seconds_bucket{provider=%q,le=\"+Inf\"} %d\n",
			string(k), buckets[len(BucketBounds)])
		fmt.Fprintf(b, "subflux_search_duration_seconds_sum{provider=%q} %.3f\n", string(k), sum)
		fmt.Fprintf(b, "subflux_search_duration_seconds_count{provider=%q} %d\n", string(k), count)
	}
}

// formatBucketBound produces the canonical Prometheus float-as-label
// form. 0.1 stays "0.1"; 1.0 becomes "1" (Prometheus drops trailing
// .0); 2.5 stays "2.5". Avoids label-set drift across scrapes.
func formatBucketBound(b float64) string {
	if b == float64(int64(b)) {
		return strconv.FormatInt(int64(b), 10)
	}
	return strconv.FormatFloat(b, 'g', -1, 64)
}

func writeCounterMap(b *strings.Builder, name, help, label string, mp map[string]*atomic.Int64) {
	if len(mp) == 0 {
		return
	}
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", name)
	for _, key := range slices.Sorted(maps.Keys(mp)) {
		fmt.Fprintf(b, "%s{%s=%q} %d\n", name, label, key, mp[key].Load())
	}
}

func writeSingleCounter(b *strings.Builder, name, help string, val int64) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s counter\n", name)
	fmt.Fprintf(b, "%s %d\n", name, val)
}

func writeGauge(b *strings.Builder, name, help string, val float64) {
	fmt.Fprintf(b, "# HELP %s %s\n", name, help)
	fmt.Fprintf(b, "# TYPE %s gauge\n", name)
	fmt.Fprintf(b, "%s %.3f\n", name, val)
}
