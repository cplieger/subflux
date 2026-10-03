package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cplieger/auth/v6"
	"github.com/cplieger/subflux/internal/arrsvc"
	"github.com/cplieger/subflux/internal/config"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/search/providerhealth"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/server/activity"
	"github.com/cplieger/subflux/internal/server/events"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
	"github.com/cplieger/subflux/internal/wiring"
)

const gateSecret = "placeholder-key"

var errGateRefused = &subflux.AuthError{Msg: "OpenSubtitles refused the API key (HTTP 401)"}

// authRecordStore is an in-memory providergate.Store, so a second gate can
// load what a first one persisted, the way a restart does.
type authRecordStore struct {
	recs map[subflux.ProviderID]subflux.ProviderAuthRecord
	mu   sync.Mutex
}

func (s *authRecordStore) ProviderAuthRecords(context.Context) ([]subflux.ProviderAuthRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Collect(maps.Values(s.recs)), nil
}

func (s *authRecordStore) PutProviderAuthRecord(_ context.Context, rec *subflux.ProviderAuthRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.recs == nil {
		s.recs = map[subflux.ProviderID]subflux.ProviderAuthRecord{}
	}
	s.recs[rec.Provider] = *rec
	return nil
}

func (s *authRecordStore) DeleteProviderAuthRecord(_ context.Context, id subflux.ProviderID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, id)
	return nil
}

// stepClock is a gate clock each test moves by hand.
type stepClock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *stepClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *stepClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newStepClock() *stepClock {
	return &stepClock{now: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)}
}

// openTestGate opens a gate on clock, persisting to store when it is non-nil.
func openTestGate(t *testing.T, store providergate.Store, m providergate.Metrics, clock *stepClock) *providergate.Gate {
	t.Helper()
	cfg := providergate.Config{Metrics: m, Now: clock.Now}
	if store != nil {
		h, err := auth.NewHasher(auth.Argon2Params{Memory: 1024, Iterations: 1, Parallelism: 1, SaltLength: 16, KeyLength: 32})
		if err != nil {
			t.Fatalf("NewHasher: %v", err)
		}
		cfg.Store, cfg.Hasher = store, h
	}
	g, err := providergate.Open(t.Context(), cfg)
	if err != nil {
		t.Fatalf("providergate.Open: %v", err)
	}
	return g
}

// gateRegistry registers one stub provider, opensubtitles, with a secret key.
func gateRegistry() *provider.Registry {
	reg := provider.NewRegistry()
	reg.Register("opensubtitles", func(context.Context, map[string]any) (provider.Provider, error) {
		return &stubProvider{name: "opensubtitles"}, nil
	})
	reg.RegisterSchema("opensubtitles", "OpenSubtitles", []subflux.ProviderSchemaField{{Key: "api_key", Secret: true}})
	return reg
}

// gateConfig is a configuration whose only provider is opensubtitles, keyed
// with gateSecret.
func gateConfig(t *testing.T, enabled bool, extra ...string) *config.Config {
	t.Helper()
	doc := "sonarr:\n  url: \"http://sonarr:8989\"\n  api_key: \"test\"\n" +
		"languages:\n  default:\n    - code: en\n" +
		"providers:\n  opensubtitles:\n    enabled: " + strconv.FormatBool(enabled) + "\n" +
		"    settings:\n      api_key: \"" + gateSecret + "\"\n" +
		"media_roots:\n  - " + strconv.Quote(t.TempDir()) + "\n" + strings.Join(extra, "\n")
	cfg, err := config.LoadFromBytes(t.Context(), []byte(doc))
	if err != nil {
		t.Fatalf("config.LoadFromBytes: %v\n%s", err, doc)
	}
	t.Cleanup(func() { _ = cfg.Close() })
	return cfg
}

// newGateServer builds a Server through New, so the production gate hook and
// provider publisher are the ones installed.
func newGateServer(t *testing.T, reg *provider.Registry, gate *providergate.Gate, m *obs.Metrics, wire wiring.Func) *Server {
	t.Helper()
	arr := func(_, _ string, _ *arrsvc.ReadGate) (SonarrClient, error) { return dummyArrClient{}, nil }
	radarr := func(_, _ string, _ *arrsvc.ReadGate) (RadarrClient, error) { return dummyArrClient{}, nil }
	s := New(&testsupport.NopStore{}, reg,
		WithMetrics(m),
		WithAlertLog(activity.NewAlertLog(100)),
		WithProviderGate(gate),
		WithMediaWriter(testsupport.MediaWriter()),
		WithMediaPresence(testsupport.MediaPresence()),
		WithWire(wire),
		WithArrClientFactories(arr, radarr),
	)
	s.launchWorkers = func() {}
	return s
}

