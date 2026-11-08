// Package obs is subflux's /metrics registry: the platform series and the
// door every other owner's series registers through. It wraps
// github.com/cplieger/go/measure under another name so the two never collide at
// an import.
package obs

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"

	"github.com/cplieger/go/httpserver"
	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/buildinfo"
)

// Metric is the metrics library's collector, so a series another package
// declares with that library registers here.
type Metric = measure.Metric

var configModes = []string{"unconfigured", "failed", "active"}

// Metrics is the process's one registry; New builds it.
type Metrics struct {
	registry     *measure.Registry
	httpRequests *measure.LabeledCounter
	httpDuration *measure.Histogram
	panics       *measure.Counter
	configMode   *measure.LabeledGauge
	alertsActive *measure.LabeledGauge
}

// New builds the registry, prefix subflux, with the platform series
// registered and build_info set. The library already exports
// process_start_time_seconds beside its other process series.
func New() *Metrics {
	m := &Metrics{
		registry:     measure.NewRegistry("subflux"),
		httpRequests: measure.NewLabeledCounter("http_requests_total", "HTTP requests by method, route and status.", []string{"method", "path", "status"}),
		httpDuration: measure.NewHistogram("http_request_duration_seconds", "HTTP request latency.", measure.WithBuckets(measure.APIBuckets())),
		panics:       measure.NewCounter("panics_total", "HTTP handler panics recovered."),
		configMode:   measure.NewLabeledGauge("config_mode", "1 for the configuration's current mode, 0 for the others.", []string{"mode"}),
		alertsActive: measure.NewLabeledGauge("alerts_active", "1 for each active alert, by its kind and id.", []string{"kind", "id"}),
	}
	buildInfo := measure.NewLabeledGauge("build_info", "Always 1, labelled with the running version.", []string{"version"})
	m.registry.MustRegister(m.httpRequests, m.httpDuration, m.panics, m.configMode, m.alertsActive, buildInfo)
	buildInfo.Set(1, buildinfo.Version())
	for _, mode := range configModes {
		m.configMode.Set(0, mode)
	}
	return m
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

// Gather reads every registered family, a declared family with no series
// yet included, as measure's Registry.Gather does.
func (m *Metrics) Gather(ctx context.Context) ([]measure.Family, error) {
	return m.registry.Gather(ctx)
}

// Handler serves the exposition.
func (m *Metrics) Handler() http.HandlerFunc { return m.registry.Handler() }

// RecordHTTP is httpserver.WithRecordRouteMetric's hook: the library bounds the
// method and route labels.
func (m *Metrics) RecordHTTP(r httpserver.RequestMetric) {
	measure.RecordHTTP(m.httpRequests, m.httpDuration, r.Latency, r.Method, r.Path, strconv.Itoa(r.Status))
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

// SetAlertActive sets the alerts_active series of one active alert.
func (m *Metrics) SetAlertActive(kind, id string) { m.alertsActive.Set(1, kind, id) }

// DeleteAlertActive removes a cleared alert's alerts_active series.
func (m *Metrics) DeleteAlertActive(kind, id string) { m.alertsActive.Delete(kind, id) }
