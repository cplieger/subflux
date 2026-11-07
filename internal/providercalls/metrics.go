package providercalls

import (
	"context"
	"errors"

	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/status"
)

var healthNames = [...]string{status.OK: "ok", status.Degraded: "degraded", status.Disabled: "disabled"}

type funnelMetrics struct {
	calls   *measure.LabeledCounter
	seconds *measure.LabeledHistogram
	health  *measure.LabeledGauge
}

func newFunnelMetrics() *funnelMetrics {
	const id, op = "provider", "op"
	return &funnelMetrics{
		calls:   measure.NewLabeledCounter("provider_calls_total", "Provider calls by op and result.", []string{id, op, "result"}),
		seconds: measure.NewLabeledHistogram("provider_call_duration_seconds", "Time a provider call ran, after admission.", []string{id, op}, measure.WithBuckets(measure.APIBuckets())),
		health:  measure.NewLabeledGauge("provider_health", "1 for the provider's current health, 0 for the others.", []string{id, "health"}),
	}
}

// Collectors are the funnel's series, for app to register through obs.
func (f *Funnel) Collectors() []measure.Metric {
	return []measure.Metric{f.metrics.calls, f.metrics.seconds, f.metrics.health}
}

func (m *funnelMetrics) count(id provider.ProviderID, op Op, err error) {
	m.calls.Inc(string(id), op.String(), result(err))
}

func (m *funnelMetrics) setHealth(id provider.ProviderID, h status.Health) {
	for i := status.OK; i <= status.Disabled; i++ {
		v := 0.0
		if i == h {
			v = 1
		}
		m.health.Set(v, string(id), healthNames[i])
	}
}

// result is a call's bounded result label.
func result(err error) string {
	var (
		fail    *provider.Failure
		skipped *provider.Skipped
		backoff *BackoffError
	)
	switch {
	case err == nil:
		return "ok"
	case errors.As(err, &fail):
		return fail.Kind.String()
	case errors.As(err, &skipped):
		return "skipped"
	case errors.As(err, &backoff):
		return "backoff"
	case errors.Is(err, ErrCredentialDisabled):
		return "disabled"
	case errors.Is(err, ErrReleased):
		return "released"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	}
	return "error"
}