// disableThroughLadder drives b's opensubtitles through every rejection the
// ladder allows, moving clock past each cooldown.
func disableThroughLadder(t *testing.T, b *providergate.Binding, clock *stepClock) {
	t.Helper()
	for range providergate.MaxAuthFailures {
		b.Observe(t.Context(), "opensubtitles", providergate.OpSearch, errGateRefused)
		clock.Advance(time.Hour)
	}
}

func metricsBody(t *testing.T, m *obs.Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/metrics", http.NoBody))
	return rec.Body.String()
}

// A disable recorded before a restart is the operator's problem until they
// fix it, so the first activation after the restart raises the alert again and
// the gauge reads 1. Switching the provider off is one of the fixes: the alert
// goes and so does the series, rather than a stale 1 firing the alert rule.
func TestActivate_a_persisted_disable_alerts_until_the_provider_is_switched_off(t *testing.T) {
	t.Parallel()
	reg := gateRegistry()
	store := &authRecordStore{}
	clock := newStepClock()
	before := openTestGate(t, store, testsupport.NopGateMetrics{}, clock)
	b := before.Bind(map[subflux.ProviderID]map[string]any{
		"opensubtitles": reg.Normalize("opensubtitles", map[string]any{"api_key": gateSecret}),
	})
	before.Activate(b)
	disableThroughLadder(t, b, clock)

	m := obs.New()
	gate := openTestGate(t, store, m, clock)
	s := newGateServer(t, reg, gate, m, func(ctx context.Context, cfg *config.Config, db search.Store, sm search.Metrics) (wiring.Result, error) {
		return wiring.Build(ctx, cfg, db, sm, reg, gate, wiring.Extras{SyncExec: syncing.InProcessExec{}, Tracks: search.NoopDetector{}, Media: testsupport.MediaWriter()})
	})
	const series = `subflux_provider_disabled{provider="opensubtitles"} 1`

	if err := s.activate(t.Context(), gateConfig(t, true), activateCold); err != nil {
		t.Fatalf("activate(enabled) error = %v", err)
	}
	alert := providerAlertText(s, "provider:opensubtitles")
	if !strings.HasPrefix(alert, "OpenSubtitles rejected its credentials and was disabled after 3 attempts") {
		t.Errorf("alert after a cold activation = %q, want the disable alert", alert)
	}
	if !strings.Contains(metricsBody(t, m), series) {
		t.Errorf("/metrics after a cold activation lacks %q", series)
	}

	if err := s.activate(t.Context(), gateConfig(t, false), activateHot); err != nil {
		t.Fatalf("activate(disabled) error = %v", err)
	}
	if alert := providerAlertText(s, "provider:opensubtitles"); alert != "" {
		t.Errorf("alert after switching the provider off = %q, want none", alert)
	}
	if body := metricsBody(t, m); strings.Contains(body, `subflux_provider_disabled{provider="opensubtitles"}`) {
		t.Errorf("/metrics after switching the provider off still carries the series:\n%s", body)
	}
}

func providerAlertText(s *Server, source string) string {
	for _, a := range s.alerts.VisibleAlerts() {
		if a.Source == source {
			return a.Message
		}
	}
	return ""
}

// gateRig is a server whose engine carries a real health tracker the test can
// trip directly, beside the gate binding activation handed out.
type gateRig struct {
	srv     *Server
	clock   *stepClock
	tracker *providerhealth.Tracker
	binding *providergate.Binding
}

// newGateRig activates a server over a memory-only gate. withTimeouts selects
// whether provider timeouts are configured and the engine tracks health.
func newGateRig(t *testing.T, withTimeouts bool) *gateRig {
	t.Helper()
	r := &gateRig{clock: newStepClock()}
	reg := gateRegistry()
	gate := openTestGate(t, nil, testsupport.NopGateMetrics{}, r.clock)
	if withTimeouts {
		r.tracker = providerhealth.New(providerhealth.Config{Now: r.clock.Now})
	}
	r.srv = newGateServer(t, reg, gate, obs.New(), func(_ context.Context, cfg *config.Config, db search.Store, m search.Metrics) (wiring.Result, error) {
		providers := []provider.Provider{&stubProvider{name: "opensubtitles"}}
		r.binding = gate.Bind(map[subflux.ProviderID]map[string]any{"opensubtitles": {"api_key": gateSecret}})
		scores := cfg.Scores()
		sc := scorer.New(&scores)
		opts := []search.Option{
			search.WithStore(db), search.WithConfig(cfg), search.WithMetrics(m), search.WithScorer(sc),
			search.WithSyncer(syncing.Syncer{}), search.WithTracks(search.NoopDetector{}),
			search.WithProviderGate(r.binding), search.WithMediaWriter(testsupport.MediaWriter()),
		}
		if r.tracker != nil {
			opts = append(opts, search.WithTimeout(r.tracker))
		}
		return wiring.Result{Engine: search.New(providers, opts...), Scorer: sc, Providers: providers, Binding: r.binding}, nil
	})
	var extra []string
	if !withTimeouts {
		extra = append(extra, "search:\n  provider_timeout: 0s")
	}
	if err := r.srv.activate(t.Context(), gateConfig(t, true, extra...), activateCold); err != nil {
		t.Fatalf("activate() error = %v", err)
	}
	return r
}

