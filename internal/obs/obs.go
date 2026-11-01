// Package obs is subflux's /metrics registry: the platform series and the
// door every other owner's series registers through. It wraps
// github.com/cplieger/metrics under another name so the two never collide at
// an import.
package obs

import (
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"slices"
	"strconv"

	"github.com/cplieger/metrics/v4"
	"github.com/cplieger/webhttp/v3"
)

// Metric is the metrics library's collector, so a series another package
// declares with that library registers here.
type Metric = metrics.Metric

var configModes = []string{"unconfigured", "failed", "active"}

// buildVersion is the release tag the image build stamps with -X; a build
// without it reports the module version.
var buildVersion string

// Metrics is the process's one registry; New builds it.
type Metrics struct {
	registry     *metrics.Registry
	httpRequests *metrics.LabeledCounter
	httpDuration *metrics.Histogram
	panics       *metrics.Counter
	configMode   *metrics.LabeledGauge
}

// New builds the registry, prefix subflux, with the platform series
// registered and build_info set. The library already exports
// process_start_time_seconds beside its other process series.
func New() *Metrics {
	m := &Metrics{
		registry:     metrics.NewRegistry("subflux"),
		httpRequests: metrics.NewLabeledCounter("http_requests_total", "HTTP requests by method, route and status.", []string{"method", "path", "status"}),
		httpDuration: metrics.NewHistogram("http_request_duration_seconds", "HTTP request latency.", metrics.WithBuckets(metrics.APIBuckets())),
		panics:       metrics.NewCounter("panics_total", "HTTP handler panics recovered."),
		configMode:   metrics.NewLabeledGauge("config_mode", "1 for the configuration's current mode, 0 for the others.", []string{"mode"}),
	}
	buildInfo := metrics.NewLabeledGauge("build_info", "Always 1, labelled with the running version.", []string{"version"})
	m.registry.MustRegister(m.httpRequests, m.httpDuration, m.panics, m.configMode, buildInfo)
	buildInfo.Set(1, version())
	for _, mode := range configModes {
		m.configMode.Set(0, mode)
	}
	return m
}

func version() string {
	if buildVersion != "" {
		return buildVersion
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
		return bi.Main.Version
	}
	return "unknown"
}

// Register adds other owners' collectors in turn, stopping at the first
// error: a duplicate family, or one a collector captured at construction.
func (m *Metrics) Register(ms ...Metric) error {
	for _, c := range ms {
		if err := m.registry.Register(c); err != nil {
			return fmt.Errorf("obs: %w", err)
		}
	}
	return nil
}

// Handler serves the exposition.
func (m *Metrics) Handler() http.HandlerFunc { return m.registry.Handler() }

// RecordHTTP is webhttp.WithRecordRouteMetric's hook: the library bounds the
// method and route labels.
func (m *Metrics) RecordHTTP(r webhttp.RequestMetric) {
	metrics.RecordHTTP(m.httpRequests, m.httpDuration, r.Latency, r.Method, r.Path, strconv.Itoa(r.Status))
}

// RecordPanic counts one recovered handler panic.
func (m *Metrics) RecordPanic() { m.panics.Inc() }

// SetConfigMode sets mode's config_mode series to 1 and the others to 0. A
// mode outside settings' three is a programming fault: logged at ERROR, and
// nothing changes.
func (m *Metrics) SetConfigMode(mode string) {
	if !slices.Contains(configModes, mode) {
		slog.Error("obs: unknown config mode", "mode", mode)
		return
	}
	for _, other := range configModes {
		v := 0.0
		if other == mode {
			v = 1
		}
		m.configMode.Set(v, other)
	}
}
