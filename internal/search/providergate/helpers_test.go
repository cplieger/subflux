package providergate

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/auth/v6"
	"github.com/cplieger/subflux/internal/subflux"
)

const (
	hdbits  subflux.ProviderID = "hdbits"
	subdl   subflux.ProviderID = "subdl"
	animeto subflux.ProviderID = "animetosho"

	secretA = "placeholder-passkey-a"
	secretB = "placeholder-passkey-b"
)

var errAuth = &subflux.AuthError{Msg: "HDBits refused the username and passkey (status 5: Auth failed)"}

type fakeClock struct {
	now time.Time
	mu  sync.Mutex
}

func newClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 9, 21, 13, 50, 0, 0, time.Local)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

type gaugeKey struct {
	id      subflux.ProviderID
	setting string
}

type fakeMetrics struct {
	disabled     map[subflux.ProviderID]bool
	rejected     map[gaugeKey]bool
	authFailures map[subflux.ProviderID]int
	rateLimited  map[pauseKey]int
	mu           sync.Mutex
}

func newMetrics() *fakeMetrics {
	return &fakeMetrics{
		disabled:     make(map[subflux.ProviderID]bool),
		rejected:     make(map[gaugeKey]bool),
		authFailures: make(map[subflux.ProviderID]int),
		rateLimited:  make(map[pauseKey]int),
	}
}

func (m *fakeMetrics) SetProviderDisabled(id subflux.ProviderID, disabled bool) {
	m.mu.Lock()
	m.disabled[id] = disabled
	m.mu.Unlock()
}

func (m *fakeMetrics) DeleteProviderDisabled(id subflux.ProviderID) {
	m.mu.Lock()
	delete(m.disabled, id)
	m.mu.Unlock()
}

func (m *fakeMetrics) IncProviderAuthFailure(id subflux.ProviderID) {
	m.mu.Lock()
	m.authFailures[id]++
	m.mu.Unlock()
}

func (m *fakeMetrics) IncProviderRateLimited(id subflux.ProviderID, op Op) {
	m.mu.Lock()
	m.rateLimited[pauseKey{id: id, op: op}]++
	m.mu.Unlock()
}

func (m *fakeMetrics) SetProviderSettingRejected(id subflux.ProviderID, setting string, rejected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if rejected {
		m.rejected[gaugeKey{id: id, setting: setting}] = true
		return
	}
	delete(m.rejected, gaugeKey{id: id, setting: setting})
}

// disabledSeries reports the gauge value and whether the series exists.
func (m *fakeMetrics) disabledSeries(id subflux.ProviderID) (value, exists bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, exists = m.disabled[id]
	return value, exists
}

func (m *fakeMetrics) failures(id subflux.ProviderID) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.authFailures[id]
}

// heldVerdict is a provider instance holding its upstream's answer about one
// optional setting until the gate tells it to forget.
type heldVerdict struct {
	refusal error
	setting string
	forgets int
}

func refusing(setting string, reason error) *heldVerdict {
	return &heldVerdict{setting: setting, refusal: reason}
}

func accepting(setting string) *heldVerdict { return &heldVerdict{setting: setting} }

func (h *heldVerdict) SettingVerdict() (setting string, refusal error) { return h.setting, h.refusal }

func (h *heldVerdict) ForgetSettingVerdict() {
	h.setting, h.refusal = "", nil
	h.forgets++
}

type eventLog struct {
	events []Event
	mu     sync.Mutex
}

func (l *eventLog) record(e Event) {
	l.mu.Lock()
	l.events = append(l.events, e)
	l.mu.Unlock()
}

func (l *eventLog) kinds(id subflux.ProviderID) []Kind {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Kind
	for _, e := range l.events {
		if e.Provider == id {
			out = append(out, e.Kind)
		}
	}
	return out
}

func (l *eventLog) last(id subflux.ProviderID) (Event, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, e := range slices.Backward(l.events) {
		if e.Provider == id {
			return e, true
		}
	}
	return Event{}, false
}

// memStore is an in-memory Store that counts writes.
type memStore struct {
	recs    map[subflux.ProviderID]subflux.ProviderAuthRecord
	readErr error
	writes  int
	mu      sync.Mutex
}

func newMemStore() *memStore {
	return &memStore{recs: make(map[subflux.ProviderID]subflux.ProviderAuthRecord)}
}

func (s *memStore) ProviderAuthRecords(context.Context) ([]subflux.ProviderAuthRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return nil, s.readErr
	}
	out := make([]subflux.ProviderAuthRecord, 0, len(s.recs))
	for _, r := range s.recs {
		out = append(out, r)
	}
	return out, nil
}

func (s *memStore) PutProviderAuthRecord(_ context.Context, rec *subflux.ProviderAuthRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	r := *rec
	r.FailedOps = slices.Clone(rec.FailedOps)
	s.recs[rec.Provider] = r
	return nil
}

func (s *memStore) DeleteProviderAuthRecord(_ context.Context, id subflux.ProviderID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writes++
	delete(s.recs, id)
	return nil
}

func (s *memStore) stored(id subflux.ProviderID) (subflux.ProviderAuthRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.recs[id]
	return r, ok
}

func (s *memStore) writeCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

// testHasher is a deliberately cheap Argon2id hasher: the gate's behaviour,
// not the derivation cost, is under test.
func testHasher(t *testing.T) *auth.Hasher {
	t.Helper()
	h, err := auth.NewHasher(auth.Argon2Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
	if err != nil {
		t.Fatalf("NewHasher: %v", err)
	}
	return h
}

type rig struct {
	gate    *Gate
	clock   *fakeClock
	metrics *fakeMetrics
	events  *eventLog
	store   *memStore
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{clock: newClock(), metrics: newMetrics(), events: &eventLog{}, store: newMemStore()}
	r.gate = r.open(t, r.store)
	return r
}

func (r *rig) open(t *testing.T, store Store) *Gate {
	t.Helper()
	g, err := Open(t.Context(), Config{Store: store, Hasher: testHasher(t), Metrics: r.metrics, Now: r.clock.Now})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	g.SetOnChange(r.events.record)
	return g
}

func hdbitsSettings(passkey string) map[subflux.ProviderID]map[string]any {
	return map[subflux.ProviderID]map[string]any{
		hdbits: {"username": "placeholder-user", "passkey": passkey},
		subdl:  {"api_key": "placeholder-api-key"},
	}
}

// liveBinding binds and activates settings, then reconciles.
func (r *rig) liveBinding(t *testing.T, settings map[subflux.ProviderID]map[string]any) *Binding {
	t.Helper()
	b := r.gate.Bind(settings)
	r.gate.Activate(b)
	r.gate.Reconcile(t.Context())
	return b
}

func mustAdmit(t *testing.T, b *Binding, id subflux.ProviderID, op Op) {
	t.Helper()
	if ok, reason := b.Admit(id, op); !ok {
		t.Fatalf("Admit(%s, %s) = false (%q), want true", id, op, reason)
	}
}

func mustRefuse(t *testing.T, b *Binding, id subflux.ProviderID, op Op, wantReason string) {
	t.Helper()
	ok, reason := b.Admit(id, op)
	if ok {
		t.Fatalf("Admit(%s, %s) = true, want refused with %q", id, op, wantReason)
	}
	if reason != wantReason {
		t.Fatalf("Admit(%s, %s) reason = %q, want %q", id, op, reason, wantReason)
	}
}

var errTransient = errors.New("hdbits: HTTP 502")
