package events

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// stream is one connected SSE client: the response status/headers, a line
// scanner over the body, and an explicit closer (the body is also closed via
// t.Cleanup, so calling close is optional and double-close is safe).
type stream struct {
	status int
	header http.Header
	sc     *bufio.Scanner
	close  func()
}

// startStream connects an SSE client to a HandleEvents server. The client cap
// lives on the bus itself (construct with New(cap)). The response body never
// escapes: it is owned here and closed via t.Cleanup and/or st.close.
func startStream(t *testing.T, bus *EventBus) stream {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		HandleEvents(bus, w, r)
	}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return stream{
		status: resp.StatusCode,
		header: resp.Header,
		sc:     bufio.NewScanner(resp.Body),
		close:  func() { resp.Body.Close() },
	}
}

// readUntil scans lines until pred matches, bounded to avoid hanging on a
// broken stream.
func readUntil(t *testing.T, sc *bufio.Scanner, pred func(string) bool) []string {
	t.Helper()
	var lines []string
	for range 100 {
		if !sc.Scan() {
			t.Fatalf("stream ended early; lines: %v", lines)
		}
		l := sc.Text()
		lines = append(lines, l)
		if pred(l) {
			return lines
		}
	}
	t.Fatalf("predicate never matched; lines: %v", lines)
	return nil
}

func waitClients(t *testing.T, bus *EventBus, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for bus.ClientCount() != want {
		if time.Now().After(deadline) {
			t.Fatalf("ClientCount = %d, want %d", bus.ClientCount(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestPublishNilBusIsNoop(t *testing.T) {
	t.Parallel()
	var eb *EventBus
	eb.Publish(Event{Type: Notify}) // must not panic
}

func TestPublishNoSubscribersDoesNotPanic(t *testing.T) {
	t.Parallel()
	New(0).Publish(Event{Type: Notify, Data: NotifyEvent{Level: NotifyInfo, Text: "x"}})
}

func TestClientCountZeroInitially(t *testing.T) {
	t.Parallel()
	if got := New(0).ClientCount(); got != 0 {
		t.Errorf("ClientCount = %d", got)
	}
}

// TestWireFormat pins the browser contract: a published event arrives as a
// NAMED SSE event whose data payload is the JSON-encoded Event (type +
// data), exactly what static-src/events.ts addEventListener handlers parse.
func TestWireFormat(t *testing.T) {
	bus := New(0)
	st := startStream(t, bus)
	readUntil(t, st.sc, func(l string) bool { return l == ": connected" })
	waitClients(t, bus, 1)

	bus.Publish(Event{Type: CoverageUpdate, Data: CoverageEvent{
		MediaType: "episode", MediaID: "tt1-s01e01", Language: "fr", Source: "auto",
	}})

	lines := readUntil(t, st.sc, func(l string) bool { return strings.HasPrefix(l, "data: ") })
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "event: coverage") {
		t.Errorf("missing named event field: %v", lines)
	}
	if !strings.Contains(joined, `"type":"coverage"`) || !strings.Contains(joined, `"media_id":"tt1-s01e01"`) {
		t.Errorf("payload not the JSON-encoded Event: %v", lines)
	}
}

func TestHandleEventsClientCap(t *testing.T) {
	bus := New(1)
	st := startStream(t, bus)
	readUntil(t, st.sc, func(l string) bool { return l == ": connected" })
	waitClients(t, bus, 1)

	st2 := startStream(t, bus)
	if st2.status != http.StatusServiceUnavailable {
		t.Errorf("second client status = %d, want 503", st2.status)
	}

	// Hot reload raises the cap on the live hub: the next client is admitted.
	bus.SetMaxClients(2)
	st3 := startStream(t, bus)
	readUntil(t, st3.sc, func(l string) bool { return l == ": connected" })
	waitClients(t, bus, 2)
}

func TestShutdownDrainsAndRefuses(t *testing.T) {
	bus := New(0)
	st := startStream(t, bus)
	readUntil(t, st.sc, func(l string) bool { return l == ": connected" })
	waitClients(t, bus, 1)

	bus.Shutdown()
	waitClients(t, bus, 0)
	st.close()

	st2 := startStream(t, bus)
	if st2.status != http.StatusServiceUnavailable {
		t.Errorf("post-shutdown client status = %d, want 503", st2.status)
	}

	var nilBus *EventBus
	nilBus.Shutdown()       // must not panic
	nilBus.SetMaxClients(5) // must not panic
}

func TestHandleEventsHeaders(t *testing.T) {
	bus := New(0)
	st := startStream(t, bus)
	if ct := st.header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := st.header.Get("Cache-Control"); !strings.Contains(cc, "no-transform") {
		t.Errorf("Cache-Control = %q, want no-transform (Caddy/nginx gzip defense)", cc)
	}
	readUntil(t, st.sc, func(l string) bool { return l == ": connected" })
}
