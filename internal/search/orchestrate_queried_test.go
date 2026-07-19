package search

import (
	"context"
	"errors"
	"testing"

	"github.com/cplieger/subflux/internal/api"
	"github.com/cplieger/subflux/internal/scorer"
)

// fakeHealth is a ProviderHealth stub with a fixed timed-out answer, for
// pinning the Queried semantics when the health timeout suppresses the sweep.
type fakeHealth struct{ timedOut bool }

func (f fakeHealth) IsTimedOut(api.ProviderID) bool              { return f.timedOut }
func (fakeHealth) RecordSuccess(api.ProviderID)                  {}
func (fakeHealth) RecordFailure(api.ProviderID, error)           {}
func (fakeHealth) Status() map[api.ProviderID]api.ProviderStatus { return nil }
func (fakeHealth) Reset()                                        {}

// --- LangOutcome.Queried / SearchResult.ProviderQueried ---
//
// The inter-item scan pacing delay keys on real provider traffic, so the
// Queried signal must be positive exactly when the sweep issued requests:
// a group whose providers all ERRORED still queried them (pacing applies
// during provider-error storms), while a skipped, backed-off, or fully
// health-timed-out group issued nothing.

// A searched group counts its issued provider queries, including failed ones.
func TestSearchTargets_queried_counts_provider_traffic(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		provider *mockProvider
	}{
		"provider answered with no results": {provider: &mockProvider{name: "test", results: nil}},
		"provider errored":                  {provider: &mockProvider{name: "test", searchErr: errors.New("boom")}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			ms := &mockStore{}
			mc := &mockConfig{searchCfg: api.SearchConfig{}}
			e := newEngine([]api.Provider{tc.provider}, ms, mc, nil,
				scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

			req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"}
			result, err := e.SearchTargets(context.Background(), req, "",
				[]api.SubtitleTarget{{Code: "fr"}})
			if err != nil {
				t.Fatalf("SearchTargets() unexpected error: %v", err)
			}
			if !result.ProviderQueried() {
				t.Errorf("ProviderQueried() = false, want true (a query was issued)")
			}
			if len(result.Langs) != 1 || result.Langs[0].Queried != 1 {
				t.Errorf("Langs[0].Queried = %+v, want 1", result.Langs)
			}
		})
	}
}

// A manually locked (skipped) group issues no query and reports none.
func TestSearchTargets_queried_zero_when_skipped(t *testing.T) {
	t.Parallel()
	ms := &mockStore{manualLocked: true}
	mc := &mockConfig{searchCfg: api.SearchConfig{}}
	p := &mockProvider{name: "test"}
	e := newEngine([]api.Provider{p}, ms, mc, nil,
		scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"}
	result, err := e.SearchTargets(context.Background(), req, "",
		[]api.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if result.ProviderQueried() {
		t.Errorf("ProviderQueried() = true for a fully skipped item, want false")
	}
}

// An adaptive-backoff group never reaches the sweep and reports no query.
func TestSearchTargets_queried_zero_when_backed_off(t *testing.T) {
	t.Parallel()
	backedStore := &mockStoreWithBackoff{
		mockStore: mockStore{},
		backedOff: []api.ProviderID{"test"},
	}
	mc := &mockConfig{
		adaptiveCfg: api.AdaptiveConfig{Enabled: true, MaxAttempts: 5},
		searchCfg:   api.SearchConfig{},
	}
	p := &mockProvider{name: "test"}
	e := newEngine([]api.Provider{p}, backedStore, mc, &mockMetrics{},
		scorer.New(&api.DefaultScores), Syncer{}, noopDetector{})

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"}
	result, err := e.SearchTargets(context.Background(), req, "",
		[]api.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if result.ProviderQueried() {
		t.Errorf("ProviderQueried() = true for a backed-off item, want false")
	}
}

// When every eligible provider is suppressed by the health timeout, the
// group is classified searched (variant targets were processed) but the
// sweep issued zero requests: Queried must be 0 so the pacing delay is
// skipped — the exact case where keying on TargetsSearched() would sleep
// for nothing.
func TestSearchTargets_queried_zero_when_all_providers_timed_out(t *testing.T) {
	t.Parallel()
	p := &mockProvider{name: "test"}
	e := New([]api.Provider{p},
		WithStore(&mockStore{}),
		WithConfig(&mockConfig{searchCfg: api.SearchConfig{}}),
		WithScorer(scorer.New(&api.DefaultScores)),
		WithSyncer(Syncer{}),
		WithTracks(noopDetector{}),
		WithTimeout(fakeHealth{timedOut: true}))

	req := &api.SearchRequest{MediaType: "movie", ImdbID: "tt123"}
	result, err := e.SearchTargets(context.Background(), req, "",
		[]api.SubtitleTarget{{Code: "fr"}})
	if err != nil {
		t.Fatalf("SearchTargets() unexpected error: %v", err)
	}
	if result.TargetsSearched() == 0 {
		t.Fatalf("TargetsSearched() = 0, want > 0 (the group ran; only the sweep was suppressed)")
	}
	if result.ProviderQueried() {
		t.Errorf("ProviderQueried() = true with every provider health-timed-out, want false")
	}
}
