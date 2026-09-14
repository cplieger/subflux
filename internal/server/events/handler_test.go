package events

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cplieger/sse"
)

// sseFrame is one parsed wire frame: the id/event fields (empty when the
// frame omitted them) and the concatenated data payload.
type sseFrame struct {
	id    string
	event string
	data  string
}

// parseFrames splits a recorded SSE body into frames. Blocks with no id,
// event or data field (the retry: line) are skipped.
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
// writes the hello, the replay and the OnConnect frames synchronously and
// then exits its live loop without delivering live frames.
func handleOnce(t *testing.T, bus *EventBus, header map[string]string) []sseFrame {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	Handle(bus, rec, req)
	return parseFrames(rec.Body.String())
}

// epochEnvelope is the wire envelope the epoch frame carries — the same
// {type,data} shape Publish uses.
type epochEnvelope struct {
	Type string     `json:"type"`
	Data EpochEvent `json:"data"`
}

// epochFrames returns the epoch frames in wire order.
func epochFrames(frames []sseFrame) []sseFrame {
	var found []sseFrame
	for _, f := range frames {
		if f.event == string(Epoch) {
			found = append(found, f)
		}
	}
	return found
}

// epochOf finds the single epoch frame, decodes its envelope, and fails the
// test when there is not exactly one.
func epochOf(t *testing.T, frames []sseFrame) (sseFrame, EpochEvent) {
	t.Helper()
	found := epochFrames(frames)
	if len(found) != 1 {
		t.Fatalf("epoch frames = %d, want exactly 1; frames: %+v", len(found), frames)
	}
	var env epochEnvelope
	if err := json.Unmarshal([]byte(found[0].data), &env); err != nil {
		t.Fatalf("epoch payload %q: %v", found[0].data, err)
	}
	if env.Type != string(Epoch) {
		t.Fatalf("epoch envelope type = %q, want %q (the {type,data} shape Publish uses)", env.Type, Epoch)
	}
	return found[0], env.Data
}

// helloOf finds the single sse:hello frame and decodes it.
func helloOf(t *testing.T, frames []sseFrame) sse.Hello {
	t.Helper()
	var found []sseFrame
	for _, f := range frames {
		if f.event == "sse:hello" {
			found = append(found, f)
		}
	}
	if len(found) != 1 {
		t.Fatalf("sse:hello frames = %d, want exactly 1; frames: %+v", len(found), frames)
	}
	var h sse.Hello
	if err := json.Unmarshal([]byte(found[0].data), &h); err != nil {
		t.Fatalf("hello payload %q: %v", found[0].data, err)
	}
	return h
}

