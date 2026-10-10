package search

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/search/providerhealth"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

var errRefused = &subflux.AuthError{Msg: "HDBits refused the username and passkey (status 5: Auth failed)"}

// gateClock is a settable clock for the provider gate.
type gateClock struct {
	now time.Time
	mu  sync.Mutex
}

func (c *gateClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *gateClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// gateMetrics records the provider_disabled gauge.
type gateMetrics struct {
	disabled map[subflux.ProviderID]bool
	mu       sync.Mutex
}

func (m *gateMetrics) SetProviderDisabled(id subflux.ProviderID, v bool) {
	m.mu.Lock()
	m.disabled[id] = v
	m.mu.Unlock()
}
func (*gateMetrics) DeleteProviderDisabled(subflux.ProviderID)                   {}
func (*gateMetrics) IncProviderAuthFailure(subflux.ProviderID)                   {}
func (*gateMetrics) IncProviderRateLimited(subflux.ProviderID, providergate.Op)  {}
func (*gateMetrics) SetProviderSettingRejected(subflux.ProviderID, string, bool) {}
func (m *gateMetrics) isDisabled(id subflux.ProviderID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disabled[id]
}

type liveGate struct {
	gate    *providergate.Gate
	binding *providergate.Binding
	clock   *gateClock
	metrics *gateMetrics
}

// newLiveGate binds and activates every named provider with placeholder
// settings over a memory-only gate on a settable clock.
func newLiveGate(t *testing.T, names ...subflux.ProviderID) *liveGate {
	t.Helper()
	lg := &liveGate{
		clock:   &gateClock{now: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)},
		metrics: &gateMetrics{disabled: make(map[subflux.ProviderID]bool)},
	}
	g, err := providergate.Open(t.Context(), providergate.Config{Metrics: lg.metrics, Now: lg.clock.Now})
	if err != nil {
		t.Fatalf("providergate.Open: %v", err)
	}
	settings := make(map[subflux.ProviderID]map[string]any, len(names))
	for _, n := range names {
		settings[n] = map[string]any{"api_key": "placeholder-" + string(n)}
	}
	lg.gate = g
	lg.binding = g.Bind(settings)
	g.Activate(lg.binding)
	g.Reconcile(t.Context())
	return lg
}

// countingProvider answers every call with fixed values and counts them.
type countingProvider struct {
	searchErr   error
	downloadErr error
	name        subflux.ProviderID
	results     []subflux.Subtitle
	searches    atomic.Int32
	downloads   atomic.Int32
	counts      atomic.Int32
	count       int
	countErr    error
	rejected    string
}

func (p *countingProvider) Name() subflux.ProviderID { return p.name }
func (p *countingProvider) Search(context.Context, *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	p.searches.Add(1)
	return p.results, p.searchErr
}

func (p *countingProvider) Download(context.Context, *subflux.Subtitle) ([]byte, error) {
	p.downloads.Add(1)
	return []byte("1\n00:00:01,000 --> 00:00:02,000\nHello\n\n"), p.downloadErr
}

// countingCounter is a countingProvider that also counts show subtitles.
type countingCounter struct{ *countingProvider }

func (c countingCounter) CountShowSubtitles(context.Context, subflux.ShowSubtitleQuery) (int, error) {
	c.counts.Add(1)
	return c.count, c.countErr
}

// rejectingProvider reports one refused optional setting.
type rejectingProvider struct{ *countingProvider }

func (r rejectingProvider) SettingVerdict() (string, error) {
	if r.rejected == "" {
		return "", nil
	}
	return r.rejected, errors.New("client version missing or invalid")
}

func (r rejectingProvider) ForgetSettingVerdict() { r.rejected = "" }

// latchingProvider asks its upstream about one optional setting on every
// search until the upstream refuses it, then holds the refusal and searches
// without it until told to forget, the way AnimeTosho holds AniDB's answer
// about its client key.
type latchingProvider struct {
	*countingProvider
	refusal         error
	settingRequests atomic.Int32
	upstreamRefuses atomic.Bool
	mu              sync.Mutex
	answered        bool
}

