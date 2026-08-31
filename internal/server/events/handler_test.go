package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/webhttp/v2/sse"
)

// sseFrame is one parsed wire frame: the id/event fields (empty when the
// frame omitted them) and the concatenated data payload.
type sseFrame struct {
	id    string
	event string
	data  string
}

// parseFrames splits a recorded SSE body into frames. Comment-only frames
// (keepalives) are skipped.
func parseFrames(body string) []sseFrame {
	var out []sseFrame
	for block := range strings.SplitSeq(body, "\n\n") {
		var f sseFrame
		seen := false
		for line := range strings.SplitSeq(block, "\n") {
			switch {
			case strings.HasPrefix(line, "id: "):
				f.id = strings.TrimPrefix(line, "id: ")
				seen = true
			case strings.HasPrefix(line, "event: "):
				f.event = strings.TrimPrefix(line, "event: ")
				seen = true
			case strings.HasPrefix(line, "data: "):
				f.data += strings.TrimPrefix(line, "data: ")
				seen = true
			}
		}
		if seen {
			out = append(out, f)
		}
	}
	return out
}

// handleOnce drives Handle with a pre-cancelled request context, so Serve
// writes the replay and the epoch handshake synchronously and then exits its
// live loop without delivering live frames. It returns the request it built
// (so callers can assert Handle left it unchanged) and the parsed frames.
func handleOnce(t *testing.T, bus *EventBus, target string, header map[string]string) (*http.Request, []sseFrame) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, target, nil).WithContext(ctx)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	Handle(bus, rec, req)
	return req, parseFrames(rec.Body.String())
}

// epochEnvelope is the wire envelope the epoch frame carries — the same
// {type,data} shape Publish uses.
type epochEnvelope struct {
	Type string     `json:"type"`
	Data EpochEvent `json:"data"`
}

// epochOf finds the single epoch frame, decodes its envelope, and fails the
// test when there is not exactly one.
func epochOf(t *testing.T, frames []sseFrame) (sseFrame, EpochEvent) {
	t.Helper()
	var found []sseFrame
	for _, f := range frames {
		if f.event == "epoch" {
			found = append(found, f)
		}
	}
	if len(found) != 1 {
		t.Fatalf("epoch frames = %d, want exactly 1; frames: %+v", len(found), frames)
	}
	var env epochEnvelope
	if err := json.Unmarshal([]byte(found[0].data), &env); err != nil {
		t.Fatalf("epoch payload %q: %v", found[0].data, err)
	}
	if env.Type != "epoch" {
		t.Fatalf("epoch envelope type = %q, want %q (the {type,data} shape Publish uses)", env.Type, "epoch")
	}
	return found[0], env.Data
}

// replayedIDs returns the ids of the non-epoch frames, in wire order.
func replayedIDs(frames []sseFrame) []string {
	var ids []string
	for _, f := range frames {
		if f.event != "epoch" {
			ids = append(ids, f.id)
		}
	}
	return ids
}

func publishN(bus *EventBus, n int) {
	for range n {
		bus.Publish(Event{Type: Notify, Data: NotifyEvent{Level: NotifyInfo, Text: "x"}})
	}
}

func TestHandleEpochShape(t *testing.T) {
	t.Parallel()
	bus := New(0)
	publishN(bus, 3)

	_, frames := handleOnce(t, bus, "/api/events", nil)

	f, ep := epochOf(t, frames)
	if f.id != "" {
		t.Errorf("epoch frame carries id %q, want none (must never become a cursor)", f.id)
	}
	if ep.BootID != bus.bootID {
		t.Errorf("epoch boot_id = %q, want the bus boot id %q", ep.BootID, bus.bootID)
	}
	if ep.Gap {
		t.Error("epoch gap = true on a cursor-less connect, want false")
	}
	if ep.Head != 3 {
		t.Errorf("epoch head = %d, want 3", ep.Head)
	}
	if got := replayedIDs(frames); len(got) != 0 {
		t.Errorf("cursor-less connect replayed %v, want no replay", got)
	}
}

func TestHandleReplayFromMidRing(t *testing.T) {
	t.Parallel()
	bus := New(0)
	publishN(bus, 10)

	_, frames := handleOnce(t, bus, "/api/events", map[string]string{"Last-Event-ID": "5"})

	_, ep := epochOf(t, frames)
	if ep.Gap {
		t.Error("gap = true for an in-ring cursor, want false")
	}
	want := []string{"6", "7", "8", "9", "10"}
	got := replayedIDs(frames)
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("replayed ids = %v, want %v", got, want)
	}
	// The ReplayBounds pin: the epoch head covers every replayed id.
	if ep.Head < 10 {
		t.Errorf("epoch head = %d, want >= 10 (>= every replayed id)", ep.Head)
	}
	// Replay precedes the epoch on the wire.
	if frames[len(frames)-1].event != "epoch" {
		t.Errorf("epoch not last of the synchronous frames: %+v", frames)
	}
}