// replayedIDs returns the ids of the application frames (neither the hello
// nor the epoch), in wire order.
func replayedIDs(frames []sseFrame) []string {
	var ids []string
	for _, f := range frames {
		if f.event != string(Epoch) && f.event != "sse:hello" {
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

func cursor(bus *EventBus, offset uint64) string {
	return sse.Cursor{Epoch: hubEpoch(bus), Offset: offset}.String()
}

// hubEpoch reads the epoch the way production does, off a stamp.
func hubEpoch(bus *EventBus) string {
	st, _ := bus.Versions().Stamp(SubjectActivity, "")
	return st.Epoch
}

func TestHandle_legacy_connect_writes_epoch_frame(t *testing.T) {
	t.Parallel()
	bus := New(0, nil)
	publishN(bus, 3)

	frames := handleOnce(t, bus, nil)

	f, ep := epochOf(t, frames)
	if f.id != "" {
		t.Errorf("epoch frame carries id %q, want none (must never become a cursor)", f.id)
	}
	if ep.BootID != hubEpoch(bus) {
		t.Errorf("epoch boot_id = %q, want the hub epoch %q", ep.BootID, hubEpoch(bus))
	}
	// A legacy tab only ever reaches this server on a reconnect (a fresh page
	// load fetches the v3 bundle), and its own reconnect path carries no
	// header cursor, so "fresh" means "missed an unknown number of events":
	// gap true is the honest verdict and the tab runs its transaction.
	if !ep.Gap {
		t.Error("epoch gap = false on a cursor-less connect, want true (fresh is not resumed)")
	}
	if ep.Head != 3 {
		t.Errorf("epoch head = %d, want 3", ep.Head)
	}
	if !strings.Contains(f.data, `"head":3`) {
		t.Errorf("epoch payload %q carries head as something other than a JSON number", f.data)
	}
	if got := replayedIDs(frames); len(got) != 0 {
		t.Errorf("cursor-less connect replayed %v, want no replay", got)
	}
}

func TestHandle_v3_connect_writes_no_epoch_frame(t *testing.T) {
	t.Parallel()
	bus := New(0, nil)
	publishN(bus, 3)

	frames := handleOnce(t, bus, map[string]string{"SSE-Wire": "1"})

	if got := epochFrames(frames); len(got) != 0 {
		t.Errorf("SSE-Wire connect received %d epoch frame(s), want none: %+v", len(got), got)
	}
	h := helloOf(t, frames)
	if h.Verdict != sse.VerdictFresh || h.Resumed {
		t.Errorf("hello = %+v, want verdict fresh and resumed false", h)
	}
	if h.Epoch != hubEpoch(bus) || h.Head != 3 {
		t.Errorf("hello epoch/head = %q/%d, want %q/3", h.Epoch, h.Head, hubEpoch(bus))
	}
}

// TestHandleAdvertisesReconnectDelayOnce pins that the hub is CONSTRUCTED with
// the reconnect hint. Without the option the browser picks its own delay and
// the two disagree (Chrome 3s, Firefox 5s), which nothing else in the suite
// would notice: the field is not a frame, so parseFrames skips it.
func TestHandleAdvertisesReconnectDelayOnce(t *testing.T) {
	t.Parallel()
	bus := New(0, nil)
	publishN(bus, 2)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "/api/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	Handle(bus, rec, req)
	body := rec.Body.String()

	want := fmt.Sprintf("retry: %d\n\n", SSEReconnectDelay.Milliseconds())
	if n := strings.Count(body, "retry: "); n != 1 {
		t.Fatalf("body carries %d retry: lines, want exactly 1 (it is a property of the connection, not of a frame); body = %q", n, body)
	}
	if !strings.HasPrefix(body, want) {
		t.Errorf("body does not open with %q; the delay must be in effect before the first drop, so it precedes the hello. body = %q", want, body)
	}
}

// TestHandle_options_reach_the_hub pins that ReplyMaxEvents is passed to the
// hub: one offset inside the cap resumes with exactly that many frames, one
// past it is gap_budget with no replay. The arithmetic is the library's; the
// option reaching it is subflux's.
func TestHandle_options_reach_the_hub(t *testing.T) {
	t.Parallel()
	bus := New(0, nil)
	publishN(bus, 300)

	frames := handleOnce(t, bus, map[string]string{"SSE-Wire": "1", "Last-Event-ID": cursor(bus, 43)})
	h := helloOf(t, frames)
	if h.Verdict != sse.VerdictGapBudget || h.Resumed {
		t.Errorf("hello at head-257 = %+v, want verdict gap_budget and resumed false", h)
	}
	if got := replayedIDs(frames); len(got) != 0 {
		t.Errorf("gap_budget connect replayed %d frames, want none", len(got))
	}

	frames = handleOnce(t, bus, map[string]string{"SSE-Wire": "1", "Last-Event-ID": cursor(bus, 44)})
	h = helloOf(t, frames)
	if h.Verdict != sse.VerdictResumed || !h.Resumed {
		t.Errorf("hello at head-256 = %+v, want verdict resumed", h)
	}
	if got := replayedIDs(frames); len(got) != ReplyMaxEvents {
		t.Errorf("resumed connect replayed %d frames, want ReplyMaxEvents (%d)", len(got), ReplyMaxEvents)
	}
}

// TestHandle_legacy_gap_follows_the_verdict pins the overlap frame's one
// derived field: gap is the inverse of the hello's resumed, so a legacy tab
// that presents a covered cursor reads gap false over a complete replay and
// one past the cap reads gap true with nothing replayed.
func TestHandle_legacy_gap_follows_the_verdict(t *testing.T) {
	t.Parallel()
	bus := New(0, nil)
	publishN(bus, 10)

	frames := handleOnce(t, bus, map[string]string{"Last-Event-ID": cursor(bus, 5)})
	_, ep := epochOf(t, frames)
	if ep.Gap {
		t.Error("gap = true over a covered cursor, want false")
	}
	if got := replayedIDs(frames); len(got) != 5 {
		t.Errorf("covered cursor replayed %d frames, want 5", len(got))
	}

	frames = handleOnce(t, bus, map[string]string{"Last-Event-ID": cursor(bus, 11)})
	_, ep = epochOf(t, frames)
	if !ep.Gap {
		t.Error("gap = false above head, want true")
	}
	if got := replayedIDs(frames); len(got) != 0 {
		t.Errorf("gap connect replayed %v, want none", got)
	}
}

// TestHandleEpochBeforeLive pins the ordering on a real legacy stream: the
// hello first, then the replay, then the epoch frame, then live frames.
func TestHandleEpochBeforeLive(t *testing.T) {
	bus := New(0, nil)
	publishN(bus, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Handle(bus, w, r)
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Last-Event-ID", cursor(bus, 1))
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
	want := []string{"sse:hello:", "notify:" + cursor(bus, 2), "epoch:", "notify:" + cursor(bus, 3)}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Errorf("wire order = %v, want hello, replay, epoch, live (%v)", order, want)
	}
}

// TestPublishIsTopicless pins that the bus publishes broadcasts: an empty
// Topic reaches every subscriber regardless of filter.
func TestPublishIsTopicless(t *testing.T) {
	t.Parallel()
	bus := New(0, nil)
	publishN(bus, 1)
	for _, ev := range bus.hub.Snapshot() {
		if ev.Event.Topic != "" {
			t.Errorf("published event carries topic %q, want topicless broadcast", ev.Event.Topic)
		}
	}
}

// TestRingCapacity pins WithReplay(SSERing) at events.New: the ring holds
// exactly SSERing events, so the floor moves once it fills.
func TestRingCapacity(t *testing.T) {
	t.Parallel()
	bus := New(0, nil)
	publishN(bus, SSERing+1)
	pos := bus.hub.Position()
	if pos.Floor != 2 || pos.Head != uint64(SSERing+1) {
		t.Errorf("Position() after SSERing+1 publishes = (floor %d, head %d), want (2, %d)", pos.Floor, pos.Head, SSERing+1)
	}
}

// TestEpoch_stable_per_bus pins one epoch per bus (per process start): two
// connections see the same value, and a second bus differs.
func TestEpoch_stable_per_bus(t *testing.T) {
	t.Parallel()
	bus := New(0, nil)
	_, ep1 := epochOf(t, handleOnce(t, bus, nil))
	_, ep2 := epochOf(t, handleOnce(t, bus, nil))
	if ep1.BootID != ep2.BootID {
		t.Errorf("epochs differ across connections: %q vs %q", ep1.BootID, ep2.BootID)
	}
	if len(ep1.BootID) != 16 {
		t.Errorf("epoch = %q, want 16 hex characters", ep1.BootID)
	}
	_, ep3 := epochOf(t, handleOnce(t, New(0, nil), nil))
	if ep3.BootID == ep1.BootID {
		t.Errorf("two buses share epoch %q, want distinct per process start", ep1.BootID)
	}
}