func newLatchingProvider(refuses bool) *latchingProvider {
	p := &latchingProvider{countingProvider: &countingProvider{name: "animetosho"}}
	p.upstreamRefuses.Store(refuses)
	return p
}

func (p *latchingProvider) Search(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	p.mu.Lock()
	if p.refusal == nil {
		p.settingRequests.Add(1)
		p.answered = true
		if p.upstreamRefuses.Load() {
			p.refusal = errors.New("client version missing or invalid")
		}
	}
	p.mu.Unlock()
	return p.countingProvider.Search(ctx, req)
}

func (p *latchingProvider) SettingVerdict() (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.answered {
		return "", nil
	}
	return "anidb_client_key", p.refusal
}

func (p *latchingProvider) ForgetSettingVerdict() {
	p.mu.Lock()
	p.answered, p.refusal = false, nil
	p.mu.Unlock()
}

// recordingHealth counts what the engine reports to the health tracker.
type recordingHealth struct {
	failures atomic.Int32
	fakeHealth
}

func (h *recordingHealth) RecordFailure(subflux.ProviderID, error) { h.failures.Add(1) }

func gatedEngine(t *testing.T, gate *liveGate, health providerHealth, providers ...provider.Provider) *Engine {
	t.Helper()
	opts := []Option{
		WithStore(&mockStore{}), WithConfig(&mockConfig{}), WithScorer(scorer.New(&subflux.DefaultScores)),
		WithSyncer(Syncer{}), WithTracks(noopDetector{}), WithProviderGate(gate.binding),
		WithMediaWriter(testsupport.MediaWriter()),
	}
	if health != nil {
		opts = append(opts, WithTimeout(health))
	}
	return New(providers, opts...)
}

func movieReq(i int) *subflux.SearchRequest {
	return &subflux.SearchRequest{MediaType: subflux.MediaTypeMovie, TmdbID: 1000 + i, Title: fmt.Sprintf("Movie %d", i)}
}

func TestSearchTargets_a_refused_credential_reaches_the_provider_only_per_the_ladder(t *testing.T) {
	t.Parallel()
	gate := newLiveGate(t, "hdbits")
	p := &countingProvider{name: "hdbits", searchErr: errRefused}
	health := &recordingHealth{}
	e := gatedEngine(t, gate, health, p)

	steps := []time.Duration{0, time.Minute, 4 * time.Minute, time.Minute, 30 * time.Minute, time.Hour, time.Hour, 24 * time.Hour, time.Hour, time.Hour}
	for i, step := range steps {
		gate.clock.Advance(step)
		if _, err := e.SearchTargets(t.Context(), movieReq(i), "", []subflux.SubtitleTarget{{Code: "en"}}); err != nil {
			t.Fatalf("SearchTargets(item %d) error = %v", i, err)
		}
	}
	if got := p.searches.Load(); got != 3 {
		t.Errorf("provider searches over 10 items = %d, want 3 (one call, two probes)", got)
	}
	if !gate.metrics.isDisabled("hdbits") {
		t.Error("provider_disabled{hdbits} = 0, want 1")
	}
	if got := health.failures.Load(); got != 0 {
		t.Errorf("health tracker failures = %d, want 0 for a refused credential", got)
	}
}

func TestSearchTargets_transient_failures_never_disable_and_still_trip_health(t *testing.T) {
	t.Parallel()
	gate := newLiveGate(t, "hdbits")
	p := &countingProvider{name: "hdbits", searchErr: errors.New("hdbits: HTTP 502")}
	tracker := providerhealth.New(providerhealth.Config{Cooldown: time.Hour})
	e := gatedEngine(t, gate, tracker, p)
	for i := range providerhealth.DefaultThreshold {
		if _, err := e.SearchTargets(t.Context(), movieReq(i), "", []subflux.SubtitleTarget{{Code: "en"}}); err != nil {
			t.Fatalf("SearchTargets(item %d) error = %v", i, err)
		}
	}
	if ok, reason := gate.binding.Admit("hdbits", providergate.OpSearch); !ok {
		t.Errorf("Admit(hdbits) after transient failures = false (%q), want true", reason)
	}
	if !tracker.IsTimedOut("hdbits") {
		t.Error("health tracker did not time hdbits out after the threshold of transient failures")
	}
}

