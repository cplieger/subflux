package events

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cplieger/slogx/capture"
	"github.com/cplieger/sse"
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

// startStream connects a v3 SSE client (SSE-Wire set, so no legacy epoch
// frame follows the hello) to a Handle server. The client cap lives on the
// bus itself (construct with New(cap, nil)). The response body never
// escapes: it is owned here and closed via t.Cleanup and/or st.close.
func startStream(t *testing.T, bus *EventBus) stream {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Handle(bus, w, r)
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("SSE-Wire", "1")
	resp, err := http.DefaultClient.Do(req)
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

// readHello consumes the stream through the hello's data line, so the next
// data: line a test reads is an application frame.
func readHello(t *testing.T, sc *bufio.Scanner) {
	t.Helper()
	readUntil(t, sc, func(l string) bool {
		return strings.HasPrefix(l, "data: ") && strings.Contains(l, `"verdict":`)
	})
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
	New(0, nil).Publish(Event{Type: Notify, Data: NotifyEvent{Level: NotifyInfo, Text: "x"}})
}

func TestClientCountZeroInitially(t *testing.T) {
	t.Parallel()
	if got := New(0, nil).ClientCount(); got != 0 {
		t.Errorf("ClientCount = %d", got)
	}
}

// TestWireFormat pins the browser contract: a published event arrives as a
// NAMED SSE event whose data payload is the JSON-encoded Event (type +
// data), exactly what static-src/events.ts addEventListener handlers parse.
func TestWireFormat(t *testing.T) {
	bus := New(0, nil)
	st := startStream(t, bus)
	readHello(t, st.sc)
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

func TestHandleClientCap(t *testing.T) {
	bus := New(1, nil)
	st := startStream(t, bus)
	readHello(t, st.sc)
	waitClients(t, bus, 1)

	st2 := startStream(t, bus)
	if st2.status != http.StatusServiceUnavailable {
		t.Errorf("second client status = %d, want 503", st2.status)
	}

	// Hot reload raises the cap on the live hub: the next client is admitted,
	// and the one after that is refused — the new cap is the value passed,
	// not an unlimited hub.
	bus.SetMaxClients(2)
	st3 := startStream(t, bus)
	readHello(t, st3.sc)
	waitClients(t, bus, 2)

	if st4 := startStream(t, bus); st4.status != http.StatusServiceUnavailable {
		t.Errorf("third client status = %d, want 503 (cap raised to 2, not lifted)", st4.status)
	}
}

func TestShutdownDrainsAndRefuses(t *testing.T) {
	bus := New(0, nil)
	st := startStream(t, bus)
	readHello(t, st.sc)
	waitClients(t, bus, 1)

	if err := bus.Shutdown(t.Context()); err != nil {
		t.Fatalf("Shutdown() with one reading client = %v, want nil", err)
	}
	waitClients(t, bus, 0)
	st.close()

	st2 := startStream(t, bus)
	if st2.status != http.StatusServiceUnavailable {
		t.Errorf("post-shutdown client status = %d, want 503", st2.status)
	}

	var nilBus *EventBus
	if err := nilBus.Shutdown(t.Context()); err != nil {
		t.Errorf("nil bus Shutdown() = %v, want nil", err)
	}
	nilBus.SetMaxClients(5) // must not panic
}

func TestHandleHeaders(t *testing.T) {
	bus := New(0, nil)
	st := startStream(t, bus)
	if ct := st.header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := st.header.Get("Cache-Control"); !strings.Contains(cc, "no-transform") {
		t.Errorf("Cache-Control = %q, want no-transform (Caddy/nginx gzip defense)", cc)
	}
	readHello(t, st.sc)
}

// TestPublish_oversize_frame_is_logged_not_published pins the one Publish
// refusal subflux can reach: a payload past sse.MaxFrameBytes consumes no
// offset and costs one Warn naming the event type. Serial: it swaps the
// default logger.
func TestPublish_oversize_frame_is_logged_not_published(t *testing.T) {
	sink := capture.Default(t)
	bus := New(0, nil)
	bus.Publish(Event{Type: Notify, Data: NotifyEvent{Level: NotifyInfo, Text: "small"}})
	before := bus.hub.Position().Head

	bus.Publish(Event{Type: Notify, Data: NotifyEvent{
		Level: NotifyInfo, Text: strings.Repeat("x", sse.MaxFrameBytes+1),
	}})

	if got := bus.hub.Position().Head; got != before {
		t.Errorf("Position().Head = %d after an oversize publish, want unchanged %d", got, before)
	}
	if n := sink.CountExact("SSE: event refused by the hub"); n != 1 {
		t.Errorf("refusal Warn lines = %d, want 1; messages: %v", n, sink.Messages())
	}
	if v, ok := sink.AttrValue("SSE: event refused by the hub", "type"); !ok || v != string(Notify) {
		t.Errorf("refusal Warn type attr = %q (present %v), want %q", v, ok, Notify)
	}
}
