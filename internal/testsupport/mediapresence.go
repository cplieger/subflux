package testsupport

import "github.com/cplieger/subflux/internal/mediapresence"

// NopPresenceMetrics is a mediapresence.Metrics that records nothing.
type NopPresenceMetrics struct{}

// SetMediaRootUnavailable records nothing.
func (NopPresenceMetrics) SetMediaRootUnavailable(string, bool) {}

// DeleteMediaRootUnavailable records nothing.
func (NopPresenceMetrics) DeleteMediaRootUnavailable(string) {}

// MediaPresence returns a checker bound to roots with no-op reporting.
func MediaPresence(roots ...string) *mediapresence.Checker {
	c, err := mediapresence.New(mediapresence.Config{Metrics: NopPresenceMetrics{}, Alerts: NopAlerts{}})
	if err != nil {
		panic("testsupport: " + err.Error())
	}
	c.Bind(roots)
	return c
}