func TestSearchTargets_a_gated_provider_is_not_queried_and_not_counted(t *testing.T) {
	t.Parallel()
	gate := newLiveGate(t, "hdbits")
	gate.binding.Observe(t.Context(), "hdbits", providergate.OpSearch, errRefused)
	p := &countingProvider{name: "hdbits"}
	e := gatedEngine(t, gate, nil, p)

	result, err := e.SearchTargets(t.Context(), movieReq(0), "", []subflux.SubtitleTarget{{Code: "en"}})
	if err != nil {
		t.Fatalf("SearchTargets() error = %v", err)
	}
	if p.searches.Load() != 0 || result.ProviderQueried() {
		t.Errorf("searches = %d, ProviderQueried() = %v; want 0 and false for a gated provider", p.searches.Load(), result.ProviderQueried())
	}
}

func TestDownloadFromProvider_refused_download_makes_no_request(t *testing.T) {
	t.Parallel()
	gate := newLiveGate(t, "opensubtitles")
	gate.binding.Observe(t.Context(), "opensubtitles", providergate.OpDownload, &subflux.RateLimitError{Msg: "download limit exceeded (406)"})
	p := &countingProvider{name: "opensubtitles"}
	e := gatedEngine(t, gate, nil, p)

	_, err := e.Download(t.Context(), &subflux.Subtitle{Provider: "opensubtitles", ID: "1"})
	if !errors.Is(err, ErrProviderGated) || !strings.Contains(err.Error(), "rate limited until") {
		t.Errorf("Download() = %v, want ErrProviderGated with the gate's reason", err)
	}
	if got := p.downloads.Load(); got != 0 {
		t.Errorf("provider downloads = %d, want 0", got)
	}
}

func TestDownload_rate_limit_is_observed_by_the_gate(t *testing.T) {
	t.Parallel()
	gate := newLiveGate(t, "opensubtitles")
	p := &countingProvider{name: "opensubtitles", downloadErr: &subflux.RateLimitError{Msg: "406", RetryAfter: time.Hour}}
	e := gatedEngine(t, gate, nil, p)
	if _, err := e.Download(t.Context(), &subflux.Subtitle{Provider: "opensubtitles", ID: "1"}); err == nil {
		t.Fatal("Download() = nil, want the rate limit")
	}
	if ok, _ := gate.binding.Admit("opensubtitles", providergate.OpDownload); ok {
		t.Error("Admit(download) after a rate limit = true, want the download paused")
	}
	if ok, _ := gate.binding.Admit("opensubtitles", providergate.OpSearch); !ok {
		t.Error("Admit(search) after a download rate limit = false, want search unaffected")
	}
}

func TestSearchProvider_reports_a_rejected_optional_setting(t *testing.T) {
	t.Parallel()
	gate := newLiveGate(t, "animetosho")
	p := rejectingProvider{&countingProvider{name: "animetosho", rejected: "anidb_client_key"}}
	e := gatedEngine(t, gate, nil, p)
	if _, err := e.SearchTargets(t.Context(), movieReq(0), "", []subflux.SubtitleTarget{{Code: "en"}}); err != nil {
		t.Fatalf("SearchTargets() error = %v", err)
	}
	st := gate.binding.Status()["animetosho"]
	if len(st.RejectedSettings) != 1 || st.RejectedSettings[0] != "anidb_client_key" || st.Disabled {
		t.Errorf("gate status = %+v, want anidb_client_key rejected and the provider enabled", st)
	}
}

