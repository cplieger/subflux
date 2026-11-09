package scan

import (
	"time"

	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/media"
	"github.com/cplieger/subflux/internal/mediasource"
)

// metrics are a Scanner's series (design 5.1). Their names carry no
// subflux_ prefix: obs's registry adds it.
type metrics struct {
	lastComplete  *measure.LabeledGauge
	wholeInterval *measure.Gauge
}

func newMetrics() metrics {
	return metrics{
		lastComplete: measure.NewLabeledGauge("scan_last_complete_timestamp_seconds",
			"End of the source's latest complete whole pass, or its loop's start before one.", []string{"source"}),
		wholeInterval: measure.NewGauge("scan_whole_interval_seconds", "The scan section's whole_interval."),
	}
}

// Collectors declares scan_last_complete_timestamp_seconds{source}, the end
// of each source's latest complete whole pass, seeded with its loop's start
// and deleted when Reload removes the source, and
// scan_whole_interval_seconds; app registers them through obs.
func (s *Scanner) Collectors() []measure.Metric {
	return []measure.Metric{s.metrics.lastComplete, s.metrics.wholeInterval}
}

// mark sets src's latest complete pass, or its loop's start, to at.
func (m metrics) mark(src media.SourceID, at time.Time) {
	m.lastComplete.Set(float64(at.UnixMilli())/1e3, string(src))
}

func (m metrics) forget(src media.SourceID) { m.lastComplete.Delete(string(src)) }

func (m metrics) interval(sched mediasource.Schedule) { m.wholeInterval.Set(sched.Whole.Seconds()) }
