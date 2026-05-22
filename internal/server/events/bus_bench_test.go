package events

import "testing"

func BenchmarkPublish(b *testing.B) {
	eb := New()
	sub, unsub := eb.Subscribe()
	defer unsub()

	evt := Event{Type: "test", Data: CoverageEvent{}}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		eb.Publish(evt)
		// Drain to prevent buffer overflow.
		sub.Next()
	}
}

func BenchmarkPublish_10subscribers(b *testing.B) {
	eb := New()
	subs := make([]*Subscription, 10)
	unsubs := make([]func(), 10)
	for i := range 10 {
		subs[i], unsubs[i] = eb.Subscribe()
	}
	defer func() {
		for _, u := range unsubs {
			u()
		}
	}()

	evt := Event{Type: "coverage", Data: CoverageEvent{}}

	b.ResetTimer()
	b.ReportAllocs()
	for range b.N {
		eb.Publish(evt)
		for _, s := range subs {
			s.Next()
		}
	}
}