// latchedAnimeTosho is an engine over a latchingProvider whose upstream
// refused the setting on the first of two searches, and the gate's event kinds
// from then on; the upstream accepts the setting from now on.
type latchedAnimeTosho struct {
	gate   *liveGate
	engine *Engine
	p      *latchingProvider
	kinds  func() []providergate.Kind
}

func newLatchedAnimeTosho(t *testing.T) *latchedAnimeTosho {
	t.Helper()
	gate := newLiveGate(t, "animetosho")
	var (
		mu    sync.Mutex
		kinds []providergate.Kind
	)
	gate.gate.SetOnChange(func(ev providergate.Event) {
		mu.Lock()
		kinds = append(kinds, ev.Kind)
		mu.Unlock()
	})
	l := &latchedAnimeTosho{
		gate: gate,
		p:    newLatchingProvider(true),
		kinds: func() []providergate.Kind {
			mu.Lock()
			defer mu.Unlock()
			return slices.Clone(kinds)
		},
	}
	l.engine = gatedEngine(t, gate, nil, l.p)
	l.search(t, 0)
	l.search(t, 1)
	if got := l.p.settingRequests.Load(); got != 1 {
		t.Fatalf("Setup: setting requests = %d, want 1 (the refusal is held)", got)
	}
	l.p.upstreamRefuses.Store(false)
	return l
}

func (l *latchedAnimeTosho) search(t *testing.T, i int) {
	t.Helper()
	if _, err := l.engine.SearchTargets(t.Context(), movieReq(i), "", []subflux.SubtitleTarget{{Code: "en"}}); err != nil {
		t.Fatalf("SearchTargets(item %d) error = %v", i, err)
	}
}

func TestSearchProvider_a_passing_credential_test_makes_the_instance_ask_again(t *testing.T) {
	t.Parallel()
	l := newLatchedAnimeTosho(t)
	if got := l.gate.gate.ClearIfMatches(t.Context(), "animetosho",
		map[string]any{"api_key": "placeholder-animetosho"}); got != providergate.Cleared {
		t.Fatalf("ClearIfMatches(live settings) = %v, want Cleared", got)
	}
	l.search(t, 2)

	if got := l.p.settingRequests.Load(); got != 2 {
		t.Errorf("setting requests after the test = %d, want 2 (the upstream is asked again)", got)
	}
	if got, want := l.kinds(), []providergate.Kind{providergate.SettingRejected, providergate.SettingCleared}; !slices.Equal(got, want) {
		t.Errorf("gate events = %v, want %v and no second rejection", got, want)
	}
	if st := l.gate.binding.Status()["animetosho"]; len(st.RejectedSettings) != 0 {
		t.Errorf("gate status after the test = %+v, want no rejected setting", st)
	}
}

func TestSearchProvider_a_reset_makes_the_instance_ask_again(t *testing.T) {
	t.Parallel()
	l := newLatchedAnimeTosho(t)
	l.engine.ResetProviderState(t.Context())
	l.search(t, 2)

	if got := l.p.settingRequests.Load(); got != 2 {
		t.Errorf("setting requests after the reset = %d, want 2 (the upstream is asked again)", got)
	}
	if got, want := l.kinds(), []providergate.Kind{providergate.SettingRejected, providergate.SettingCleared}; !slices.Equal(got, want) {
		t.Errorf("gate events = %v, want %v and no second rejection", got, want)
	}
	if st := l.gate.binding.Status()["animetosho"]; len(st.RejectedSettings) != 0 {
		t.Errorf("gate status after the reset = %+v, want no rejected setting", st)
	}
}

// rebind activates a second binding for the gate's unchanged settings, the
// way a settings save rebuilds every provider, and returns an engine over p
// bound to it.
func (l *latchedAnimeTosho) rebind(t *testing.T, p provider.Provider) *Engine {
	t.Helper()
	next := *l.gate
	next.binding = l.gate.gate.Bind(map[subflux.ProviderID]map[string]any{
		"animetosho": {"api_key": "placeholder-animetosho"},
	})
	next.gate.Activate(next.binding)
	next.gate.Reconcile(t.Context())
	return gatedEngine(t, &next, nil, p)
}

