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
	"time"

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
	authDropped  *measure.Counter
	backup       backupSeries
}

// backupSeries are the backup runner's schedule, last success and failures.
type backupSeries struct {
	enabled     *measure.Gauge
	interval    *measure.Gauge
	lastSuccess *measure.Gauge
	failures    *measure.Counter
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
		authDropped:  measure.NewCounter("auth_events_dropped_total", "Sign-in audit events dropped because the audit log queue was full."),
		backup: backupSeries{
			enabled:     measure.NewGauge("backup_enabled", "1 while scheduled backups are on, else 0."),
			interval:    measure.NewGauge("backup_interval_seconds", "The time between scheduled backups."),
			lastSuccess: measure.NewGauge("backup_last_success_timestamp_seconds", "When the newest backup was made, 0 before any."),
			failures:    measure.NewCounter("backup_failures_total", "Scheduled backup runs that failed."),
		},
	}
	buildInfo := measure.NewLabeledGauge("build_info", "Always 1, labelled with the running version.", []string{"version"})
	m.registry.MustRegister(m.httpRequests, m.httpDuration, m.panics, m.configMode, m.alertsActive, m.authDropped,
		m.backup.enabled, m.backup.interval, m.backup.lastSuccess, m.backup.failures, buildInfo)
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

// IncAuthEventsDropped counts one sign-in audit event the full audit queue
// dropped.
func (m *Metrics) IncAuthEventsDropped() { m.authDropped.Inc() }

// SetBackupSchedule sets backup_enabled and backup_interval_seconds.
func (m *Metrics) SetBackupSchedule(enabled bool, interval time.Duration) {
	v := 0.0
	if enabled {
		v = 1
	}
	m.backup.enabled.Set(v)
	m.backup.interval.Set(interval.Seconds())
}

// SetBackupLastSuccess sets backup_last_success_timestamp_seconds to at, 0
// for the zero time.
func (m *Metrics) SetBackupLastSuccess(at time.Time) {
	v := 0.0
	if !at.IsZero() {
		v = float64(at.Unix())
	}
	m.backup.lastSuccess.Set(v)
}

// IncBackupFailure counts one failed backup run.
func (m *Metrics) IncBackupFailure() { m.backup.failures.Inc() }