func TestHandleDerivedRequestInjection(t *testing.T) {
	t.Parallel()
	bus := New(0)
	publishN(bus, 10)

	req, frames := handleOnce(t, bus, "/api/events?last_id=7", nil)

	_, ep := epochOf(t, frames)
	if ep.Gap {
		t.Error("gap = true for a covered derived cursor, want false")
	}
	want := []string{"8", "9", "10"}
	if got := replayedIDs(frames); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("derived cursor replayed %v, want %v", got, want)
	}
	// The injection happens on a clone: the caller's request is unchanged.
	if h := req.Header.Get("Last-Event-ID"); h != "" {
		t.Errorf("original request header mutated to %q, want untouched", h)
	}
}

func TestHandleHeaderWinsOverQuery(t *testing.T) {
	t.Parallel()
	bus := New(0)
	publishN(bus, 10)

	// A native EventSource retry carries the header; a stale synthetic
	// cursor may still sit in the recreate URL. The header wins.
	_, frames := handleOnce(t, bus, "/api/events?last_id=2",
		map[string]string{"Last-Event-ID": "8"})

	want := []string{"9", "10"}
	if got := replayedIDs(frames); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("replayed %v, want %v (header cursor 8, not query cursor 2)", got, want)
	}
}

func TestHandleCursorValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		target string
		header map[string]string
	}{
		{"query zero", "/api/events?last_id=0", nil},
		{"query invalid", "/api/events?last_id=abc", nil},
		{"query negative", "/api/events?last_id=-3", nil},
		{"query over 2^53-1", "/api/events?last_id=9007199254740992", nil},
		{"query absent", "/api/events", nil},
		{"header invalid", "/api/events", map[string]string{"Last-Event-ID": "junk"}},
		{"header over 2^53-1", "/api/events", map[string]string{"Last-Event-ID": "9007199254740992"}},
	}
	for _, tc := range cases {
		t.Run(strings.ReplaceAll(tc.name, " ", "_"), func(t *testing.T) {
			t.Parallel()
			bus := New(0)
			publishN(bus, 5)
			_, frames := handleOnce(t, bus, tc.target, tc.header)
			_, ep := epochOf(t, frames)
			if ep.Gap {
				t.Error("gap = true, want false (an ignored cursor is no resume, not a gap)")
			}
			if got := replayedIDs(frames); len(got) != 0 {
				t.Errorf("ignored cursor replayed %v, want none", got)
			}
		})
	}
}

func TestHandleGapTruthTable(t *testing.T) {
	t.Parallel()
	// Fill past the ring so the floor moves: 1030 events in a 1024 ring
	// leaves floor = 7, head = 1030.
	filled := func(t *testing.T) *EventBus {
		t.Helper()
		bus := New(0)
		publishN(bus, SSERing+6)
		floor, head := bus.hub.Bounds()
		if floor != 7 || head != uint64(SSERing+6) {
			t.Fatalf("Bounds() = (%d, %d), want (7, %d)", floor, head, SSERing+6)
		}
		return bus
	}

	// At the production sizes the budget disjunct fires before the floor one
	// ever can (a full 1024-ring puts floor-1 exactly 1024 behind head), so
	// the floor-boundary rows isolate their disjunct on a small test ring.
	// In-package struct literal: no production constructor changes.
	smallRing := func(t *testing.T) *EventBus {
		t.Helper()
		bus := &EventBus{hub: sse.NewHub(sse.WithReplay(8)), bootID: newBootID()}
		publishN(bus, 12) // floor = 5, head = 12
		floor, head := bus.hub.Bounds()
		if floor != 5 || head != 12 {
			t.Fatalf("Bounds() = (%d, %d), want (5, 12)", floor, head)
		}
		return bus
	}

	t.Run("floor_minus_1_covered", func(t *testing.T) {
		t.Parallel()
		bus := smallRing(t)
		// lastID 4: the next id needed is 5 == floor, so the ring covers it.
		_, frames := handleOnce(t, bus, "/api/events", map[string]string{"Last-Event-ID": "4"})
		_, ep := epochOf(t, frames)
		if ep.Gap {
			t.Error("gap = true at floor-1, want covered")
		}
		if got := replayedIDs(frames); len(got) != 8 {
			t.Errorf("replayed %d frames, want the whole ring (8)", len(got))
		}
	})

	t.Run("below_floor_gap", func(t *testing.T) {
		t.Parallel()
		bus := smallRing(t)
		_, frames := handleOnce(t, bus, "/api/events", map[string]string{"Last-Event-ID": "3"})
		_, ep := epochOf(t, frames)
		if !ep.Gap {
			t.Error("gap = false below the floor, want gap")
		}
		if got := replayedIDs(frames); len(got) != 0 {
			t.Errorf("gap verdict replayed %v, want the strip (no frames)", got)
		}
	})

	t.Run("above_head_gap", func(t *testing.T) {
		t.Parallel()
		bus := New(0)
		publishN(bus, 5)
		_, frames := handleOnce(t, bus, "/api/events", map[string]string{"Last-Event-ID": "6"})
		_, ep := epochOf(t, frames)
		if !ep.Gap {
			t.Error("gap = false above head, want gap")
		}
	})

	t.Run("empty_ring_gap", func(t *testing.T) {
		t.Parallel()
		bus := New(0)
		_, frames := handleOnce(t, bus, "/api/events", map[string]string{"Last-Event-ID": "5"})
		_, ep := epochOf(t, frames)
		if !ep.Gap {
			t.Error("gap = false on an empty ring with a cursor, want gap")
		}
		if ep.Head != 0 {
			t.Errorf("head = %d, want 0", ep.Head)
		}
	})

	t.Run("budget_boundary", func(t *testing.T) {
		t.Parallel()
		bus := New(0)
		publishN(bus, 300)
		// head - lastID == 256: within budget, replays.
		_, frames := handleOnce(t, bus, "/api/events", map[string]string{"Last-Event-ID": "44"})
		_, ep := epochOf(t, frames)
		if ep.Gap {
			t.Error("gap = true at head-lastID == ReplayBudget, want covered")
		}
		if got := replayedIDs(frames); len(got) != ReplayBudget {
			t.Errorf("replayed %d, want %d", len(got), ReplayBudget)
		}
		// head - lastID == 257: past the budget, gap.
		_, frames = handleOnce(t, bus, "/api/events", map[string]string{"Last-Event-ID": "43"})
		_, ep = epochOf(t, frames)
		if !ep.Gap {
			t.Error("gap = false at head-lastID == ReplayBudget+1, want gap")
		}
	})

	t.Run("header_carried_pre_gap_stripped", func(t *testing.T) {
		t.Parallel()
		bus := filled(t)
		// A native retry header below the floor: the strip follows the
		// verdict whatever the cursor's source — no replayed frame reaches
		// the wire before the gap epoch.
		_, frames := handleOnce(t, bus, "/api/events", map[string]string{"Last-Event-ID": "3"})
		f, ep := epochOf(t, frames)
		if !ep.Gap {
			t.Error("gap = false, want gap")
		}
		if got := replayedIDs(frames); len(got) != 0 {
			t.Errorf("header-carried pre-gap replayed %v, want stripped", got)
		}
		if frames[0] != f {
			t.Errorf("first wire frame is %+v, want the epoch", frames[0])
		}
	})
}