func searchWith(t *testing.T, e *Engine, i int) {
	t.Helper()
	if _, err := e.SearchTargets(t.Context(), movieReq(i), "", []subflux.SubtitleTarget{{Code: "en"}}); err != nil {
		t.Fatalf("SearchTargets(item %d) error = %v", i, err)
	}
}

func TestSearchProvider_an_unchanged_save_clears_a_rejection_the_upstream_no_longer_makes(t *testing.T) {
	t.Parallel()
	l := newLatchedAnimeTosho(t)
	fresh := newLatchingProvider(false)
	searchWith(t, l.rebind(t, fresh), 2)

	if st := l.gate.gate.Status()["animetosho"]; len(st.RejectedSettings) != 0 {
		t.Errorf("gate status after the new instance's accepted lookup = %+v, want no rejected setting", st)
	}
	if got, want := l.kinds(), []providergate.Kind{providergate.SettingRejected, providergate.SettingCleared}; !slices.Equal(got, want) {
		t.Errorf("gate events = %v, want %v", got, want)
	}
	l.search(t, 3)
	if got := l.p.settingRequests.Load(); got != 2 {
		t.Errorf("outgoing instance's setting requests after the acceptance = %d, want 2 (it asks again)", got)
	}
}

func TestSearchProvider_an_unchanged_save_keeps_a_rejection_the_upstream_still_makes(t *testing.T) {
	t.Parallel()
	l := newLatchedAnimeTosho(t)
	l.p.upstreamRefuses.Store(true)
	fresh := newLatchingProvider(true)
	searchWith(t, l.rebind(t, fresh), 2)
	l.search(t, 3)

	st := l.gate.gate.Status()["animetosho"]
	if !slices.Equal(st.RejectedSettings, []string{"anidb_client_key"}) {
		t.Errorf("gate status after the new instance's refused lookup = %+v, want anidb_client_key rejected", st)
	}
	if got, want := l.kinds(), []providergate.Kind{providergate.SettingRejected}; !slices.Equal(got, want) {
		t.Errorf("gate events = %v, want %v and no clear in between", got, want)
	}
	if got := fresh.settingRequests.Load(); got != 1 {
		t.Errorf("new instance's setting requests = %d, want 1", got)
	}
}

func TestClearIfMatches_makes_every_instance_of_the_settings_ask_again(t *testing.T) {
	t.Parallel()
	l := newLatchedAnimeTosho(t)
	l.p.upstreamRefuses.Store(true)
	live := newLatchingProvider(true)
	liveEngine := l.rebind(t, live)
	searchWith(t, liveEngine, 2)
	l.search(t, 3)
	asked := l.p.settingRequests.Load()

	if got := l.gate.gate.ClearIfMatches(t.Context(), "animetosho",
		map[string]any{"api_key": "placeholder-animetosho"}); got != providergate.Cleared {
		t.Fatalf("ClearIfMatches(live settings) = %v, want Cleared", got)
	}
	l.p.upstreamRefuses.Store(false)
	live.upstreamRefuses.Store(false)
	searchWith(t, liveEngine, 4)
	l.search(t, 5)

	if got := live.settingRequests.Load(); got != 2 {
		t.Errorf("live instance's setting requests after the test = %d, want 2 (it asks again)", got)
	}
	if got := l.p.settingRequests.Load(); got != asked+1 {
		t.Errorf("outgoing instance's setting requests after the test = %d, want %d (it asks again)", got, asked+1)
	}
	if st := l.gate.gate.Status()["animetosho"]; len(st.RejectedSettings) != 0 {
		t.Errorf("gate status after the test = %+v, want no rejected setting", st)
	}
}

