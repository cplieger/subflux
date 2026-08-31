package events

import (
	"strings"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/subflux"
)

// The three status deltas ride the same {type,data} envelope Publish always
// uses, as named SSE events — the shape the generated EventData decoders
// dispatch on (DiscriminatorMap: "activity"/"alert"/"provider").

func TestWireFormat_activity_delta(t *testing.T) {
	bus := New(0)
	st := startStream(t, bus)
	readUntil(t, st.sc, func(l string) bool { return strings.Contains(l, `"type":"epoch"`) })
	waitClients(t, bus, 1)

	entry := activity.Entry{ID: "7", Action: "Full Scan", Source: activity.SourceManual, Done: true}
	bus.Publish(Event{Type: ActivityDelta, Data: ActivityEvent{Op: ActivityUpsert, Entry: &entry}})

	lines := readUntil(t, st.sc, func(l string) bool { return strings.HasPrefix(l, "data: ") })
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "event: activity") {
		t.Errorf("missing named event field: %v", lines)
	}
	for _, want := range []string{`"type":"activity"`, `"op":"upsert"`, `"id":"7"`, `"done":true`} {
		if !strings.Contains(joined, want) {
			t.Errorf("activity payload missing %s: %v", want, lines)
		}
	}
}

func TestWireFormat_alert_delta(t *testing.T) {
	bus := New(0)
	st := startStream(t, bus)
	readUntil(t, st.sc, func(l string) bool { return strings.Contains(l, `"type":"epoch"`) })
	waitClients(t, bus, 1)

	bus.PublishAlert(AlertRaise, &activity.Alert{
		ID: 3, Level: activity.LevelError, Source: "scan",
		Message: "boom", Kind: activity.AlertTransient, Time: time.Now(),
	})

	lines := readUntil(t, st.sc, func(l string) bool { return strings.HasPrefix(l, "data: ") })
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "event: alert") {
		t.Errorf("missing named event field: %v", lines)
	}
	for _, want := range []string{`"type":"alert"`, `"op":"raise"`, `"id":3`, `"message":"boom"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("alert payload missing %s: %v", want, lines)
		}
	}
}

func TestWireFormat_provider_delta(t *testing.T) {
	bus := New(0)
	st := startStream(t, bus)
	readUntil(t, st.sc, func(l string) bool { return strings.Contains(l, `"type":"epoch"`) })
	waitClients(t, bus, 1)

	bus.PublishProvider(ProviderRaise, &ProviderTimeoutEntry{
		Provider: "opensubtitles",
		Status: subflux.ProviderStatus{
			TimedOut: true, CooldownRemaining: time.Hour, RecentFailures: 5, Threshold: 5,
		},
	})

	lines := readUntil(t, st.sc, func(l string) bool { return strings.HasPrefix(l, "data: ") })
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "event: provider") {
		t.Errorf("missing named event field: %v", lines)
	}
	for _, want := range []string{`"type":"provider"`, `"op":"raise"`, `"provider":"opensubtitles"`, `"timed_out":true`} {
		if !strings.Contains(joined, want) {
			t.Errorf("provider payload missing %s: %v", want, lines)
		}
	}
}