// providerStream is an SSE subscriber reading provider events off the bus.
type providerStream struct{ sc *bufio.Scanner }

func subscribe(t *testing.T, s *Server) *providerStream {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		events.Handle(s.events, w, r)
	}))
	t.Cleanup(srv.Close)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("SSE-Wire", "1")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	st := &providerStream{sc: bufio.NewScanner(resp.Body)}
	for st.sc.Scan() {
		if l := st.sc.Text(); strings.HasPrefix(l, "data: ") && strings.Contains(l, `"verdict":`) {
			return st
		}
	}
	t.Fatal("stream ended before its hello")
	return nil
}

// next returns the next provider event on the stream.
func (p *providerStream) next(t *testing.T) events.ProviderEvent {
	t.Helper()
	for p.sc.Scan() {
		line, ok := strings.CutPrefix(p.sc.Text(), "data: ")
		if !ok || !strings.Contains(line, `"type":"provider"`) {
			continue
		}
		var frame struct {
			Data events.ProviderEvent `json:"data"`
		}
		if err := json.Unmarshal([]byte(line), &frame); err != nil {
			t.Fatalf("decode provider frame %s: %v", line, err)
		}
		return frame.Data
	}
	t.Fatalf("stream ended before a provider event: %v", p.sc.Err())
	return events.ProviderEvent{}
}

// The client replaces its record for a provider with each event's status, so
// every provider event must carry the whole status: a health clear sent after
// a credential disable that said only "not timed out" would repaint a disabled
// provider as healthy, and a gate event that said only the gate's half would
// erase a running health cooldown.
func TestProviderEvents_carry_the_full_merged_status(t *testing.T) {
	t.Parallel()
	t.Run("health events after a disable", func(t *testing.T) {
		t.Parallel()
		r := newGateRig(t, true)
		st := subscribe(t, r.srv)
		disableThroughLadder(t, r.binding, r.clock)
		if ev := st.next(t); ev.Op != events.ProviderRaise || !ev.Entry.Status.Disabled {
			t.Fatalf("disable event = %+v, want a raise carrying disabled", ev)
		}

		for range providerhealth.DefaultThreshold {
			r.tracker.RecordFailure("opensubtitles", errors.New("HTTP 502"))
		}
		r.tracker.RecordSuccess("opensubtitles")
		for _, want := range []events.ProviderOp{events.ProviderRaise, events.ProviderClear} {
			if ev := st.next(t); ev.Op != want || !ev.Entry.Status.Disabled {
				t.Errorf("health %s event = %+v, want it to still carry disabled", want, ev)
			}
		}
	})
	t.Run("a gate event during a health cooldown", func(t *testing.T) {
		t.Parallel()
		r := newGateRig(t, true)
		st := subscribe(t, r.srv)
		for range providerhealth.DefaultThreshold {
			r.tracker.RecordFailure("opensubtitles", errors.New("HTTP 502"))
		}
		if ev := st.next(t); !ev.Entry.Status.TimedOut {
			t.Fatalf("health raise = %+v, want timed out", ev)
		}

		r.binding.Observe(t.Context(), "opensubtitles", providergate.OpDownload, &subflux.RateLimitError{Msg: "download limit (406)"})
		if ev := st.next(t); !ev.Entry.Status.TimedOut || ev.Entry.Status.PausedFor <= 0 || !ev.TimeoutsEnabled {
			t.Errorf("pause event = %+v, want the pause beside the running timeout", ev)
		}
	})
	t.Run("timeouts off", func(t *testing.T) {
		t.Parallel()
		r := newGateRig(t, false)
		st := subscribe(t, r.srv)
		disableThroughLadder(t, r.binding, r.clock)
		if ev := st.next(t); ev.TimeoutsEnabled || !ev.Entry.Status.Disabled {
			t.Errorf("disable event with provider_timeout 0 = %+v, want timeouts_enabled false and disabled", ev)
		}
	})
}