func TestSweepProviders_reports_a_gated_and_an_erroring_provider(t *testing.T) {
	t.Parallel()
	gate := newLiveGate(t, "hdbits", "subdl", "gestdown")
	gate.binding.Observe(t.Context(), "hdbits", providergate.OpSearch, errRefused)
	gated := &countingProvider{name: "hdbits"}
	failing := &countingProvider{name: "subdl", searchErr: errors.New("subdl: HTTP 502")}
	answering := &countingProvider{name: "gestdown", results: []subflux.Subtitle{{Provider: "gestdown", ID: "7"}}}
	e := gatedEngine(t, gate, fakeHealth{timedOut: true}, answering, gated, failing)

	subs, notices := e.SweepProviders(t.Context(), movieReq(0), time.Second)
	if len(subs) != 1 || subs[0].ID != "7" {
		t.Errorf("SweepProviders() results = %v, want gestdown's one result despite the health timeout", subs)
	}
	if gated.searches.Load() != 0 {
		t.Errorf("gated provider searches = %d, want 0", gated.searches.Load())
	}
	if len(notices) != 2 || notices[0].Provider != "hdbits" || !notices[0].Gated ||
		!strings.Contains(notices[0].Reason, "next attempt is in 5m") || notices[1].Provider != "subdl" || notices[1].Err == nil {
		t.Errorf("SweepProviders() notices = %+v, want a gated hdbits and an erroring subdl", notices)
	}
}

func TestCountShowSubtitles_goes_through_the_gate(t *testing.T) {
	t.Parallel()
	t.Run("gated counter makes no request", func(t *testing.T) {
		gate := newLiveGate(t, "opensubtitles")
		gate.binding.Observe(t.Context(), "opensubtitles", providergate.OpSearch, errRefused)
		c := countingCounter{&countingProvider{name: "opensubtitles", count: 40}}
		e := gatedEngine(t, gate, nil, c)
		_, err := e.CountShowSubtitles(t.Context(), subflux.ShowSubtitleQuery{ImdbID: "tt4396196", Language: "en"})
		if !errors.Is(err, ErrProviderGated) || c.counts.Load() != 0 {
			t.Errorf("CountShowSubtitles() = %v after %d counts, want ErrProviderGated and 0", err, c.counts.Load())
		}
	})
	t.Run("a refused credential is observed", func(t *testing.T) {
		gate := newLiveGate(t, "opensubtitles")
		c := countingCounter{&countingProvider{name: "opensubtitles", countErr: errRefused}}
		e := gatedEngine(t, gate, nil, c)
		if _, err := e.CountShowSubtitles(t.Context(), subflux.ShowSubtitleQuery{ImdbID: "tt4396196", Language: "en"}); err == nil {
			t.Fatal("CountShowSubtitles() = nil, want the refusal")
		}
		if got := gate.binding.Status()["opensubtitles"].AuthFailures; got != 1 {
			t.Errorf("gate AuthFailures after a refused count = %d, want 1", got)
		}
	})
	t.Run("the first counting provider answers", func(t *testing.T) {
		gate := newLiveGate(t, "aaa", "opensubtitles", "zzz")
		first := countingCounter{&countingProvider{name: "opensubtitles", count: 40}}
		second := countingCounter{&countingProvider{name: "zzz", count: 1}}
		e := gatedEngine(t, gate, nil, &countingProvider{name: "aaa"}, first, second)
		n, err := e.CountShowSubtitles(t.Context(), subflux.ShowSubtitleQuery{ImdbID: "tt4396196", Language: "en"})
		if !e.HasShowCounter() || err != nil || n != 40 || second.counts.Load() != 0 {
			t.Errorf("CountShowSubtitles() = (%d, %v), HasShowCounter %v, second counted %d; want (40, nil), true, 0",
				n, err, e.HasShowCounter(), second.counts.Load())
		}
	})
	t.Run("no counter", func(t *testing.T) {
		e := gatedEngine(t, newLiveGate(t, "subdl"), nil, &countingProvider{name: "subdl"})
		if e.HasShowCounter() {
			t.Error("HasShowCounter() = true with no counting provider")
		}
		if _, err := e.CountShowSubtitles(t.Context(), subflux.ShowSubtitleQuery{}); !errors.Is(err, errProviderNotFound) {
			t.Errorf("CountShowSubtitles() without a counter = %v, want ErrProviderNotFound", err)
		}
	})
}

