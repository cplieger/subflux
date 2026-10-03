package server

import (
	"fmt"

	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/subflux"
)

// ProviderStatusSource answers one provider's full merged status and whether
// provider timeouts are enabled.
type ProviderStatusSource func(id subflux.ProviderID) (status subflux.ProviderStatus, timeoutsEnabled bool)

// providerStatusFor reads id's status from the live engine at call time, so a
// hook still firing from an outgoing engine publishes the live state. With no
// engine (unconfigured mode) it answers the zero status.
func (s *Server) providerStatusFor(id subflux.ProviderID) (subflux.ProviderStatus, bool) {
	engine := s.state().engine
	if engine == nil {
		return subflux.ProviderStatus{}, false
	}
	all, enabled := engine.ProviderStatus()
	return all[id], enabled
}

// NewProviderPublisher returns the only builder of provider SSE events. Every
// event carries the provider's full status as status reports it at publish
// time, because the client replaces its record with each event's status.
func NewProviderPublisher(bus *events.EventBus, status ProviderStatusSource) func(op events.ProviderOp, id subflux.ProviderID) {
	return func(op events.ProviderOp, id subflux.ProviderID) {
		st, enabled := status(id)
		bus.PublishProvider(&events.ProviderEvent{
			Op:              op,
			TimeoutsEnabled: enabled,
			Entry:           &events.ProviderTimeoutEntry{Provider: id, Status: st},
		})
	}
}

// NewProviderGateHook turns credential gate events into operator alerts and
// provider events. label names a provider the way the settings dialog does.
func NewProviderGateHook(alerts *activity.AlertLog, label func(subflux.ProviderID) string,
	publish func(op events.ProviderOp, id subflux.ProviderID),
) func(providergate.Event) {
	return func(ev providergate.Event) {
		source := "provider:" + string(ev.Provider)
		switch ev.Kind {
		case providergate.Disabled:
			alerts.RecordPersistent(source, fmt.Sprintf(
				"%s rejected its credentials and was disabled after %d attempts (%s). "+
					"Fix the credentials in Settings, then Providers and save, or press Test.",
				label(ev.Provider), providergate.MaxAuthFailures, ev.Reason,
			))
			publish(events.ProviderRaise, ev.Provider)
		case providergate.Enabled, providergate.Inactive:
			alerts.DismissBySource(source)
			publish(events.ProviderClear, ev.Provider)
		case providergate.SettingRejected:
			alerts.RecordPersistent(source+":"+ev.Setting, settingRejectedText(label(ev.Provider), ev.Setting))
			publish(events.ProviderRaise, ev.Provider)
		case providergate.SettingCleared:
			alerts.DismissBySource(source + ":" + ev.Setting)
			publish(events.ProviderClear, ev.Provider)
		case providergate.Paused:
			publish(events.ProviderRaise, ev.Provider)
		}
	}
}

func settingRejectedText(label, setting string) string {
	if setting == "anidb_client_key" {
		return fmt.Sprintf("%s's AniDB client key was rejected; episode lookup is off and %s searches by title. "+
			"Fix or clear the key in Settings, then Providers.", label, label)
	}
	return fmt.Sprintf("%s rejected its %s setting. Fix or clear it in Settings, then Providers.", label, setting)
}

// providerLabel names id by its registry label, falling back to the id.
func (s *Server) providerLabel(id subflux.ProviderID) string {
	if label, _ := s.registry.Schema(id); label != "" {
		return label
	}
	return string(id)
}
