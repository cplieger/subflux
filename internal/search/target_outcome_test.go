package search

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/search/providergate"
	"github.com/cplieger/subflux/internal/subflux"
	"github.com/cplieger/subflux/internal/testsupport"
)

// rankedConfig orders equal-score candidates by an explicit provider rank and
// turns adaptive backoff on.
type rankedConfig struct {
	rank map[subflux.ProviderID]int
	mockConfig
}

func (c *rankedConfig) ProviderPriority(id subflux.ProviderID) int { return c.rank[id] }

func newRankedConfig(maxAttempts int, ids ...subflux.ProviderID) *rankedConfig {
	c := &rankedConfig{rank: make(map[subflux.ProviderID]int, len(ids))}
	for i, id := range ids {
		c.rank[id] = i + 1
	}
	c.searchCfg = subflux.SearchConfig{DownloadMaxAttempts: maxAttempts}
	c.adaptiveCfg = subflux.AdaptiveConfig{Enabled: true, InitialDelay: time.Hour, MaxDelay: time.Hour, BackoffMultiplier: 2}
	return c
}

// resumeStore records the resume stamps and backoff writes a search makes.
type resumeStore struct {
	stamps []subflux.ScanRecord
	mockStore
}

func (s *resumeStore) RecordScanState(_ context.Context, rec *subflux.ScanRecord) error {
	s.stamps = append(s.stamps, *rec)
	return nil
}

// candidates returns n search results from id, all matched by IMDb.
func candidates(id subflux.ProviderID, n int) []subflux.Subtitle {
	out := make([]subflux.Subtitle, n)
	for i := range out {
		out[i] = subflux.Subtitle{Provider: id, ID: string(id) + string(rune('a'+i)), ReleaseName: "Movie-GRP", MatchedBy: subflux.MatchByIMDB, Language: "fr"}
	}
	return out
}

type outcomeRig struct {
	engine *Engine
	gate   *liveGate
	store  *resumeStore
}

func newOutcomeRig(t *testing.T, cfg Cfg, providers ...*countingProvider) *outcomeRig {
	t.Helper()
	names := make([]subflux.ProviderID, len(providers))
	ps := make([]provider.Provider, len(providers))
	for i, p := range providers {
		names[i], ps[i] = p.name, p
	}
	r := &outcomeRig{gate: newLiveGate(t, names...), store: &resumeStore{}}
	r.engine = New(ps, WithStore(r.store), WithConfig(cfg), WithScorer(fixedScorer{score: 10}),
		WithSyncer(&recordingSyncer{}), WithTracks(noopDetector{}), WithProviderGate(r.gate.binding), WithMediaWriter(testsupport.MediaWriter()))
	return r
}

func (r *outcomeRig) search(t *testing.T, targets ...subflux.SubtitleTarget) subflux.SearchResult {
	t.Helper()
	if len(targets) == 0 {
		targets = []subflux.SubtitleTarget{{Code: "fr"}}
	}
	videoPath := filepath.Join(t.TempDir(), "movie.mkv")
	result, err := r.engine.SearchTargets(t.Context(),
		&subflux.SearchRequest{MediaType: subflux.MediaTypeMovie, ImdbID: "tt0111161", ReleaseName: "Movie-GRP"},
		videoPath, targets)
	if err != nil {
		t.Fatalf("SearchTargets() error = %v", err)
	}
	return result
}

func TestDownloadBestCandidate_a_gated_candidate_costs_no_attempt(t *testing.T) {
	t.Parallel()
	paused := &countingProvider{name: "opensubtitles", results: candidates("opensubtitles", 2)}
	other := &countingProvider{name: "subdl", results: candidates("subdl", 1)}
	r := newOutcomeRig(t, newRankedConfig(1, "opensubtitles", "subdl"), paused, other)
	r.gate.binding.Observe(t.Context(), "opensubtitles", providergate.OpDownload, &subflux.RateLimitError{Msg: "406"})

	result := r.search(t)
	if len(result.Paths()) != 1 || paused.downloads.Load() != 0 || other.downloads.Load() != 1 {
		t.Errorf("paths = %v, gated downloads = %d, other downloads = %d; want one save from subdl and no gated request",
			result.Paths(), paused.downloads.Load(), other.downloads.Load())
	}
}

func TestDownloadBestCandidate_a_refusal_skips_the_provider_without_costing_an_attempt(t *testing.T) {
	t.Parallel()
	for name, err := range map[string]error{
		"auth":       errRefused,
		"rate limit": &subflux.RateLimitError{Msg: "download limit exceeded (406)"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			refusing := &countingProvider{name: "opensubtitles", results: candidates("opensubtitles", 2), downloadErr: err}
			other := &countingProvider{name: "subdl", results: candidates("subdl", 1)}
			r := newOutcomeRig(t, newRankedConfig(1, "opensubtitles", "subdl"), refusing, other)

			result := r.search(t)
			if len(result.Paths()) != 1 || refusing.downloads.Load() != 1 || other.downloads.Load() != 1 {
				t.Errorf("paths = %v, refusing downloads = %d, other downloads = %d; want one refused call then a save from subdl",
					result.Paths(), refusing.downloads.Load(), other.downloads.Load())
			}
		})
	}
}