// A provider that keeps rejecting its credentials reaches the operator once:
// one persistent alert and one ERROR line naming the provider, and the line
// never carries the credential itself.
func TestGateHook_a_disable_alerts_once_and_logs_no_secret(t *testing.T) {
	// No t.Parallel: captureSlog swaps the process-wide default logger.
	logs := captureSlog(t)
	r := newGateRig(t, true)

	disableThroughLadder(t, r.binding, r.clock)
	r.binding.Observe(t.Context(), "opensubtitles", providergate.OpSearch, errGateRefused)

	var raised int
	for _, a := range r.srv.alerts.VisibleAlerts() {
		if a.Source == "provider:opensubtitles" {
			raised++
		}
	}
	if raised != 1 {
		t.Errorf("provider alerts = %d, want 1", raised)
	}
	var errorLines []string
	for line := range strings.Lines(logs.String()) {
		if strings.Contains(line, `msg="provider disabled: credentials rejected"`) {
			errorLines = append(errorLines, line)
		}
	}
	if len(errorLines) != 1 || !strings.Contains(errorLines[0], "level=ERROR") ||
		!strings.Contains(errorLines[0], "provider=opensubtitles") {
		t.Errorf("disable log lines = %q, want one ERROR line naming opensubtitles", errorLines)
	}
	if strings.Contains(logs.String(), gateSecret) {
		t.Errorf("logs carry the credential:\n%s", logs.String())
	}
}

// The gate's events become the operator surfaces a provider fault calls for:
// a rejected credential and a rejected optional setting each raise their own
// persistent alert, a fix dismisses it, and a quota pause only repaints.
func TestNewProviderGateHook(t *testing.T) {
	t.Parallel()
	label := func(id subflux.ProviderID) string {
		return map[subflux.ProviderID]string{"animetosho": "AnimeTosho"}[id]
	}
	tests := []struct {
		name        string
		ev          providergate.Event
		seed        map[string]string
		wantAlerts  map[string]string
		wantPublish events.ProviderOp
	}{
		{
			name:        "disabled raises the provider alert",
			ev:          providergate.Event{Provider: "animetosho", Kind: providergate.Disabled, Reason: "HTTP 401"},
			wantAlerts:  map[string]string{"provider:animetosho": "AnimeTosho rejected its credentials and was disabled after 3 attempts (HTTP 401). Fix the credentials in Settings, then Providers and save, or press Test."},
			wantPublish: events.ProviderRaise,
		},
		{
			name:        "enabled dismisses it",
			ev:          providergate.Event{Provider: "animetosho", Kind: providergate.Enabled},
			seed:        map[string]string{"provider:animetosho": "x"},
			wantAlerts:  map[string]string{},
			wantPublish: events.ProviderClear,
		},
		{
			name:        "inactive dismisses it",
			ev:          providergate.Event{Provider: "animetosho", Kind: providergate.Inactive},
			seed:        map[string]string{"provider:animetosho": "x"},
			wantAlerts:  map[string]string{},
			wantPublish: events.ProviderClear,
		},
		{
			name:        "a rejected setting raises its own alert",
			ev:          providergate.Event{Provider: "animetosho", Kind: providergate.SettingRejected, Setting: "anidb_client_key"},
			wantAlerts:  map[string]string{"provider:animetosho:anidb_client_key": "AnimeTosho's AniDB client key was rejected; episode lookup is off and AnimeTosho searches by title. Fix or clear the key in Settings, then Providers."},
			wantPublish: events.ProviderRaise,
		},
		{
			name:        "a cleared setting dismisses only that alert",
			ev:          providergate.Event{Provider: "animetosho", Kind: providergate.SettingCleared, Setting: "anidb_client_key"},
			seed:        map[string]string{"provider:animetosho": "x", "provider:animetosho:anidb_client_key": "y"},
			wantAlerts:  map[string]string{"provider:animetosho": "x"},
			wantPublish: events.ProviderClear,
		},
		{
			name:        "a pause raises no alert",
			ev:          providergate.Event{Provider: "animetosho", Kind: providergate.Paused},
			wantAlerts:  map[string]string{},
			wantPublish: events.ProviderRaise,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			alerts := activity.NewAlertLog(100)
			for src, msg := range tt.seed {
				alerts.RecordPersistent(src, msg)
			}
			var published []string
			hook := NewProviderGateHook(alerts, label, func(op events.ProviderOp, id subflux.ProviderID) {
				published = append(published, fmt.Sprintf("%s %s", op, id))
			})

			hook(tt.ev)

			got := map[string]string{}
			for _, a := range alerts.VisibleAlerts() {
				got[a.Source] = a.Message
			}
			if !maps.Equal(got, tt.wantAlerts) {
				t.Errorf("alerts after %s = %v, want %v", tt.ev.Kind, got, tt.wantAlerts)
			}
			if want := []string{string(tt.wantPublish) + " animetosho"}; !slices.Equal(published, want) {
				t.Errorf("published after %s = %v, want %v", tt.ev.Kind, published, want)
			}
		})
	}
}
