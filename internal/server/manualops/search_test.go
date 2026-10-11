package manualops

import (
	"context"
	"errors"
	"maps"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/embedded"
	"github.com/cplieger/subflux/internal/obs"
	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/scorer"
	"github.com/cplieger/subflux/internal/search"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/search/syncing"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// fakeSearchEngine counts what a manual search asks the engine to do, so the
// tests can assert the work that did NOT happen: hashing a video the request
// already carries a hash for, or scoring an empty candidate set.
type fakeSearchEngine struct {
	hash       string
	swept      []subflux.Subtitle
	scored     []subflux.ScoredResult
	size       int64
	hashCalls  int
	scoreCalls int
}

func (e *fakeSearchEngine) SweepProviders(context.Context, *subflux.SearchRequest, time.Duration) ([]subflux.Subtitle, []search.SweepNotice) {
	return e.swept, nil
}

func (*fakeSearchEngine) Download(context.Context, *subflux.Subtitle) ([]byte, error) {
	return nil, nil
}

func (e *fakeSearchEngine) HashFile(_ context.Context, _ string) (string, int64, error) {
	e.hashCalls++
	return e.hash, e.size, nil
}

func (e *fakeSearchEngine) ScoreSubtitles(_ *subflux.SearchRequest, _ []subflux.Subtitle) []subflux.ScoredResult {
	e.scoreCalls++
	return e.scored
}

func (*fakeSearchEngine) SyncAndPostProcess(_ context.Context, data []byte, _, _ string, _ subflux.Variant) ([]byte, int64) {
	return data, 0
}

// Hashing a video is a full file read, so it happens once and only when it can
// change the search: with no resolved video path, or a request that already
// carries a hash, there is nothing to compute.
func TestTryComputeHash(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		filePath  string
		haveHash  string
		wantHash  string
		wantCalls int
	}{
		{
			name:      "a resolved path is hashed",
			filePath:  "/media/movie.mkv",
			wantHash:  "abc123",
			wantCalls: 1,
		},
		{
			name:      "no resolved path leaves the request unhashed",
			filePath:  "",
			wantHash:  "",
			wantCalls: 0,
		},
		{
			name:      "a request that already carries a hash is not rehashed",
			filePath:  "/media/movie.mkv",
			haveHash:  "fromclient",
			wantHash:  "fromclient",
			wantCalls: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			engine := &fakeSearchEngine{hash: "abc123", size: 4096}
			ls := &LiveState{Cfg: fakeManualCfg{}, Engine: engine}
			req := &subflux.SearchRequest{Title: "Show", VideoHash: tc.haveHash}

			tryComputeHash(t.Context(), ls, req, tc.filePath)

			if req.VideoHash != tc.wantHash {
				t.Errorf("TryComputeHash(path=%q, have=%q) VideoHash = %q, want %q",
					tc.filePath, tc.haveHash, req.VideoHash, tc.wantHash)
			}
			if engine.hashCalls != tc.wantCalls {
				t.Errorf("TryComputeHash(path=%q, have=%q) hashed %d times, want %d",
					tc.filePath, tc.haveHash, engine.hashCalls, tc.wantCalls)
			}
		})
	}
}

