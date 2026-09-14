package activityhandlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/activityhandlers"
	"github.com/cplieger/subflux/internal/server/events"
)

// recordingMetrics counts the connects and the presence transitions the bus
// reports; the other sink methods are inert.
type recordingMetrics struct {
	transitions []string
	connects    atomic.Int64
	mu          sync.Mutex
}

func (m *recordingMetrics) RecordSSEConnect(string, bool) { m.connects.Add(1) }
func (m *recordingMetrics) RecordSSEDisconnect(string)    {}
func (m *recordingMetrics) RecordSSEPresenceTransition(kind string) {
	m.mu.Lock()
	m.transitions = append(m.transitions, kind)
	m.mu.Unlock()
}
func (m *recordingMetrics) SetSSEClients(int)      {}
func (m *recordingMetrics) SetSSEQueuedFrames(int) {}
func (m *recordingMetrics) SetSSEHead(uint64)      {}

func (m *recordingMetrics) snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.transitions...)
}

func newEventsHandler(t *testing.T, m events.Metrics) (*activityhandlers.Handler, *events.EventBus) {
	t.Helper()
	bus := events.New(1, m)
	h := activityhandlers.New(activityhandlers.Deps{
		Activity: activity.New(10),
		Alerts:   activity.NewAlertLog(10),
		Stops:    &activity.StopRegistry{},
		Events:   bus,
	})
	return h, bus
}

type digestReply struct {
	Epoch   string `json:"epoch"`
	Changed []struct {
		Kind    string `json:"kind"`
		Ref     string `json:"ref"`
		Version string `json:"version"`
	} `json:"changed"`
	Checked     int  `json:"checked"`
	MustRefetch bool `json:"must_refetch"`
}

func postDigest(t *testing.T, h *activityhandlers.Handler, body string) (int, digestReply) {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/events/sync", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.HandleEventsSync(rec, req)
	var reply digestReply
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil {
			t.Fatalf("decode digest reply %q: %v", rec.Body.String(), err)
		}
	}
	return rec.Code, reply
}

func TestHandleEventsSync_answers_the_digest_over_the_bus_versions(t *testing.T) {
	t.Parallel()
	h, bus := newEventsHandler(t, nil)
	body := `{"epoch":"` + bus.Epoch() + `","subjects":[{"kind":"activity","ref":"","version":"0"}]}`

	code, reply := postDigest(t, h, body)
	if code != http.StatusOK || reply.Checked != 1 || reply.MustRefetch || len(reply.Changed) != 0 {
		t.Fatalf("digest before any publish = %d %+v, want 200, checked 1, nothing changed", code, reply)
	}

	entry := activity.Entry{ID: "1", Action: "Scan"}
	bus.Publish(events.Event{Type: events.ActivityDelta, Data: events.ActivityEvent{Op: events.ActivityUpsert, Entry: &entry}})

	code, reply = postDigest(t, h, body)
	if code != http.StatusOK || reply.Checked != 1 || reply.MustRefetch {
		t.Fatalf("digest after one activity publish = %d %+v, want 200, checked 1", code, reply)
	}
	if len(reply.Changed) != 1 || reply.Changed[0].Kind != events.SubjectActivity || reply.Changed[0].Version != "1" {
		t.Errorf("changed = %+v, want [{activity  1}]", reply.Changed)
	}
}

func TestHandleEventsSync_foreign_epoch_is_must_refetch(t *testing.T) {
	t.Parallel()
	h, _ := newEventsHandler(t, nil)
	code, reply := postDigest(t, h, `{"epoch":"0123456789abcdef","subjects":[{"kind":"activity","ref":"","version":"0"}]}`)
	if code != http.StatusOK || !reply.MustRefetch {
		t.Errorf("digest with a foreign epoch = %d %+v, want 200 must_refetch", code, reply)
	}
}

func TestHandleEventsSync_get_is_method_not_allowed(t *testing.T) {
	t.Parallel()
	h, _ := newEventsHandler(t, nil)
	rec := httptest.NewRecorder()
	h.HandleEventsSync(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/events/sync", http.NoBody))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/events/sync = %d, want 405", rec.Code)
	}
}

func TestHandleEventsAlive_refuses_a_bad_tag_and_records_nothing_without_a_socket(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		tag  string
		set  bool
		want int
	}{
		{name: "valid_tag_no_socket", tag: "functional-1", set: true, want: http.StatusNoContent},
		{name: "missing_header", want: http.StatusBadRequest},
		{name: "too_long", tag: strings.Repeat("a", 65), set: true, want: http.StatusBadRequest},
		{name: "bad_byte", tag: "not valid!", set: true, want: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &recordingMetrics{}
			h, _ := newEventsHandler(t, m)
			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/events/alive", http.NoBody)
			if tc.set {
				req.Header.Set("SSE-Client", tc.tag)
			}
			rec := httptest.NewRecorder()
			h.HandleEventsAlive(rec, req)
			if rec.Code != tc.want {
				t.Errorf("POST /api/events/alive with tag %q = %d, want %d; body %s", tc.tag, rec.Code, tc.want, rec.Body.String())
			}
			if got := m.snapshot(); len(got) != 0 {
				t.Errorf("presence transitions = %v, want none (no socket presented tag %q)", got, tc.tag)
			}
		})
	}
}

// TestHandleEventsAlive_acknowledges_a_connected_tag pins the route's reach
// into the presence table: a stream that presented the tag makes the first
// acknowledgement an alive transition.
func TestHandleEventsAlive_acknowledges_a_connected_tag(t *testing.T) {
	t.Parallel()
	m := &recordingMetrics{}
	h, bus := newEventsHandler(t, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events.Handle(bus, w, r)
	}))
	t.Cleanup(srv.Close)

	stream, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	stream.Header.Set("SSE-Wire", "1")
	stream.Header.Set("SSE-Client", "functional-1")
	resp, err := http.DefaultClient.Do(stream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	// The connect counter fires from the OnConnect hook, after the presence
	// hook seeded the row on the same goroutine.
	deadline := time.Now().Add(5 * time.Second)
	for m.connects.Load() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("no connect recorded within 5s of the stream request")
		}
		time.Sleep(5 * time.Millisecond)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/api/events/alive", http.NoBody)
	req.Header.Set("SSE-Client", "functional-1")
	rec := httptest.NewRecorder()
	h.HandleEventsAlive(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("POST /api/events/alive for the connected tag = %d, want 204; body %s", rec.Code, rec.Body.String())
	}
	if got := m.snapshot(); len(got) != 1 || got[0] != "alive" {
		t.Errorf("presence transitions after the first acknowledgement = %v, want [alive]", got)
	}
}
