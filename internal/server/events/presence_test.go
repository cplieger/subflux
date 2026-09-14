package events

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// countingMetrics records the presence transitions and counts connects;
// every other sink method is inert.
type countingMetrics struct {
	nopMetrics
	transitions []string
	connects    atomic.Int64
	mu          sync.Mutex
}

func (m *countingMetrics) RecordSSEConnect(string, bool) { m.connects.Add(1) }

func (m *countingMetrics) RecordSSEPresenceTransition(kind string) {
	m.mu.Lock()
	m.transitions = append(m.transitions, kind)
	m.mu.Unlock()
}

func (m *countingMetrics) snapshot() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.transitions...)
}

// waitConnects blocks until want connects have been recorded. The connect
// counter fires from the OnConnect hook, which the hub runs after the
// presence hook on the same goroutine, so the presence table has seen every
// counted connect; ClientCount moves at subscribe and would not order that.
func waitConnects(t *testing.T, m *countingMetrics, want int64) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for m.connects.Load() != want {
		if time.Now().After(deadline) {
			t.Fatalf("connects recorded = %d, want %d", m.connects.Load(), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (p *Presence) rowSnapshot(tag string) (presenceRow, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.rows[tag]
	if !ok {
		return presenceRow{}, false
	}
	return *r, true
}

func TestPresence_expires_a_silent_socket_and_revives_it_on_the_next_ack(t *testing.T) {
	t.Parallel()
	m := &countingMetrics{}
	p := newPresence(m)
	t0 := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)

	p.connected("abc", t0)
	if r, ok := p.rowSnapshot("abc"); !ok || r.connected != 1 {
		t.Fatalf("row after connect = %+v (present %v), want connected 1", r, ok)
	}
	p.Sweep(t0.Add(aliveWindow))
	if got := m.snapshot(); len(got) != 0 {
		t.Fatalf("transitions inside the window = %v, want none", got)
	}

	p.Sweep(t0.Add(aliveWindow + time.Second))
	if got := m.snapshot(); len(got) != 1 || got[0] != presenceExpired {
		t.Fatalf("transitions after 31s of silence = %v, want [expired]", got)
	}
	p.Sweep(t0.Add(aliveWindow + 2*time.Second))
	if got := m.snapshot(); len(got) != 1 {
		t.Fatalf("a second sweep past the window recorded %v, want one expired per lapse", got)
	}
	if r, _ := p.rowSnapshot("abc"); r.connected != 1 {
		t.Errorf("expired row connected = %d, want 1 (the socket is still open)", r.connected)
	}

	p.Alive("abc", t0.Add(aliveWindow+3*time.Second))
	if got := m.snapshot(); len(got) != 2 || got[1] != presenceAlive {
		t.Fatalf("transitions after the ack = %v, want [expired alive]", got)
	}
	p.Alive("abc", t0.Add(aliveWindow+4*time.Second))
	if got := m.snapshot(); len(got) != 2 {
		t.Errorf("a second ack recorded %v, want no new transition", got)
	}

	p.disconnected("abc")
	p.Sweep(t0.Add(aliveWindow + 5*time.Second))
	if _, ok := p.rowSnapshot("abc"); !ok {
		t.Fatal("row swept inside the window after disconnect, want kept")
	}
	p.Sweep(t0.Add(2*aliveWindow + 5*time.Second))
	if _, ok := p.rowSnapshot("abc"); ok {
		t.Error("row still present one window after its last ack with no socket, want swept")
	}
}

func TestPresence_first_ack_after_connect_is_alive(t *testing.T) {
	t.Parallel()
	m := &countingMetrics{}
	p := newPresence(m)
	t0 := time.Now()
	p.connected("abc", t0)
	p.Alive("abc", t0.Add(time.Second))
	if got := m.snapshot(); len(got) != 1 || got[0] != presenceAlive {
		t.Errorf("transitions = %v, want [alive]", got)
	}
}

func TestPresence_ack_without_a_socket_mints_no_row(t *testing.T) {
	t.Parallel()
	m := &countingMetrics{}
	p := newPresence(m)
	p.Alive("ghost", time.Now())
	if r, ok := p.rowSnapshot("ghost"); ok {
		t.Errorf("Alive(%q) with no connected socket minted %+v, want no row", "ghost", r)
	}
	if got := m.snapshot(); len(got) != 0 {
		t.Errorf("transitions = %v, want none", got)
	}
}

// TestHandle_passes_the_client_tag_raw pins the SSE-Client pass-through: a
// tag inside the library's grammar keys the presence row, one outside it is
// absent (the library's verdict), and no row is minted for it.
func TestHandle_passes_the_client_tag_raw(t *testing.T) {
	t.Parallel()
	m := &countingMetrics{}
	bus := New(0, m)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		Handle(bus, w, r)
	}))
	t.Cleanup(srv.Close)

	connect := func(tag string) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("SSE-Wire", "1")
		req.Header.Set("SSE-Client", tag)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { resp.Body.Close() })
	}
	connect("abc")
	waitConnects(t, m, 1)
	if r, ok := bus.Presence().rowSnapshot("abc"); !ok || r.connected != 1 {
		t.Errorf("presence row for tag abc = %+v (present %v), want connected 1", r, ok)
	}

	connect("not valid!")
	waitConnects(t, m, 2)
	if _, ok := bus.Presence().rowSnapshot("not valid!"); ok {
		t.Error("presence row minted for a tag outside the grammar, want none")
	}
	if _, ok := bus.Presence().rowSnapshot(""); ok {
		t.Error("presence row minted for the empty tag, want none")
	}
}

// TestHandle_absent_client_tag_is_silent pins what a connect without the
// SSE-Client header costs: no presence row and no rejection Warn from the hub,
// which is what lets the handler pass the header through unconditionally
// (legacy tabs, curl and every test connect without one). Serial: the hub
// logs through slog.Default(), read at New.
func TestHandle_absent_client_tag_is_silent(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	m := &countingMetrics{}
	bus := New(0, m)
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
	waitConnects(t, m, 1)

	if r, ok := bus.Presence().rowSnapshot(""); ok {
		t.Errorf("presence row for the empty tag = %+v, want none", r)
	}
	if got := logs.String(); strings.Contains(got, `msg="sse: client tag rejected"`) {
		t.Errorf("connect without SSE-Client logged a tag rejection:\n%s", got)
	}
}