// TestHandleEpochBeforeLive pins the ordering on a real stream: the epoch is
// written after replay and before any live frame.
func TestHandleEpochBeforeLive(t *testing.T) {
	bus := New(0)
	publishN(bus, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Handle(bus, w, r)
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	waitClients(t, bus, 1)
	bus.Publish(Event{Type: Notify, Data: NotifyEvent{Level: NotifyInfo, Text: "live"}})

	var order []string
	buf := make([]byte, 1)
	var body strings.Builder
	for !strings.Contains(body.String(), `"live"`) {
		if _, err := resp.Body.Read(buf); err != nil {
			t.Fatalf("stream ended early: %v; body: %q", err, body.String())
		}
		body.WriteString(string(buf))
	}
	for _, f := range parseFrames(body.String()) {
		order = append(order, f.event+":"+f.id)
	}
	want := []string{"notify:2", "epoch:", "notify:3"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Errorf("wire order = %v, want replay, then epoch, then live (%v)", order, want)
	}
}

// TestPublishIsTopicless pins that the bus publishes broadcasts: an empty
// Topic reaches every subscriber regardless of filter.
func TestPublishIsTopicless(t *testing.T) {
	t.Parallel()
	bus := New(0)
	publishN(bus, 1)
	for _, ev := range bus.hub.Buffered() {
		if ev.Event.Topic != "" {
			t.Errorf("published event carries topic %q, want topicless broadcast", ev.Event.Topic)
		}
	}
}

// TestRingCapacity pins WithReplay(SSERing) at events.New: the ring holds
// exactly SSERing events, so the floor moves once it fills.
func TestRingCapacity(t *testing.T) {
	t.Parallel()
	bus := New(0)
	publishN(bus, SSERing+1)
	floor, head := bus.hub.Bounds()
	if floor != 2 || head != uint64(SSERing+1) {
		t.Errorf("Bounds() after SSERing+1 publishes = (%d, %d), want (2, %d)", floor, head, SSERing+1)
	}
}

// TestBootIDStableAcrossConnections pins one boot id per bus (per process
// start): two connections see the same value, and a second bus differs.
func TestBootIDStableAcrossConnections(t *testing.T) {
	t.Parallel()
	bus := New(0)
	_, f1 := handleOnce(t, bus, "/api/events", nil)
	_, f2 := handleOnce(t, bus, "/api/events", nil)
	_, ep1 := epochOf(t, f1)
	_, ep2 := epochOf(t, f2)
	if ep1.BootID != ep2.BootID {
		t.Errorf("boot ids differ across connections: %q vs %q", ep1.BootID, ep2.BootID)
	}
	if ep1.BootID == "" {
		t.Error("boot id empty")
	}
	_, f3 := handleOnce(t, New(0), "/api/events", nil)
	_, ep3 := epochOf(t, f3)
	if ep3.BootID == ep1.BootID {
		t.Errorf("two buses share boot id %q, want distinct per process start", ep1.BootID)
	}
}
