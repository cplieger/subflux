package manualops

import (
	"context"
	"strings"
	"testing"

	"github.com/cplieger/subflux/internal/provider"
	"github.com/cplieger/subflux/internal/subflux"
)

// fakeSearchEngine counts what a manual search asks the engine to do, so the
// tests can assert the work that did NOT happen: hashing a video the request
// already carries a hash for, or scoring an empty candidate set.
type fakeSearchEngine struct {
	hash       string
	scored     []subflux.ScoredResult
	size       int64
	hashCalls  int
	scoreCalls int
}

func (e *fakeSearchEngine) HashFile(_ context.Context, _ string) (string, int64, error) {
	e.hashCalls++
	return e.hash, e.size, nil
}

func (e *fakeSearchEngine) ScoreSubtitles(_ *subflux.SearchRequest, _ []subflux.Subtitle) []subflux.ScoredResult {
	e.scoreCalls++
	return e.scored
}

func (e *fakeSearchEngine) SyncAndPostProcess(_ context.Context, data []byte, _, _ string, _ subflux.Variant) ([]byte, int64) {
	return data, 0
}

// oneSubProvider answers every search with a single candidate.
type oneSubProvider struct{}

func (oneSubProvider) Name() subflux.ProviderID { return "os" }

func (oneSubProvider) Search(context.Context, *subflux.SearchRequest) ([]subflux.Subtitle, error) {
	return []subflux.Subtitle{{Provider: "os", ID: "sub-1", Language: "en", ReleaseName: "Show.S01E01"}}, nil
}

func (oneSubProvider) Download(context.Context, *subflux.Subtitle) ([]byte, error) {
	return nil, nil
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

			TryComputeHash(t.Context(), ls, req, tc.filePath)

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
		engine := &fakeSearchEngine{scored: scored}
		store := &recStore{refs: []subflux.DownloadedRef{{Provider: "os", ReleaseName: "Show.S01E01"}}}
		ls := &LiveState{
			Cfg: fakeManualCfg{}, Engine: engine,
			Providers: []provider.Provider{oneSubProvider{}},
		}
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