// A manual search that no provider answered has nothing to score and nothing
// to mark as on-disk, so neither the scorer nor the download history is
// consulted — and, since every provider error is already reported per
// provider, the pass itself stays silent.
func TestRunSearch_scores_and_looks_up_history_only_with_candidates(t *testing.T) {
	// No t.Parallel: these subtests swap the global slog default logger.
	scored := []subflux.ScoredResult{{
		Sub:   subflux.Subtitle{Provider: "os", ID: "sub-1", Language: "en", ReleaseName: "Show.S01E01"},
		Score: 80,
	}}

	t.Run("no_candidates", func(t *testing.T) {
		buf := captureLogs(t)
		engine := &fakeSearchEngine{scored: scored}
		store := &recStore{}
		ls := &LiveState{Cfg: fakeManualCfg{}, Engine: engine}
		req := &subflux.SearchRequest{Title: "Show", MediaType: subflux.MediaTypeMovie, TmdbID: 27205}

		got := RunSearch(t.Context(), &SearchDeps{DB: store}, ls, req, "en", subflux.MediaTypeMovie, "")

		if len(got.Results) != 0 {
			t.Errorf("RunSearch(no providers) results = %+v, want none", got.Results)
		}
		if engine.scoreCalls != 0 {
			t.Errorf("RunSearch(no providers) scored %d times, want 0", engine.scoreCalls)
		}
		if store.refsCalls != 0 {
			t.Errorf("RunSearch(no providers) looked up download history %d times, want 0", store.refsCalls)
		}
		if strings.Contains(buf.String(), "level=WARN") {
			t.Errorf("RunSearch(no providers) logged a warning; log was:\n%s", buf.String())
		}
	})

	t.Run("one_candidate", func(t *testing.T) {
		buf := captureLogs(t)
		engine := &fakeSearchEngine{swept: []subflux.Subtitle{scored[0].Sub}, scored: scored}
		store := &recStore{refs: []subflux.DownloadedRef{{Provider: "os", ReleaseName: "Show.S01E01"}}}
		ls := &LiveState{Cfg: fakeManualCfg{}, Engine: engine}
		req := &subflux.SearchRequest{Title: "Show", MediaType: subflux.MediaTypeMovie, TmdbID: 27205}

		got := RunSearch(t.Context(), &SearchDeps{DB: store}, ls, req, "en", subflux.MediaTypeMovie, "")

		if len(got.Results) != 1 {
			t.Fatalf("RunSearch(one candidate) results = %+v, want exactly one", got.Results)
		}
		if !got.Results[0].OnDisk {
			t.Error("RunSearch(one candidate) OnDisk = false, want true (the history row matches the release)")
		}
		if engine.scoreCalls != 1 || store.refsCalls != 1 {
			t.Errorf("RunSearch(one candidate) scored %d times and looked up history %d times, want 1 and 1",
				engine.scoreCalls, store.refsCalls)
		}
		if strings.Contains(buf.String(), "level=WARN") {
			t.Errorf("RunSearch(one candidate) logged a warning; log was:\n%s", buf.String())
		}
	})
}

// countingProvider counts its searches and answers each with err.
type countingProvider struct {
	err   error
	name  subflux.ProviderID
	calls atomic.Int32
}

func (p *countingProvider) Name() subflux.ProviderID { return p.name }

func (p *countingProvider) Search(context.Context, *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	p.calls.Add(1)
	return nil, p.err
}

func (*countingProvider) Download(context.Context, *subflux.Subtitle) ([]byte, error) {
	return nil, nil
}

// A manual search names every provider that contributed nothing and why: one
// the provider gate is holding back is skipped without a request, and one
// whose search failed reports its error, so an empty result list is never
// silent about a provider the operator expected to answer.
func TestRunSearch_names_gated_and_failed_providers(t *testing.T) {
	t.Parallel()
	paused := &countingProvider{name: "hdbits"}
	failing := &countingProvider{name: "subdl", err: errors.New("subdl: upstream answered 500")}

	g, err := providergate.Open(t.Context(), providergate.Config{Metrics: testsupport.NopGateMetrics{}})
	if err != nil {
		t.Fatalf("providergate.Open: %v", err)
	}
	binding := g.Bind(map[subflux.ProviderID]map[string]any{
		"hdbits": {"username": "placeholder-user"}, "subdl": {"api_key": "placeholder-api-key"},
	})
	g.Activate(binding)
	binding.Observe(t.Context(), "hdbits", providergate.OpSearch, &subflux.RateLimitError{Msg: "rate limited"})

	cfg := fakeManualCfg{}
	scores := cfg.Scores()
	engine := search.New([]provider.Provider{paused, failing},
		search.WithStore(&testsupport.NopStore{}), search.WithConfig(cfg),
		search.WithMetrics(obs.New()), search.WithScorer(scorer.New(&scores)),
		search.WithSyncer(syncing.Syncer{}),
		search.WithTracks(embedded.Detector{}),
		search.WithProviderGate(binding), search.WithMediaWriter(testsupport.MediaWriter()))
	ls := &LiveState{Cfg: cfg, Engine: engine}
	req := &subflux.SearchRequest{Title: "Show", MediaType: subflux.MediaTypeMovie, TmdbID: 27205}

	got := RunSearch(t.Context(), &SearchDeps{DB: &recStore{}}, ls, req, "en", subflux.MediaTypeMovie, "")

	kinds := map[subflux.ProviderID]string{}
	for _, n := range got.Providers {
		kinds[n.Provider] = n.Kind
		if n.Message == "" {
			t.Errorf("RunSearch() notice for %s has no message", n.Provider)
		}
	}
	want := map[subflux.ProviderID]string{"hdbits": noticeGated, "subdl": noticeError}
	if !maps.Equal(kinds, want) {
		t.Errorf("RunSearch() provider notices = %+v, want kinds %v", got.Providers, want)
	}
	if n := paused.calls.Load(); n != 0 {
		t.Errorf("RunSearch() searched the paused provider %d times, want 0", n)
	}
}
