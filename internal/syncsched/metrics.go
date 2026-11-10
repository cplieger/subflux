package syncsched

import (
	"context"
	"errors"
	"time"

	"github.com/cplieger/go/measure"
	"github.com/cplieger/subflux/internal/timing"
)

// origin is who asked for an alignment, the runs series' origin label.
type origin string

const (
	originSearch   origin = "search"
	originProposal origin = "proposal"
	originBatch    origin = "batch"
)

// stage is where an alignment failed, the failures series' label.
type stage string

const (
	stageOpen    stage = "open"
	stageProbe   stage = "probe"
	stageExtract stage = "extract"
	stagePCM     stage = "pcm"
	stagePanic   stage = "panic"
)

type metrics struct {
	runs     *measure.LabeledCounter
	failures *measure.LabeledCounter
	wait     *measure.Histogram
	seconds  *measure.LabeledHistogram
	depth    *measure.LabeledGauge
}

func newMetrics() *metrics {
	return &metrics{
		runs:     measure.NewLabeledCounter("sync_runs_total", "Alignments by origin, the method that settled them and their verdict.", []string{"origin", "method", "verdict"}),
		failures: measure.NewLabeledCounter("sync_failures_total", "Alignment stage failures by stage.", []string{"stage"}),
		wait:     measure.NewHistogram("sync_slot_wait_seconds", "Time an alignment waited for the slot."),
		seconds:  measure.NewLabeledHistogram("sync_run_seconds", "Time an alignment held the slot, by the method that settled it.", []string{"method"}),
		depth:    measure.NewLabeledGauge("sync_queue_depth", "Alignments waiting for the slot, by class.", []string{"class"}),
	}
}

// Collectors are the sync series, for app to register through obs.
func (a *Aligner) Collectors() []measure.Metric {
	return []measure.Metric{a.m.runs, a.m.failures, a.m.wait, a.m.seconds, a.m.depth}
}

var methodLabels = [...]string{timing.MethodNone: "none", timing.MethodReference: "reference", timing.Audio: "audio"}

// ran counts one alignment that held the slot for took.
func (m *metrics) ran(o origin, r *result, err error, took time.Duration) {
	method := methodLabels[r.fit.Method]
	m.runs.Inc(string(o), method, verdictLabel(r, err))
	m.seconds.Observe(took.Seconds(), method)
}

func verdictLabel(r *result, err error) string {
	switch {
	case errors.Is(err, ErrDifferentCut):
		return "different_cut"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "canceled"
	case err != nil, r.failure != nil:
		return "failed"
	case r.verdict == timing.Verified:
		return "verified"
	}
	return "unverified"
}