// heldProvider holds its one search until released.
type heldProvider struct {
	*countingProvider
	entered chan struct{}
	release chan struct{}
}

func (p heldProvider) Search(ctx context.Context, req *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	close(p.entered)
	<-p.release
	return p.countingProvider.Search(ctx, req)
}

func TestSearchProvidersFiltered_a_provider_waiting_for_a_slot_is_admitted_when_it_gets_one(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		gate := newLiveGate(t, "gestdown", "hdbits")
		for _, wait := range []time.Duration{0, 5*time.Minute + time.Second} {
			gate.clock.Advance(wait)
			if ok, reason := gate.binding.Admit("hdbits", providergate.OpSearch); !ok {
				t.Fatalf("Setup: Admit(hdbits) = false (%q), want the ladder's next probe", reason)
			}
			gate.binding.Observe(t.Context(), "hdbits", providergate.OpSearch, errRefused)
		}
		gate.clock.Advance(30*time.Minute + time.Second)

		held := heldProvider{&countingProvider{name: "gestdown"}, make(chan struct{}), make(chan struct{})}
		hdbits := &countingProvider{name: "hdbits"}
		e := New([]provider.Provider{held, hdbits},
			WithStore(&mockStore{}), WithConfig(&mockConfig{searchCfg: subflux.SearchConfig{MaxProviderConcurrency: 1}}),
			WithScorer(scorer.New(&subflux.DefaultScores)), WithSyncer(Syncer{}), WithTracks(noopDetector{}),
			WithProviderGate(gate.binding), WithMediaWriter(testsupport.MediaWriter()))
		done := make(chan searchOutcome, 1)
		go func() { done <- e.searchProvidersFilteredInner(t.Context(), movieReq(0), e.providers) }()
		<-held.entered
		synctest.Wait()

		gate.clock.Advance(3 * time.Minute)
		if ok, reason := gate.binding.Admit("hdbits", providergate.OpSearch); !ok {
			t.Fatalf("Setup: Admit(hdbits) from another operation = false (%q), want the third probe", reason)
		}
		gate.binding.Observe(t.Context(), "hdbits", providergate.OpSearch, errRefused)
		if !gate.binding.Status()["hdbits"].Disabled {
			t.Fatal("Setup: hdbits is not disabled after its third refusal")
		}
		close(held.release)
		out := <-done

		if got := hdbits.searches.Load(); got != 0 {
			t.Errorf("hdbits searches after another operation disabled it = %d, want 0", got)
		}
		i := slices.IndexFunc(out.providers, func(r providerResult) bool { return r.name == "hdbits" })
		if i < 0 || out.providers[i].outcome != providerGated {
			t.Errorf("searchProvidersFilteredInner() providers = %+v, want hdbits gated", out.providers)
		}
	})
}

func TestMergeProviderStatus_combines_both_halves(t *testing.T) {
	t.Parallel()
	got := mergeProviderStatus(
		subflux.ProviderStatus{TimedOut: true, RecentFailures: 5, Threshold: 5, LastError: "HTTP 502"},
		providergate.Status{Disabled: true, DisabledReason: "refused", AuthFailures: 3, PausedFor: time.Minute, RejectedSettings: []string{"anidb_client_key"}},
	)
	want := subflux.ProviderStatus{
		TimedOut: true, RecentFailures: 5, Threshold: 5, LastError: "HTTP 502",
		Disabled: true, DisabledReason: "refused", AuthFailures: 3, PausedFor: time.Minute, RejectedSettings: []string{"anidb_client_key"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("MergeProviderStatus() = %+v, want %+v", got, want)
	}
}