func TestDownloadBestCandidate_an_ordinary_failure_costs_an_attempt(t *testing.T) {
	t.Parallel()
	failing := &countingProvider{name: "opensubtitles", results: candidates("opensubtitles", 2), downloadErr: errors.New("HTTP 502")}
	other := &countingProvider{name: "subdl", results: candidates("subdl", 1)}
	r := newOutcomeRig(t, newRankedConfig(1, "opensubtitles", "subdl"), failing, other)

	result := r.search(t)
	if len(result.Paths()) != 0 || result.Langs[0].Failed != 1 || failing.downloads.Load() != 1 || other.downloads.Load() != 0 {
		t.Errorf("paths = %v, failed = %d, failing downloads = %d, other downloads = %d; want the one attempt spent and nothing saved",
			result.Paths(), result.Langs[0].Failed, failing.downloads.Load(), other.downloads.Load())
	}
}

func TestSearchTargets_a_failed_download_records_no_backoff_and_no_stamp(t *testing.T) {
	t.Parallel()
	failing := &countingProvider{name: "subdl", results: candidates("subdl", 1), downloadErr: errors.New("HTTP 502")}
	r := newOutcomeRig(t, newRankedConfig(3, "subdl"), failing)

	result := r.search(t, subflux.SubtitleTarget{Code: "fr"}, subflux.SubtitleTarget{Code: "fr", Variant: subflux.VariantForced})
	if got := result.Langs[0]; got.Failed != 1 || got.Answered != 1 {
		t.Fatalf("LangOutcome = %+v, want one failed target and one answering provider", got)
	}
	if r.store.failureCalled {
		t.Error("adaptive backoff recorded although a candidate failed to download")
	}
	if len(r.store.stamps) != 0 {
		t.Errorf("resume stamps = %v, want none for unfinished work", r.store.stamps)
	}
}

func TestSearchTargets_resume_stamp_needs_an_answer(t *testing.T) {
	t.Parallel()
	t.Run("only a gated provider: not stamped", func(t *testing.T) {
		t.Parallel()
		gated := &countingProvider{name: "hdbits"}
		r := newOutcomeRig(t, newRankedConfig(3, "hdbits"), gated)
		r.gate.binding.Observe(t.Context(), "hdbits", providergate.OpSearch, errRefused)

		result := r.search(t)
		if got := result.Langs[0]; got.Kind != subflux.LangSearched || got.Answered != 0 {
			t.Fatalf("LangOutcome = %+v, want a searched group with no answer", got)
		}
		if len(r.store.stamps) != 0 || r.store.failureCalled {
			t.Errorf("stamps = %v, backoff = %v; want neither for an unanswered sweep", r.store.stamps, r.store.failureCalled)
		}
	})
	t.Run("an answering provider with nothing: stamped and backed off", func(t *testing.T) {
		t.Parallel()
		empty := &countingProvider{name: "subdl"}
		r := newOutcomeRig(t, newRankedConfig(3, "subdl"), empty)

		r.search(t)
		if len(r.store.stamps) != 1 || !r.store.stamps[0].Searched || !r.store.failureCalled {
			t.Errorf("stamps = %v, backoff = %v; want one searched stamp and a backoff", r.store.stamps, r.store.failureCalled)
		}
	})
}

func TestProcessTargetVariant_results(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		provider *countingProvider
		target   subflux.SubtitleTarget
		want     targetResult
	}{
		{name: "saved", provider: &countingProvider{name: "subdl", results: candidates("subdl", 1)}, want: targetSaved},
		{name: "answered with nothing", provider: &countingProvider{name: "subdl"}, want: targetNoResult},
		{
			name: "filtered to nothing", provider: &countingProvider{name: "subdl", results: candidates("subdl", 1)},
			target: subflux.SubtitleTarget{Variant: subflux.VariantForced}, want: targetNoResult,
		},
		{name: "no provider answered", provider: &countingProvider{name: "subdl", searchErr: errors.New("HTTP 502")}, want: targetNone},
		{
			name: "candidates, none saved", provider: &countingProvider{name: "subdl", results: candidates("subdl", 1), downloadErr: errors.New("HTTP 502")},
			want: targetDownloadFailed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newOutcomeRig(t, newRankedConfig(3, "subdl"), tc.provider)
			req := &subflux.SearchRequest{MediaType: subflux.MediaTypeMovie, ImdbID: "tt0111161", Languages: []string{"fr"}}
			tc.target.Code = "fr"
			outcome := r.engine.searchProvidersFilteredInner(t.Context(), req, r.engine.providers)
			state := targetState{
				target: &tc.target, variant: tc.target.EffectiveVariant(),
				allowedProvs: map[subflux.ProviderID]struct{}{"subdl": {}}, needsSearch: true,
			}
			_, got, _ := r.engine.processTargetVariant(t.Context(), req, &state, &outcome,
				filepath.Join(t.TempDir(), "movie.mkv"), subflux.MediaTypeMovie, "tmdb-1", "fr", "Movie")
			if got != tc.want {
				t.Errorf("processTargetVariant(%s) = %d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
