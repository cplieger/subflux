package events

import "testing"

func FuzzEventBusPublishNext(f *testing.F) {
	f.Add("coverage", "tmdb-123", "eng", "normal", "opensubtitles")
	f.Add("notify", "", "error", "connection timeout", "")
	f.Add("", "", "", "", "")

	f.Fuzz(func(t *testing.T, evType, field1, field2, field3, field4 string) {
		bus := New()
		sub, unsub := bus.Subscribe()
		defer unsub()

		var ev Event
		switch EventType(evType) {
		case CoverageUpdate:
			ev = Event{Type: CoverageUpdate, Data: CoverageEvent{
				MediaID: field1, Language: field2, Variant: field3, Source: field4,
			}}
		case Notify:
			ev = Event{Type: Notify, Data: NotifyEvent{
				Level: NotifyLevel(field2), Text: field3,
			}}
		default:
			ev = Event{Type: EventType(evType), Data: NotifyEvent{Text: field1}}
		}

		bus.Publish(ev)
		got, ok := sub.Next()
		if !ok {
			t.Fatal("expected event after Publish")
		}
		if got.Type != ev.Type {
			t.Errorf("type mismatch: got %q want %q", got.Type, ev.Type)
		}
	})
}
