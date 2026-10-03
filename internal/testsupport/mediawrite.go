package testsupport

import "github.com/cplieger/subflux/internal/mediawrite"

// NopMediaMetrics is a mediawrite.Metrics that records nothing.
type NopMediaMetrics struct{}

// SetMediaRootUnwritable records nothing.
func (NopMediaMetrics) SetMediaRootUnwritable(string, bool) {}

// DeleteMediaRoot records nothing.
func (NopMediaMetrics) DeleteMediaRoot(string) {}

// IncSubtitleWriteError records nothing.
func (NopMediaMetrics) IncSubtitleWriteError() {}

// NopAlerts is a mediawrite.Alerts that holds no alert.
type NopAlerts struct{}

// RecordPersistent records nothing.
func (NopAlerts) RecordPersistent(string, string) {}

// DismissBySource records nothing.
func (NopAlerts) DismissBySource(string) {}

// HasUndismissed reports false.
func (NopAlerts) HasUndismissed(string) bool { return false }

// MediaWriter returns an unbound writer with the default atomicfile write
// and no-op reporting: saves land on disk, a folder fault blocks its folder,
// and Preflight probes nothing until a Bind.
func MediaWriter() *mediawrite.Writer {
	mw, err := mediawrite.New(mediawrite.Config{Metrics: NopMediaMetrics{}, Alerts: NopAlerts{}})
	if err != nil {
		panic("testsupport: " + err.Error())
	}
	return mw
}
